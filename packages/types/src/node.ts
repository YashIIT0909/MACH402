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
  gpu: GpuInfo;
  limits: JobLimits;
  /** Images this node is willing to run. Untrusted images are never accepted. */
  image_allowlist: string[];
};

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

export type JobLimits = {
  /** Wall-clock cap; the container is killed past this. */
  max_seconds: number;
  memory_mb: number;
  cpu_cores: number;
  /** Cap on the artifact tarball a renter can download. */
  max_artifact_mb: number;
};
