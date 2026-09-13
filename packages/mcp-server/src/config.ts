/**
 * This server's own configuration — every knob an agent's spend controls need
 * that a human at a keyboard would otherwise provide by just... not paying for
 * things it didn't mean to. See CLAUDE.md, "Secrets": only the renter side has
 * a key, and it comes from env — never a config file, never logged.
 */
import { loadEnv } from "./vendor/env.js";

export type McpConfig = {
  /** The mach402 registry this agent searches. No default — never guessed. */
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

  const registryUrl = process.env["mach402_REGISTRY_URL"]?.trim();
  if (!registryUrl) {
    throw new Error(
      "mach402_REGISTRY_URL must be set to the mach402 registry this agent should search " +
        "(e.g. your own `make dev-registry` instance, or one your provider gave you). " +
        "See .env.example.",
    );
  }

  const maxTinybarsPerPayment =
    readBigIntEnv("mach402_MAX_TINYBARS_PER_PAYMENT") ?? DEFAULT_MAX_TINYBARS_PER_PAYMENT;
  const confirmAboveTinybars = readBigIntEnv("mach402_CONFIRM_ABOVE_TINYBARS");

  // confirmAboveTinybars is meant to sit *below* the hard per-payment cap — "pause
  // and ask before spending this much, out of a ceiling that's never crossed at
  // all." If it's set above the cap instead, any quote in between is too big to
  // pay directly and never big enough to trigger confirmation either: it always
  // fails, confirmed or not. Catching that here turns a mysterious runtime
  // payment rejection into a clear misconfiguration error at startup.
  if (confirmAboveTinybars !== null && confirmAboveTinybars > maxTinybarsPerPayment) {
    throw new Error(
      `mach402_CONFIRM_ABOVE_TINYBARS (${confirmAboveTinybars}) must not exceed ` +
        `mach402_MAX_TINYBARS_PER_PAYMENT (${maxTinybarsPerPayment}) — otherwise a payment priced ` +
        `between the two can never go through, confirmed or not. Raise ` +
        `mach402_MAX_TINYBARS_PER_PAYMENT to at least that much, or lower the confirmation threshold.`,
    );
  }

  return {
    registryUrl,
    network: process.env["HEDERA_NETWORK"] ?? "hedera:testnet",
    maxTinybarsPerPayment,
    maxTinybarsPerDay: readBigIntEnv("mach402_MAX_TINYBARS_PER_DAY"),
    confirmAboveTinybars,
  };
}
