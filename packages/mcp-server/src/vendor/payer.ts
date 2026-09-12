/**
 * The x402 paying-fetch machinery, vendored from client/src/pay.ts +
 * client/src/payment.ts.
 *
 * All Hedera signing in ClearGate lives in TypeScript, backed by `@x402/hedera`
 * (CLAUDE.md invariant 2). This package is standalone, so it carries its own
 * copy rather than importing `client/`'s — see the note in package.json.
 */
import { PrivateKey } from "@hiero-ledger/sdk";
import { createClientHederaSigner } from "@x402/hedera";
import { ExactHederaScheme } from "@x402/hedera/exact/client";
import { x402Client } from "@x402/core/client";
import { wrapFetchWithPayment, decodePaymentResponseHeader } from "@x402/fetch";
import type { PaymentRequirements, SettleResponse } from "@x402/core/types";
import { renterCredentials } from "./env.js";

/** CAIP-2 network. Testnet only for now. */
export const HEDERA_TESTNET = "hedera:testnet";
/** The x402 asset id for native HBAR. */
export const HBAR_ASSET_ID = "0.0.0";
/** v2 header carrying the settlement receipt on a paid response. */
export const HEADER_PAYMENT_RESPONSE = "PAYMENT-RESPONSE";
/** v2 header carrying the 402 challenge. */
export const HEADER_PAYMENT_REQUIRED = "PAYMENT-REQUIRED";
/** 0.1 HBAR. Generous for one session chunk, small enough to catch a typo'd price. */
export const DEFAULT_MAX_TINYBARS_PER_PAYMENT = 10_000_000n;

export type Payer = {
  /** A fetch that transparently answers 402s by paying them. */
  fetch: typeof globalThis.fetch;
  accountId: string;
};

export type PayerOptions = {
  network?: string;
  /** Refuse any single payment above this many tinybars. */
  maxTinybarsPerPayment?: bigint;
};

export type HederaSigner = {
  readonly accountId: string;
  createPartiallySignedTransferTransaction(requirements: PaymentRequirements): Promise<string>;
};

/**
 * Wraps a signer into a paying fetch.
 *
 * By default `x402Client` only permits assets its `findDefaultAsset`
 * recognizes — on Hedera that is testnet USDC, capped at $1 — and native HBAR
 * is not one of them, so a stock client refuses a ClearGate challenge with no
 * clear error. This registers an explicit policy instead: HBAR only, on the
 * expected network, below the caller's own cap.
 */
export function payerFromSigner(signer: HederaSigner, options: PayerOptions = {}): Payer {
  const network = options.network ?? HEDERA_TESTNET;
  const maxTinybars = options.maxTinybarsPerPayment ?? DEFAULT_MAX_TINYBARS_PER_PAYMENT;

  const client = new x402Client()
    .register("hedera:*", new ExactHederaScheme(signer))
    .setSpendControls(false)
    .registerPolicy((_version, requirements) =>
      requirements.filter((option) => acceptable(option, network, maxTinybars)),
    );

  return { fetch: wrapFetchWithPayment(fetch, client), accountId: signer.accountId };
}

export function acceptable(
  option: PaymentRequirements,
  network: string,
  maxTinybars: bigint,
): boolean {
  if (option.network !== network) return false;
  if (option.asset !== HBAR_ASSET_ID) return false;
  try {
    return BigInt(option.amount) <= maxTinybars;
  } catch {
    return false;
  }
}

/** Builds a paying fetch from the caller's key in the environment. */
export function createPayer(options: PayerOptions = {}): Payer {
  const { accountId, privateKey, keyType } = renterCredentials();
  const network = options.network ?? HEDERA_TESTNET;

  const key =
    keyType === "ed25519"
      ? PrivateKey.fromStringED25519(privateKey)
      : PrivateKey.fromStringECDSA(privateKey);

  const signer = createClientHederaSigner(accountId, key, { network });

  return payerFromSigner(signer, options);
}

/** Reads the settlement receipt off a paid response (v2 header, v1 fallback). */
export function readSettlement(response: Response): SettleResponse | null {
  const header =
    response.headers.get(HEADER_PAYMENT_RESPONSE) ?? response.headers.get("X-PAYMENT-RESPONSE");
  return header === null ? null : decodePaymentResponseHeader(header);
}

/** Thrown when a request needed payment and the payment did not go through. */
export class PaymentError extends Error {
  constructor(
    message: string,
    readonly status: number,
    readonly body: string,
  ) {
    super(message);
    this.name = "PaymentError";
  }
}

/** Fetches a resource, paying if asked, and fails loudly if it comes back unpaid. */
export async function payFor(
  payer: Payer,
  url: string,
  init?: RequestInit,
): Promise<{ response: Response; settlement: SettleResponse }> {
  const response = await payer.fetch(url, init);

  if (!response.ok) {
    const body = await response.text();
    throw new PaymentError(
      `${init?.method ?? "GET"} ${url} failed: ${response.status} ${response.statusText}`,
      response.status,
      body,
    );
  }

  const settlement = readSettlement(response);
  if (settlement === null) {
    throw new PaymentError(
      `${url} returned ${response.status} without a settlement header — nothing was paid`,
      response.status,
      "",
    );
  }
  if (!settlement.success) {
    throw new PaymentError(
      `settlement failed: ${settlement.errorReason ?? "unknown"} ${settlement.errorMessage ?? ""}`.trim(),
      response.status,
      "",
    );
  }

  return { response, settlement };
}
