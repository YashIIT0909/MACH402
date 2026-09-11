/**
 * The ClearGate registry: discovery, and nothing else.
 *
 * Every route here is free and unauthenticated except the heartbeat, which a
 * node authorizes with its own listing token. The registry never receives,
 * holds or forwards funds (CLAUDE.md invariant 3) — a renter pays the node
 * directly at the public_url listed here.
 */
import Fastify, { type FastifyInstance } from "fastify";
import type pg from "pg";

import { cloudflareFromEnv, provisionTunnel } from "./cloudflare.js";
import { ONLINE_WINDOW_SECONDS } from "./db.js";
import { claimTunnel, getNode, listNodes, recordHeartbeat, recordTunnel, withdrawNode } from "./store.js";
import { heartbeatSchema } from "./validate.js";

export function buildServer(pool: pg.Pool): FastifyInstance {
  const app = Fastify({ logger: true });

  // Read once at startup. Null means this registry has no Cloudflare account,
  // which is a perfectly good registry — it just cannot hand out named tunnels,
  // and nodes fall back to quick ones.
  const cloudflare = cloudflareFromEnv();
  if (cloudflare === null) {
    app.log.info("no Cloudflare credentials configured; nodes must use quick tunnels for leases");
  }

  // free: liveness, and what `cleargate-node setup` preflights against.
  app.get("/health", async () => ({
    ok: true,
    online_window_seconds: ONLINE_WINDOW_SECONDS,
    // So a node can tell before it asks whether named tunnels are on offer.
    tunnels: cloudflare !== null,
  }));

  // node-token-gated: a node proves it owns its listing with the token minted
  // at `cleargate-node setup`. The first heartbeat for a node_id claims it.
  app.post("/v1/nodes/heartbeat", async (request, reply) => {
    const token = bearerToken(request.headers.authorization);
    if (token === null) {
      return reply.code(401).send({ error: "expected an Authorization: Bearer <registry_token> header" });
    }

    const parsed = heartbeatSchema.safeParse(request.body);
    if (!parsed.success) {
      return reply.code(400).send({
        error: "invalid heartbeat",
        issues: parsed.error.issues.map((issue) => ({
          field: issue.path.join("."),
          message: issue.message,
        })),
      });
    }

    const result = await recordHeartbeat(pool, parsed.data, token);
    if (result === "wrong-token") {
      // Deliberately not "this node is taken": whoever holds the listing does
      // not need to learn anything from a failed attempt on it.
      return reply.code(403).send({ error: "this node_id is registered to a different token" });
    }

    return reply.code(200).send({
      ok: true,
      node_id: parsed.data.node_id,
      claimed: result === "created",
      online_window_seconds: ONLINE_WINDOW_SECONDS,
    });
  });

  // node-token-gated: a node saying it is going away, so the site stops
  // offering it now instead of after the freshness window expires.
  app.post<{ Params: { id: string } }>("/v1/nodes/:id/offline", async (request, reply) => {
    const token = bearerToken(request.headers.authorization);
    if (token === null) {
      return reply.code(401).send({ error: "expected an Authorization: Bearer <registry_token> header" });
    }

    const result = await withdrawNode(pool, request.params.id, token);
    if (result === "unknown") {
      return reply.code(404).send({ error: "no such node" });
    }
    if (result === "wrong-token") {
      return reply.code(403).send({ error: "this node_id is registered to a different token" });
    }
    return reply.code(200).send({ ok: true, node_id: request.params.id });
  });

  // node-token-gated: a node asking for the Cloudflare tunnel that lets renters
  // reach a lease on it despite NAT.
  //
  // This is the only route that touches Cloudflare, and the platform's API
  // token never leaves this process — a provider never has a Cloudflare account
  // and never sees a credential beyond the one tunnel token returned here.
  //
  // It is deliberately not gated on the node having a listing. A node needs its
  // tunnel's API hostname *before* its first heartbeat, because that hostname
  // may be the public_url it announces.
  app.post<{ Params: { id: string } }>("/v1/nodes/:id/tunnel-token", async (request, reply) => {
    const token = bearerToken(request.headers.authorization);
    if (token === null) {
      return reply.code(401).send({ error: "expected an Authorization: Bearer <registry_token> header" });
    }
    if (!/^[A-Za-z0-9_-]{1,64}$/.test(request.params.id)) {
      return reply.code(400).send({ error: "node_id must be url-safe and under 64 characters" });
    }
    if (cloudflare === null) {
      // Not an error in the node's configuration — this registry simply has no
      // Cloudflare account. Say which knob to turn rather than just refusing.
      return reply.code(503).send({
        error:
          "this registry cannot provision tunnels: it has no Cloudflare credentials configured. " +
          "Run the node with leases.tunnel.mode \"quick\", which needs no Cloudflare account.",
      });
    }

    const claim = await claimTunnel(pool, request.params.id, token);
    if (claim.result === "wrong-token") {
      return reply.code(403).send({ error: "this node_id is registered to a different token" });
    }

    try {
      const provisioned = await provisionTunnel(
        cloudflare,
        request.params.id,
        claim.existing?.tunnel_id ?? null,
      );
      await recordTunnel(pool, request.params.id, token, {
        tunnel_id: provisioned.tunnelId,
        ssh_hostname: provisioned.sshHostname,
        jupyter_hostname: provisioned.jupyterHostname,
        api_hostname: provisioned.apiHostname,
      });

      return reply.code(200).send({
        tunnel_token: provisioned.token,
        ssh_hostname: provisioned.sshHostname,
        jupyter_hostname: provisioned.jupyterHostname,
        api_hostname: provisioned.apiHostname,
      });
    } catch (error: unknown) {
      // Logged in full here; the node gets the message without any hint of the
      // credentials that produced it.
      request.log.error({ err: error }, "tunnel provisioning failed");
      return reply.code(502).send({
        error: "could not provision a tunnel with Cloudflare; the node can fall back to quick tunnels",
      });
    }
  });

  // free: discovery must not cost money, or agents cannot shop around.
  app.get<{ Querystring: { online?: string } }>("/v1/nodes", async (request) => {
    const onlyOnline = request.query.online === "true";
    const nodes = await listNodes(pool, onlyOnline);
    return { nodes, online_window_seconds: ONLINE_WINDOW_SECONDS };
  });

  // free
  app.get<{ Params: { id: string } }>("/v1/nodes/:id", async (request, reply) => {
    const node = await getNode(pool, request.params.id);
    if (node === null) {
      return reply.code(404).send({ error: "no such node" });
    }
    return node;
  });

  return app;
}

function bearerToken(header: string | undefined): string | null {
  if (header === undefined || !header.startsWith("Bearer ")) return null;
  const token = header.slice("Bearer ".length).trim();
  return token === "" ? null : token;
}
