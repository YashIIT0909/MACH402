/**
 * The registry's whole schema: one row per node, replaced on every heartbeat.
 *
 * There is deliberately no payments table, no balance and no ledger. Payments
 * are renter -> node, direct, and the registry is not in that path
 * (CLAUDE.md invariant 3). All it knows is where nodes are and what they claim
 * to offer.
 */
export const SCHEMA_SQL = `
CREATE TABLE IF NOT EXISTS nodes (
  node_id          TEXT PRIMARY KEY,

  -- Set by the first heartbeat and checked on every later one, so a node's
  -- listing cannot be repointed at someone else's machine.
  token_sha256     TEXT        NOT NULL,

  public_url       TEXT        NOT NULL,
  agent_version    TEXT        NOT NULL,
  pay_to           TEXT        NOT NULL,
  price_tinybars   TEXT        NOT NULL,
  facilitator_url  TEXT        NOT NULL,
  network          TEXT        NOT NULL,
  asset            TEXT        NOT NULL,
  fee_payer        TEXT,
  paused           BOOLEAN     NOT NULL,

  -- Set when a node announces its own shutdown, cleared by its next heartbeat.
  -- Without it a stopped node keeps reading as online until its last beat ages
  -- out, which looks to a provider like the site ignoring them.
  withdrawn        BOOLEAN     NOT NULL DEFAULT FALSE,

  gpu              JSONB       NOT NULL,
  limits           JSONB       NOT NULL,
  image_allowlist  JSONB       NOT NULL,

  first_seen_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
  last_seen_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- The browse page orders by "who is up right now".
CREATE INDEX IF NOT EXISTS nodes_last_seen_at_idx ON nodes (last_seen_at DESC);

-- Brings a table created before withdrawal existed up to date. This is the
-- whole migration story for now; when the schema starts changing under a
-- running deployment it needs to become a real ordered list.
ALTER TABLE nodes ADD COLUMN IF NOT EXISTS withdrawn BOOLEAN NOT NULL DEFAULT FALSE;

-- What a node advertises about the interactive leases it sells, or NULL on a
-- node that did not opt in. Replaced wholesale by each heartbeat like the rest
-- of the row.
ALTER TABLE nodes ADD COLUMN IF NOT EXISTS leases JSONB;

-- Which Cloudflare tunnel belongs to which node.
--
-- Separate from the nodes table on purpose. A node asks for a tunnel before it
-- has ever heartbeated — it needs the tunnel's API hostname to know its own
-- public URL — so this cannot depend on a listing row existing yet.
--
-- It holds no Cloudflare credentials. The platform's API token lives in the
-- registry's environment and is never written down here; all that is stored is
-- which tunnel id was created for which node.
CREATE TABLE IF NOT EXISTS node_tunnels (
  node_id          TEXT PRIMARY KEY,

  -- Trust on first use, the same rule as a listing: whoever claims a node_id
  -- first owns its tunnel, and every later request must present the same token.
  -- Without this, anyone could ask for the tunnel credential of an established
  -- node and receive traffic meant for someone else's machine.
  token_sha256     TEXT        NOT NULL,

  tunnel_id        TEXT        NOT NULL,
  ssh_hostname     TEXT        NOT NULL,
  jupyter_hostname TEXT        NOT NULL,
  api_hostname     TEXT        NOT NULL,

  created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
  last_issued_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
`;
