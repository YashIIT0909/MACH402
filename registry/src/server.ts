/**
 * The MACH402 registry: discovery, and nothing else.
 *
 * Every route here is free and unauthenticated except the heartbeat, which a
 * node authorizes with its own listing token. The registry never receives,
 * holds or forwards funds (CLAUDE.md invariant 3) — a renter pays the node
 * directly at the public_url listed here.
 */
import Fastify, { type FastifyInstance } from "fastify";
import type pg from "pg";

import { ONLINE_WINDOW_SECONDS } from "./db.js";
import { toProviderView } from "./providers.js";
import { getNode, listNodes, recordHeartbeat, withdrawNode } from "./store.js";
import { heartbeatSchema } from "./validate.js";

export function buildServer(pool: pg.Pool): FastifyInstance {
  const app = Fastify({ logger: true });

  // free: liveness, and what `cleargate-node setup` preflights against.
  app.get("/health", async () => ({
    ok: true,
    online_window_seconds: ONLINE_WINDOW_SECONDS,
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

  // free: the agent-facing view of the same rows /v1/nodes serves.
  //
  // A second projection rather than a second store. /v1/nodes keeps its shape
  // because the website and every deployed node already read it; this one uses
  // the names an autonomous renter looks for and carries the ERC-8004 agent id.
  app.get<{ Querystring: { online?: string } }>("/v1/providers", async (request) => {
    const onlyOnline = request.query.online === "true";
    const nodes = await listNodes(pool, onlyOnline);
    return {
      providers: nodes.map(toProviderView),
      online_window_seconds: ONLINE_WINDOW_SECONDS,
    };
  });

  // free
  app.get<{ Params: { id: string } }>("/v1/providers/:id", async (request, reply) => {
    const node = await getNode(pool, request.params.id);
    if (node === null) {
      return reply.code(404).send({ error: "no such provider" });
    }
    return toProviderView(node);
  });

  return app;
}

function bearerToken(header: string | undefined): string | null {
  if (header === undefined || !header.startsWith("Bearer ")) return null;
  const token = header.slice("Bearer ".length).trim();
  return token === "" ? null : token;
}
