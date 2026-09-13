/**
 * What a node sells.
 *
 * A lease sends nothing of the renter's to the provider: the renter gets a
 * container on the provider's GPU and connects to it themselves, through
 * Jupyter. Their code and data never leave their machine.
 *
 * A lease is only ever bought as a metered session — see `session.ts` for the
 * request, response and state shapes. This file is the offer a node advertises.
 */

/**
 * What a node advertises about the leases it sells, in `GET /v1/specs` and in
 * its registry heartbeat. Absent on a node that did not opt into leasing.
 */
export type LeaseOffer = {
  price_tinybars_per_minute: string;
  min_minutes: number;
  max_minutes: number;
  /** Lifetime cap across top-ups, so a node is never leased out forever. */
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
   * How this node's interactive time is paid for.
   *
   * Always "session" on a current node: a paid-up credit metered second by
   * second, with the unburned remainder refunded when the session ends.
   * "direct" — prepaid slices, no refund — or an absent field comes from a
   * build older than that, and is not something a current client buys.
   */
  payment_mode?: "direct" | "session";

  /**
   * The rate a session burns at, in tinybars per second.
   *
   * Present alongside `payment_mode: "session"`. Per second rather than per
   * minute because that is the granularity a refund is computed at.
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
