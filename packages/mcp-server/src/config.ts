/**
 * This server's own configuration — every knob an agent's spend controls need
 * that a human at a keyboard would otherwise provide by just... not paying for
 * things it didn't mean to. See CLAUDE.md, "Secrets": only the renter side has
 * a key, and it comes from env — never a config file, never logged.
 */
import { loadEnv } from "./vendor/env.js";

export type McpConfig = {
  /** The ClearGate registry this agent searches. No default — never guessed. */
  registryUrl: string;
  /** CAIP-2 network. Testnet only for now. */
  network: string;
  /** Refuse any single payment above this many tinybars. */
  maxTinybarsPerPayment: bigint;
  /** Refuse a payment that would push today's total spend past this. null = no cap. */
  maxTinybarsPerDay: bigint | null;
  /** A payment above this pauses for confirm_payment instead of executing immediately. null = never pause. */
  confirmAboveTinybars: bigint | null;
};

const DEFAULT_MAX_TINYBARS_PER_PAYMENT = 10_000_000n; // 0.1 HBAR

function readBigIntEnv(name: string): bigint | null {
  const raw = process.env[name]?.trim();
  if (raw === undefined || raw === "") return null;
  try {
    return BigInt(raw);
  } catch {
    throw new Error(`${name} must be an integer number of tinybars, got "${raw}"`);
  }
}

export function loadConfig(): McpConfig {
  loadEnv();

  const registryUrl = process.env["CLEARGATE_REGISTRY_URL"]?.trim();
  if (!registryUrl) {
    throw new Error(
      "CLEARGATE_REGISTRY_URL must be set to the ClearGate registry this agent should search " +
        "(e.g. your own `make dev-registry` instance, or one your provider gave you). " +
        "See .env.example.",
    );
  }

  return {
    registryUrl,
    network: process.env["HEDERA_NETWORK"] ?? "hedera:testnet",
    maxTinybarsPerPayment: readBigIntEnv("CLEARGATE_MAX_TINYBARS_PER_PAYMENT") ?? DEFAULT_MAX_TINYBARS_PER_PAYMENT,
    maxTinybarsPerDay: readBigIntEnv("CLEARGATE_MAX_TINYBARS_PER_DAY"),
    confirmAboveTinybars: readBigIntEnv("CLEARGATE_CONFIRM_ABOVE_TINYBARS"),
  };
}
