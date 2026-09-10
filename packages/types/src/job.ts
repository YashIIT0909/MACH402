/**
 * Job lifecycle types. Flat-fee mode (M1); metered leases arrive in M3 and
 * flat-fee stays functional behind a config flag as the demo fallback.
 */

/** What a renter POSTs to `/v1/jobs` (x402-gated). */
export type JobSpec = {
  /** Must be in the node's `image_allowlist`. */
  image: string;
  /** Command override. Defaults to the image entrypoint. */
  cmd?: string[];
  /**
   * Optional script written into the container's working directory before
   * start. Base64 so binary payloads and newlines survive JSON transport.
   */
  script?: {
    filename: string;
    content_base64: string;
  };
  /**
   * Training data the node downloads on the renter's behalf and mounts at `/data`.
   *
   * The *node* fetches this, never the container: jobs run with no network at
   * all, so a dataset URL is the only way data gets in. The URL is preflighted
   * before the renter is asked to pay, and downloaded after settlement.
   */
  dataset?: DatasetSpec;
  /** Non-secret environment variables for the job. */
  env?: Record<string, string>;
  /** Requested wall-clock seconds, clamped to the node's `limits.max_seconds`. */
  timeout_seconds?: number;
  /**
   * Refuse to run on a node without a usable GPU. Checked before the 402, so a
   * CPU-only node rejects the job for free rather than taking payment for work
   * it cannot do properly.
   */
  require_gpu?: boolean;
};

export type DatasetSpec = {
  /** https only by default; the node's policy decides. */
  url: string;
  /** Verified as the file streams in. A mismatch fails the job. */
  sha256?: string;
  /** Overrides the filename taken from the URL. */
  filename?: string;
  /** Keep `.tar`/`.tar.gz`/`.tgz`/`.zip` archives intact instead of unpacking. */
  no_extract?: boolean;
};

export type JobStatus =
  | "pending"
  /** Pulling the image and downloading the dataset — after payment, before the container runs. */
  | "staging"
  | "running"
  | "succeeded"
  | "failed"
  | "timeout"
  | "killed";

/** Terminal states — no further transitions, artifacts are ready. */
export const TERMINAL_JOB_STATUSES: readonly JobStatus[] = [
  "succeeded",
  "failed",
  "timeout",
  "killed",
];

export function isTerminal(status: JobStatus): boolean {
  return TERMINAL_JOB_STATUSES.includes(status);
}

/** 200 response to a paid `POST /v1/jobs`. */
export type JobCreated = {
  job_id: string;
  /** Opaque bearer token, scoped to this one job. Grants logs and artifact. */
  token: string;
  status: JobStatus;
  /** Hedera transaction id of the settlement, `0.0.x@seconds.nanos`. */
  transaction: string;
  payer: string;
  amount_tinybars: string;
};

/** `GET /v1/jobs/:id` (job-token gated). */
export type JobState = {
  job_id: string;
  status: JobStatus;
  exit_code: number | null;
  started_at: string | null;
  ended_at: string | null;
  error: string | null;
  artifact_ready: boolean;
  /** What a staging job is waiting on, e.g. "downloading dataset 240 MiB / 1.2 GiB". */
  stage?: string;
  image?: string;
  gpu?: boolean;
};

/**
 * One line of the node's append-only receipt file. The provider must be able to
 * audit earnings without trusting our website, so this is written locally on
 * every settlement (and, from M4, mirrored to HCS).
 */
export type Receipt = {
  job_id: string;
  transaction: string;
  payer: string;
  pay_to: string;
  amount_tinybars: string;
  asset: string;
  network: string;
  settled_at: string;
};
