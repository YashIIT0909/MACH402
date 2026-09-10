/**
 * Validation for what nodes POST to the registry.
 *
 * Anyone on the internet can call the heartbeat endpoint, and whatever it
 * accepts ends up rendered on the website and handed to renters as a URL to
 * pay. So this is a boundary: nothing gets stored that has not been shaped and
 * bounded here.
 */
import { z } from "zod";

/** Bounded so a node cannot use the registry as free storage. */
const shortText = z.string().min(1).max(200);

/** Hedera shard.realm.num, e.g. 0.0.1234. Asset "0.0.0" is HBAR. */
const hederaId = z.string().regex(/^\d+\.\d+\.\d+$/, "expected a Hedera id like 0.0.1234");

/**
 * Absolute http(s) URL only.
 *
 * The scheme check is the point: the browse page turns this into a link, and a
 * "javascript:" or "data:" URL from a hostile node would be script injection
 * against everyone looking at the site.
 */
const httpUrl = z
  .string()
  .max(500)
  .refine((value) => {
    try {
      const { protocol } = new URL(value);
      return protocol === "http:" || protocol === "https:";
    } catch {
      return false;
    }
  }, "expected an absolute http(s) URL");

export const heartbeatSchema = z.object({
  node_id: z.string().regex(/^[A-Za-z0-9_-]{1,64}$/, "node_id must be url-safe and under 64 characters"),
  agent_version: shortText,
  public_url: httpUrl,
  pay_to: hederaId,
  // A string, always: amounts are never parsed into floats anywhere in
  // ClearGate, and tinybars overflow a double at scale.
  price_tinybars: z.string().regex(/^\d{1,20}$/, "price_tinybars must be a whole number of tinybars, as a string"),
  facilitator_url: httpUrl,
  network: z.string().regex(/^hedera:[a-z]+$/, "network must be a Hedera CAIP-2 id such as hedera:testnet"),
  asset: hederaId,
  fee_payer: hederaId.optional(),
  paused: z.boolean(),
  gpu: z.object({
    available: z.boolean(),
    model: shortText.nullable().optional(),
    vram_mb: z.number().int().nonnegative().max(1_000_000).nullable().optional(),
    reason: z.string().max(500).optional(),
  }),
  limits: z.object({
    max_seconds: z.number().int().positive(),
    memory_mb: z.number().int().positive(),
    cpu_cores: z.number().int().positive(),
    max_artifact_mb: z.number().int().positive(),
  }),
  image_allowlist: z.array(shortText).max(64),
});

export type Heartbeat = z.infer<typeof heartbeatSchema>;
