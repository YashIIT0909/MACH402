/**
 * Trimmed, vendored copies of the ClearGate wire types this package touches —
 * session lifecycle and node discovery only, since this package never opens a
 * flat-fee job or a `direct`-mode lease. Originals live in `packages/types`;
 * this package doesn't depend on that package (or any other workspace
 * package) so it can be published and run standalone. Amounts are strings in
 * tinybars throughout — never parse one into a float.
 *
 * The x402 wire types (`PaymentRequired`, `PaymentRequirements`,
 * `SettleResponse`) are *not* copied here — they're imported type-only from
 * `@x402/core/types`, an ordinary npm dependency, so this package can't drift
 * from the real facilitator contract.
 */

export type GpuInfo = {
  available: boolean;
  model: string | null;
  vram_mb: number | null;
  reason?: string;
};

export type ProviderIdentity = {
  agent_id?: number;
  agent_address?: string;
  identity_registry?: string;
  audit_topic?: string;
};

/**
 * What a node advertises about the leases/sessions it sells. Session-specific
 * fields (`payment_mode`, `price_tinybars_per_second`, `chunk_seconds`) are
 * bolted onto the same offer object the lease path uses — there is no
 * separate "sessions" field on `NodeSpec`.
 */
export type LeaseOffer = {
  price_tinybars_per_minute: string;
  min_minutes: number;
  max_minutes: number;
  max_total_minutes: number;
  ssh: boolean;
  jupyter: boolean;
  gpu: boolean;
  memory_mb: number;
  cpu_cores: number;
  workspace_gb: number;
  egress_allowlist: string[];
  /** Absent means "direct" — a node running a build from before sessions existed. */
  payment_mode?: "direct" | "session";
  price_tinybars_per_second?: string;
  chunk_seconds?: number;
};

/** What a provider node advertises about itself — `GET /v1/specs`, free. */
export type NodeSpec = {
  node_id: string;
  agent_version: string;
  pay_to: string;
  price_tinybars: string;
  facilitator_url: string;
  network: string;
  asset: string;
  gpu: GpuInfo;
  image_allowlist: string[];
  /** Absent on a node that never opted into leasing/sessions. */
  leases?: LeaseOffer;
} & ProviderIdentity;

export type NodeHeartbeat = NodeSpec & {
  public_url: string;
  paused: boolean;
};

/** A node as the registry hands it back — `GET /v1/nodes`. */
export type NodeListing = NodeHeartbeat & {
  online: boolean;
  last_seen_at: string;
  first_seen_at: string;
};

/** What a renter POSTs to `/v1/sessions` and `.../topup` (x402-gated). */
export type SessionSpec = {
  /** Seconds of credit to buy up front, bounded by the node's offer. */
  seconds: number;
  /** The renter's SSH public key. The private half never leaves their machine. */
  public_key: string;
  require_gpu?: boolean;
};

export type SessionStatus =
  | "provisioning"
  | "active"
  | "paused"
  | "stopped"
  | "expired"
  | "failed";

/** The 200 body of a paid `POST /v1/sessions`. */
export type SessionCreated = {
  session_id: string;
  lease_id: string;
  token?: string;
  status: SessionStatus;
  expires_at: string;

  certificate: string;
  ssh_user: string;
  ssh_host?: string;
  ssh_principal: string;

  jupyter_url: string;
  jupyter_token: string;
  tunnel_mode: "named" | "quick" | "off";

  transaction: string;
  payer: string;
  amount_tinybars: string;

  price_tinybars_per_second: string;
  /** Unburned credit: what the node owes back if the session stopped now. */
  credit_tinybars: string;
  low_credits: boolean;
  seconds: number;
};

/** `GET /v1/sessions/:id` — free, what a polling loop reads. */
export type SessionState = {
  session_id: string;
  lease_id: string;
  status: SessionStatus;
  created_at: string;
  expires_at: string;
  seconds_remaining: number;
  paid_seconds: number;
  gpu: boolean;

  price_tinybars_per_second: string;
  credit_tinybars: string;
  burned_tinybars: string;
  refunded_tinybars: string;
  low_credits: boolean;

  settle_state: "pending" | "done";
  error: string | null;
};
