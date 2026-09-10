/**
 * Every read and write the registry performs.
 *
 * A node's row is entirely replaced by each heartbeat, so the listing always
 * reflects what the node says about itself right now — not a stale merge of
 * what it used to say.
 */
import { createHash, timingSafeEqual } from "node:crypto";
import type pg from "pg";

import type { GpuInfo, JobLimits, NodeListing } from "@cleargate/types";

import { ONLINE_WINDOW_SECONDS } from "./db.js";
import type { Heartbeat } from "./validate.js";

/** Why a heartbeat was not applied. */
export type HeartbeatResult = "created" | "updated" | "wrong-token";

type NodeRow = {
  node_id: string;
  public_url: string;
  agent_version: string;
  pay_to: string;
  price_tinybars: string;
  facilitator_url: string;
  network: string;
  asset: string;
  fee_payer: string | null;
  paused: boolean;
  gpu: GpuInfo;
  limits: JobLimits;
  image_allowlist: string[];
  first_seen_at: Date;
  last_seen_at: Date;
  online: boolean;
};

function hashToken(token: string): string {
  return createHash("sha256").update(token).digest("hex");
}

/** Constant time, so a wrong token cannot be found one character at a time. */
function tokensMatch(a: string, b: string): boolean {
  const left = Buffer.from(a, "utf8");
  const right = Buffer.from(b, "utf8");
  return left.length === right.length && timingSafeEqual(left, right);
}

/**
 * Records a heartbeat, claiming the node_id on the first one ever seen.
 *
 * Trust on first use: whoever announces a node_id first owns it, and every
 * later heartbeat must present the same token. Without this, anyone could
 * repoint an established listing at their own machine and collect jobs — and
 * payments — meant for someone else's GPU.
 */
export async function recordHeartbeat(
  pool: pg.Pool,
  beat: Heartbeat,
  token: string,
): Promise<HeartbeatResult> {
  const existing = await pool.query<{ token_sha256: string }>(
    "SELECT token_sha256 FROM nodes WHERE node_id = $1",
    [beat.node_id],
  );

  const incoming = hashToken(token);
  const claimed = existing.rows[0];

  if (claimed !== undefined && !tokensMatch(claimed.token_sha256, incoming)) {
    return "wrong-token";
  }

  await pool.query(
    `INSERT INTO nodes (
       node_id, token_sha256, public_url, agent_version, pay_to, price_tinybars,
       facilitator_url, network, asset, fee_payer, paused, gpu, limits, image_allowlist,
       first_seen_at, last_seen_at
     ) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12::jsonb, $13::jsonb, $14::jsonb, now(), now())
     ON CONFLICT (node_id) DO UPDATE SET
       -- A node that is beating again has plainly come back.
       withdrawn       = false,
       public_url      = EXCLUDED.public_url,
       agent_version   = EXCLUDED.agent_version,
       pay_to          = EXCLUDED.pay_to,
       price_tinybars  = EXCLUDED.price_tinybars,
       facilitator_url = EXCLUDED.facilitator_url,
       network         = EXCLUDED.network,
       asset           = EXCLUDED.asset,
       fee_payer       = EXCLUDED.fee_payer,
       paused          = EXCLUDED.paused,
       gpu             = EXCLUDED.gpu,
       limits          = EXCLUDED.limits,
       image_allowlist = EXCLUDED.image_allowlist,
       last_seen_at    = now()`,
    [
      beat.node_id,
      incoming,
      beat.public_url,
      beat.agent_version,
      beat.pay_to,
      beat.price_tinybars,
      beat.facilitator_url,
      beat.network,
      beat.asset,
      beat.fee_payer ?? null,
      beat.paused,
      JSON.stringify(beat.gpu),
      JSON.stringify(beat.limits),
      JSON.stringify(beat.image_allowlist),
    ],
  );

  return claimed === undefined ? "created" : "updated";
}

/**
 * Online means two things at once: the node has not said goodbye, and it is
 * still beating. The first catches a clean shutdown instantly; the second
 * catches a node that was unplugged and never got to say anything.
 */
const IS_ONLINE = `(NOT withdrawn AND last_seen_at > now() - make_interval(secs => $1::double precision))`;

const SELECT_NODE = `
  SELECT node_id, public_url, agent_version, pay_to, price_tinybars, facilitator_url,
         network, asset, fee_payer, paused, gpu, limits, image_allowlist,
         first_seen_at, last_seen_at,
         ${IS_ONLINE} AS online
  FROM nodes
`;

/**
 * Marks a node offline right now, at its own request.
 *
 * The row stays: a renter looking for a node they used yesterday should find it
 * listed as offline rather than silently vanished, and the node reclaims the
 * listing on its next heartbeat.
 */
export async function withdrawNode(
  pool: pg.Pool,
  nodeId: string,
  token: string,
): Promise<"withdrawn" | "wrong-token" | "unknown"> {
  const existing = await pool.query<{ token_sha256: string }>(
    "SELECT token_sha256 FROM nodes WHERE node_id = $1",
    [nodeId],
  );

  const claimed = existing.rows[0];
  if (claimed === undefined) return "unknown";
  if (!tokensMatch(claimed.token_sha256, hashToken(token))) return "wrong-token";

  await pool.query("UPDATE nodes SET withdrawn = true WHERE node_id = $1", [nodeId]);
  return "withdrawn";
}

/** Lists nodes, most recently seen first. Online ones sort above offline ones. */
export async function listNodes(pool: pg.Pool, onlyOnline: boolean): Promise<NodeListing[]> {
  const rows = await pool.query<NodeRow>(
    `${SELECT_NODE}
     WHERE $2::boolean = false OR ${IS_ONLINE}
     ORDER BY online DESC, last_seen_at DESC`,
    [ONLINE_WINDOW_SECONDS, onlyOnline],
  );
  return rows.rows.map(toListing);
}

export async function getNode(pool: pg.Pool, nodeId: string): Promise<NodeListing | null> {
  const rows = await pool.query<NodeRow>(`${SELECT_NODE} WHERE node_id = $2`, [
    ONLINE_WINDOW_SECONDS,
    nodeId,
  ]);
  const row = rows.rows[0];
  return row === undefined ? null : toListing(row);
}

function toListing(row: NodeRow): NodeListing {
  return {
    node_id: row.node_id,
    agent_version: row.agent_version,
    public_url: row.public_url,
    pay_to: row.pay_to,
    price_tinybars: row.price_tinybars,
    facilitator_url: row.facilitator_url,
    network: row.network,
    asset: row.asset,
    ...(row.fee_payer === null ? {} : { fee_payer: row.fee_payer }),
    paused: row.paused,
    gpu: row.gpu,
    limits: row.limits,
    image_allowlist: row.image_allowlist,
    online: row.online,
    first_seen_at: row.first_seen_at.toISOString(),
    last_seen_at: row.last_seen_at.toISOString(),
  };
}
