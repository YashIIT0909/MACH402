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
  /**
   * Absent on a node that does not sell interactive leases, which is the
   * default. Every number is bounded for the same reason the rest of this file
   * bounds things: the website renders it, and a hostile node would otherwise
   * choose what it says.
   */
  leases: z
    .object({
      price_tinybars_per_minute: z
        .string()
        .regex(/^\d{1,20}$/, "price_tinybars_per_minute must be a whole number of tinybars, as a string"),
      min_minutes: z.number().int().positive().max(10_080),
      max_minutes: z.number().int().positive().max(10_080),
      max_total_minutes: z.number().int().positive().max(525_600),
      ssh: z.boolean(),
      jupyter: z.boolean(),
      gpu: z.boolean(),
      memory_mb: z.number().int().positive().max(10_000_000),
      cpu_cores: z.number().int().positive().max(1024),
      workspace_gb: z.number().int().nonnegative().max(1_000_000),
      egress_allowlist: z.array(shortText).max(256),
      escrow_contract: shortText.optional(),
      price_tinybars_per_second: z
        .string()
        .regex(/^\d{1,20}$/, "price_tinybars_per_second must be a whole number of tinybars, as a string")
        .optional(),
    })
    .optional(),

  /**
   * This provider's ERC-8004 identity, if they registered one. All optional: a
   * node that never registered is normal, not incomplete.
   *
   * agent_address is shape-checked for the same reason public_url is — the
   * website renders it, and a node is not a trusted source of the strings it
   * puts on our pages. The registry does not and cannot verify that the
   * registration is real; that is what the contract is for.
   */
  agent_id: z.number().int().nonnegative().optional(),
  agent_address: z
    .string()
    .regex(/^0x[0-9a-fA-F]{40}$/, "agent_address must be a 0x-prefixed EVM address")
    .optional(),
  identity_registry: shortText.optional(),
  audit_topic: z
    .string()
    .regex(/^\d+\.\d+\.\d+$/, "audit_topic must be a Hedera topic id like 0.0.1234")
    .optional(),
});

export type Heartbeat = z.infer<typeof heartbeatSchema>;
