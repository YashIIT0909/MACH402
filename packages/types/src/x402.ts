/**
 * x402 v2 protocol constants.
 *
 * IMPORTANT — v2 renamed the HTTP headers. `X-PAYMENT` and `X-PAYMENT-RESPONSE`
 * are the *v1* names and are only read for backwards compatibility. In v2:
 *
 *   client  --PAYMENT-SIGNATURE--> server    (base64 PaymentPayload)
 *   server  --PAYMENT-REQUIRED-->  client    (base64 PaymentRequired, on 402)
 *   server  --PAYMENT-RESPONSE-->  client    (base64 SettleResponse, on 200)
 *
 * Verified against @x402/core@2.25.0 `dist/cjs/http/index.js`.
 */
export const X402_VERSION = 2 as const;

/** Header the client puts the signed payment payload in (v2). */
export const HEADER_PAYMENT_SIGNATURE = "PAYMENT-SIGNATURE";
/** Header the resource server puts the 402 challenge in (v2). */
export const HEADER_PAYMENT_REQUIRED = "PAYMENT-REQUIRED";
/** Header the resource server puts the settlement receipt in (v2). */
export const HEADER_PAYMENT_RESPONSE = "PAYMENT-RESPONSE";
/** v1 header names, accepted on input only. */
export const HEADER_PAYMENT_SIGNATURE_V1 = "X-PAYMENT";
export const HEADER_PAYMENT_RESPONSE_V1 = "X-PAYMENT-RESPONSE";

/** 402 responses must never be cached — they are single-use challenges. */
export const PAYMENT_REQUIRED_CACHE_CONTROL = "no-store";

/** CAIP-2 identifiers. */
export const HEDERA_TESTNET = "hedera:testnet";
export const HEDERA_MAINNET = "hedera:mainnet";

/** The x402 asset id for native HBAR. An HTS token id goes here instead. */
export const HBAR_ASSET_ID = "0.0.0";

/** 1 HBAR = 100,000,000 tinybars. Amounts are strings end to end — never floats. */
export const TINYBARS_PER_HBAR = 100_000_000n;

/** The scheme ClearGate settles with. */
export const SCHEME_EXACT = "exact";

/**
 * Hedera `extra` block on PaymentRequirements. `feePayer` must equal the
 * facilitator's advertised signer from `GET /supported`, or the client SDK
 * throws before signing.
 */
export type HederaPaymentExtra = {
  feePayer: string;
};

/** Formats a tinybar amount for display without floating point arithmetic. */
export function formatTinybars(tinybars: string): string {
  const value = BigInt(tinybars);
  const whole = value / TINYBARS_PER_HBAR;
  const frac = (value % TINYBARS_PER_HBAR).toString().padStart(8, "0").replace(/0+$/, "");
  return frac.length > 0 ? `${whole}.${frac} HBAR` : `${whole} HBAR`;
}

/** HashScan link for a settled transaction id (`0.0.x@seconds.nanos`). */
export function hashscanUrl(transactionId: string, network = HEDERA_TESTNET): string {
  const net = network === HEDERA_MAINNET ? "mainnet" : "testnet";
  return `https://hashscan.io/${net}/transaction/${transactionId}`;
}
