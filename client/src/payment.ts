/**
 * The parts of the payment path that do not care where the signature came from.
 *
 * Kept apart from node-side key loading: `env.ts` imports `dotenv` and
 * `node:fs` at module scope, and the website bundles this file.
 *
 * Everything here is isomorphic, and it is deliberately the *shared* copy
 * rather than a browser-side duplicate. The spend-control policy below is the
 * renter's only guard rail against a node quoting an absurd price, and two
 * copies of it would eventually disagree about what a safe payment is — in a
 * payment code path, silently.
 */
import { x402Client } from "@x402/core/client";
import { wrapFetchWithPayment, decodePaymentResponseHeader } from "@x402/fetch";
import { ExactHederaScheme } from "@x402/hedera/exact/client";
import type { PaymentRequirements, SettleResponse } from "@cleargate/types";
import { HEADER_PAYMENT_RESPONSE, HEDERA_TESTNET, HBAR_ASSET_ID } from "@cleargate/types";

/** 0.1 HBAR. Generous for a session chunk, small enough to catch a typo'd price. */
export const DEFAULT_MAX_TINYBARS_PER_PAYMENT = 10_000_000n;

export type Payer = {
  /** A fetch that transparently answers 402s by paying them. */
  fetch: typeof globalThis.fetch;
  /** The Hedera account paying. */
  accountId: string;
};

export type PayerOptions = {
  /** CAIP-2 network. Testnet only for now. */
  network?: string;
  /**
   * Refuse any single payment above this many tinybars. This is the renter's
   * own guard rail, independent of what a node asks for.
   */
  maxTinybarsPerPayment?: bigint;
};

/**
 * The signer shape `ExactHederaScheme` needs — `ClientHederaSigner` from
 * `@x402/hedera`, restated structurally so this module does not depend on
 * whether the signature comes from a local key or a browser wallet.
 */
export type HederaSigner = {
  readonly accountId: string;
  createPartiallySignedTransferTransaction(requirements: PaymentRequirements): Promise<string>;
};

/**
 * Wraps a signer into a paying fetch.
 *
 * The spend-control handling here is load-bearing and easy to get wrong. By
 * default `x402Client` only permits assets its `findDefaultAsset` recognizes —
 * on Hedera that is testnet USDC (0.0.429274) and nothing else — capped at $1.
 * Native HBAR ("0.0.0") is not a recognized default asset, so a stock client
 * refuses to pay a MACH402 challenge with no network call and no clear error.
 *
 * Rather than disabling spend controls outright, this registers an explicit
 * policy: HBAR only, on the expected network, below the renter's own cap. That
 * keeps a real guard rail while letting the payment through.
 */
export function payerFromSigner(signer: HederaSigner, options: PayerOptions = {}): Payer {
  const network = options.network ?? HEDERA_TESTNET;
  const maxTinybars = options.maxTinybarsPerPayment ?? DEFAULT_MAX_TINYBARS_PER_PAYMENT;

  const client = new x402Client()
    .register("hedera:*", new ExactHederaScheme(signer))
    // Turn off the USDC-only default, then re-impose our own limits below.
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
    // A non-integer amount is a malformed challenge, not a cheap one.
    return false;
  }
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

/**
 * Fetches a resource, paying if asked, and fails loudly if it comes back unpaid.
 */
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
