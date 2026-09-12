import type { LeaseOffer } from "./lease.js";

/**
 * What a provider node advertises about itself. Returned free from
 * `GET /v1/specs`, and (from M2) mirrored into the registry by heartbeats.
 */
export type NodeSpec = {
  /** Stable node identifier, generated at `cleargate-node setup`. */
  node_id: string;
  /** Agent build, so the registry can track version skew across nodes. */
  agent_version: string;
  /** Hedera account that receives payment. Payments are renter -> node, direct. */
  pay_to: string;
  /** Flat price for one job, in tinybars, as a string. Never a float. */
  price_tinybars: string;
  /** Facilitator this node settles through. */
  facilitator_url: string;
  /** CAIP-2 network, e.g. "hedera:testnet". */
  network: string;
  /** Asset id: "0.0.0" for HBAR, or an HTS token id. */
  asset: string;
  /**
   * The facilitator's fee payer, echoed so a client can pre-build a payment
   * without first triggering a 402. Absent when the facilitator was unreachable.
   */
  fee_payer?: string;
  gpu: GpuInfo;
  limits: JobLimits;
  /** Images this node is willing to run. Untrusted images are never accepted. */
  image_allowlist: string[];
  /**
   * Timed interactive access, if this node sells it.
   *
   * Absent on a node that did not opt in — which is the default, because
   * handing a stranger a live shell is a bigger trust ask than running their
   * sandboxed batch job and must never be switched on as a side effect.
   */
  leases?: LeaseOffer;
} & ProviderIdentity;

export type GpuInfo = {
  /**
   * False when the node runs in CPU-fallback mode — either no GPU, or the
   * NVIDIA container runtime is not installed. The payment flow is identical.
   */
  available: boolean;
  model: string | null;
  vram_mb: number | null;
  /** Why the GPU is unavailable, for operator diagnostics. */
  reason?: string;
};

/**
 * What a node POSTs to the registry's `/v1/nodes/heartbeat` on a timer.
 *
 * It is the node's own `NodeSpec` plus the two things only the node knows:
 * where renters can reach it, and whether its operator has it paused.
 */
/**
 * This provider's ERC-8004 identity, if they registered one.
 *
 * Optional throughout, and that is not an oversight: registration costs a
 * transaction and is explicitly opt-in, so a node without an agent id is a
 * perfectly normal node — it is simply not discoverable by id.
 */
export type ProviderIdentity = {
  /** The on-chain agent id. Absent, or 0, means unregistered. */
  agent_id?: number;
  /** The EVM address the identity is bound to on-chain. */
  agent_address?: string;
  /**
   * The contract that issued the id. Published because an agent id without its
   * registry resolves to nothing — the number alone says nothing about who
   * minted it.
   */
  identity_registry?: string;
  /**
   * The HCS topic this node publishes settlements to, if any. A renter can read
   * it before paying to see how the provider has actually behaved.
   */
  audit_topic?: string;
};

export type NodeHeartbeat = NodeSpec & {
  /** Absolute base URL renters use to reach this node, e.g. https://gpu.example. */
  public_url: string;
  /** True while the operator has the node refusing new jobs. */
  paused: boolean;
};

/** A node as the registry hands it back to the website. */
export type NodeListing = NodeHeartbeat & {
  /** True when the last heartbeat arrived inside the registry's freshness window. */
  online: boolean;
  /** ISO 8601 timestamp of the most recent heartbeat. */
  last_seen_at: string;
  /** ISO 8601 timestamp of the first heartbeat ever seen from this node. */
  first_seen_at: string;
};

export type JobLimits = {
  /** Wall-clock cap; the container is killed past this. */
  max_seconds: number;
  memory_mb: number;
  cpu_cores: number;
  /** Cap on the artifact tarball a renter can download. */
  max_artifact_mb: number;
};
