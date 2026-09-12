/**
 * Lease lifecycle types — the metered, interactive half of ClearGate.
 *
 * A job sends the renter's code to the provider and runs it in a box with no
 * network. A lease sends nothing: the renter buys minutes on a container on the
 * provider's GPU and connects to it themselves, over SSH or from Colab's
 * "Connect to a local runtime". Their code and data never leave their machine.
 *
 * Flat-fee jobs stay functional alongside this and remain the demo fallback
 * (CLAUDE.md invariant 8).
 */

/** What a renter POSTs to `/v1/leases` and `/v1/leases/:id/extend` (x402-gated). */
export type LeaseSpec = {
  /**
   * Minutes being bought in this slice, within the node's advertised
   * `min_minutes`/`max_minutes`. An extension buys another slice; a renter
   * stops paying by simply not extending.
   */
  minutes: number;
  /**
   * The renter's own SSH public key — the public half only.
   *
   * The node signs this into a certificate scoped to one lease and never sees
   * the private half, which stays on the renter's machine. This mirrors the
   * node never holding a Hedera key: neither side ever holds the other's
   * secret.
   */
  public_key: string;
  /**
   * Refuse a CPU-fallback node before paying. Checked before the 402, so the
   * rejection costs nothing.
   */
  require_gpu?: boolean;
};

/**
 * 200 response to a paid `POST /v1/leases` or `/extend`.
 *
 * Everything needed to connect is here, and it is returned exactly once — the
 * certificate and the Jupyter token are not fetchable again afterwards.
 */
export type LeaseCreated = {
  lease_id: string;
  /**
   * Bearer token for reading and stopping this lease. Absent on an extension,
   * which reuses the token issued when the lease was created.
   */
  token?: string;
  status: LeaseStatus;
  /** ISO 8601. When the currently paid time runs out. */
  expires_at: string;

  /**
   * The renter's public key signed by this node's certificate authority, valid
   * until `expires_at` and only for the principal below. Extending re-signs a
   * fresh certificate rather than extending this one.
   */
  certificate: string;
  ssh_user: string;
  /** Empty when the node's tunnel mode carries HTTP only — see `tunnel_mode`. */
  ssh_host?: string;
  /** The certificate principal, which is the lease id. */
  ssh_principal: string;

  jupyter_url: string;
  jupyter_token: string;
  /**
   * How the node published this lease.
   *
   *  - `named` — a Cloudflare named tunnel: stable hostnames, SSH and Jupyter.
   *  - `quick` — a zero-setup tunnel: random hostname, Jupyter only, no SSH.
   *  - `off`   — no tunnel; the addresses are only reachable on the same network.
   */
  tunnel_mode: "named" | "quick" | "off";

  /** Hedera transaction id of this slice's settlement, `0.0.x@seconds.nanos`. */
  transaction: string;
  payer: string;
  amount_tinybars: string;
  minutes: number;
};

export type LeaseStatus =
  /** Container starting, tunnel not yet proven reachable. Nothing settled yet. */
  | "provisioning"
  | "active"
  /** Paid time lapsed: the container is frozen, not killed, so work is not lost. */
  | "paused"
  /** Ended at the renter's request. */
  | "stopped"
  /** Grace period ran out after a freeze; container killed and workspace wiped. */
  | "expired"
  /** Never came up, or its transport died. Nothing is charged for a failed create. */
  | "failed";

/** Terminal states — the container and its workspace are gone. */
export const TERMINAL_LEASE_STATUSES: readonly LeaseStatus[] = ["stopped", "expired", "failed"];

export function isLeaseTerminal(status: LeaseStatus): boolean {
  return TERMINAL_LEASE_STATUSES.includes(status);
}

/**
 * `GET /v1/leases/:id` (lease-token gated, free).
 *
 * The renter's auto-extend loop polls this, which is why it costs nothing:
 * charging for the check that decides whether to pay would be absurd.
 */
export type LeaseState = {
  lease_id: string;
  status: LeaseStatus;
  created_at: string;
  expires_at: string;
  /** 0 once the paid slice has lapsed, whether or not the lease is frozen yet. */
  seconds_remaining: number;
  /** Total minutes bought across every slice, against the node's lifetime cap. */
  paid_minutes: number;
  gpu: boolean;
  error: string | null;
};

/**
 * What a node advertises about the leases it sells, in `GET /v1/specs` and in
 * its registry heartbeat. Absent on a node that did not opt into leasing.
 */
export type LeaseOffer = {
  price_tinybars_per_minute: string;
  min_minutes: number;
  max_minutes: number;
  /** Lifetime cap across extensions, so a node is never leased out forever. */
  max_total_minutes: number;
  /** False on a node whose tunnel carries HTTP only: Jupyter works, SSH does not. */
  ssh: boolean;
  jupyter: boolean;
  /**
   * Whether a lease container can actually compute on this node's card.
   *
   * Narrower than the node-level `gpu.available`, which describes the host. A
   * node can have a working GPU and a lease image built without a CUDA runtime,
   * in which case the container lists the device and fails every kernel launch.
   * The node refuses `require_gpu` leases in that state, so this is what to
   * check before paying for GPU time.
   */
  gpu: boolean;
  memory_mb: number;
  cpu_cores: number;
  workspace_gb: number;
  /**
   * Hosts a lease container may reach. Published because it is a real limit on
   * the work a renter can do: a lease that cannot reach the index they need is
   * not the lease they wanted.
   */
  egress_allowlist: string[];

  /**
   * How this node's interactive time is paid for, and therefore whether
   * stopping early gives anything back.
   *
   * "direct" is forward payment per slice: what a renter buys is theirs
   * whether they use it or not. "session" meters a paid-up credit second by
   * second and refunds the unburned remainder when the session ends.
   *
   * Absent on a node running a build from before sessions existed, which means
   * "direct" — a client should decide which flow to use from this field rather
   * than from trying one and seeing what happens.
   */
  payment_mode?: "direct" | "session";

  /**
   * The rate a session burns at, in tinybars per second.
   *
   * Present alongside `payment_mode: "session"`. Per second rather than per
   * minute because that is the granularity a refund is computed at — the whole
   * reason a renter would choose this flow.
   */
  price_tinybars_per_second?: string;

  /**
   * The most time one session payment ever buys, in seconds.
   *
   * This is the renter's exposure: the node holds at most one chunk's worth of
   * their money ahead of the compute it pays for, and the running "owed if you
   * stopped now" figure is published to the node's audit topic every fifteen
   * seconds on top of that.
   */
  chunk_seconds?: number;
};
