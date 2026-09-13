/**
 * Step 3 of the payment spike: pay for the resource and prove it settled.
 *
 * All Hedera signing in ClearGate lives on this side. The agent never holds a
 * key (CLAUDE.md invariant 1), so this file is the reference for `client/src/payment.ts`.
 */
import { PrivateKey } from "@hiero-ledger/sdk";
import { x402Client } from "@x402/core/client";
import { ExactHederaScheme } from "@x402/hedera/exact/client";
import { createClientHederaSigner } from "@x402/hedera";
import { wrapFetchWithPayment, decodePaymentResponseHeader } from "@x402/fetch";
import type { SettleResponse } from "@cleargate/types";
import { HEADER_PAYMENT_RESPONSE, HEDERA_TESTNET, hashscanUrl } from "@cleargate/types";
import { SMOKE_PORT, renterCredentials } from "./env.js";

/**
 * Builds a fetch that transparently pays 402s.
 *
 * The `setSpendControls(false)` call is load-bearing. By default x402Client only
 * permits assets `findDefaultAsset` recognizes — on Hedera that is testnet USDC
 * (0.0.429274) and nothing else — capped at $1. Native HBAR ("0.0.0") is not a
 * recognized default asset, so a stock client refuses to pay our challenge with
 * no network request and no obvious error. Disabling the controls here is safe:
 * this is testnet, the price is fixed at 0.001 HBAR, and the website's payer
 * applies its own explicit per-payment cap instead.
 */
export function buildPayingFetch(): typeof globalThis.fetch {
  const { accountId, privateKey, keyType } = renterCredentials();

  const key =
    keyType === "ed25519"
      ? PrivateKey.fromStringED25519(privateKey)
      : PrivateKey.fromStringECDSA(privateKey);

  const signer = createClientHederaSigner(accountId, key, { network: HEDERA_TESTNET });

  const client = new x402Client()
    .register("hedera:*", new ExactHederaScheme(signer))
    .setSpendControls(false);

  return wrapFetchWithPayment(fetch, client);
}

/** Pulls the settlement receipt out of the response headers (v2, with a v1 fallback). */
export function readSettlement(response: Response): SettleResponse | null {
  const header =
    response.headers.get(HEADER_PAYMENT_RESPONSE) ??
    response.headers.get("X-PAYMENT-RESPONSE");
  return header === null ? null : decodePaymentResponseHeader(header);
}

/**
 * Pays for a resource and returns its settlement.
 *
 * `body`, when given, makes this a POST; the reference server's `GET /paid-hello`
 * needs none.
 */
export async function paySmokeResource(url: string, body?: unknown): Promise<SettleResponse> {
  const payingFetch = buildPayingFetch();

  const init: RequestInit | undefined =
    body === undefined
      ? undefined
      : {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify(body),
        };

  console.log(`requesting     ${init?.method ?? "GET"} ${url}`);
  const response = await payingFetch(url, init);

  if (!response.ok) {
    const body = await response.text();
    throw new Error(`request failed: ${response.status} ${response.statusText}\n${body}`);
  }

  const settlement = readSettlement(response);
  if (settlement === null) {
    throw new Error(
      `resource returned 200 but no ${HEADER_PAYMENT_RESPONSE} header — ` +
        `the server did not settle, so nothing moved on chain`,
    );
  }
  if (!settlement.success) {
    throw new Error(
      `settlement failed: ${settlement.errorReason ?? "unknown"} ${settlement.errorMessage ?? ""}`.trim(),
    );
  }

  const responseBody: unknown = await response.json();
  console.log(`response body  ${JSON.stringify(responseBody)}`);
  console.log(`payer          ${settlement.payer ?? "unknown"}`);
  console.log(`transaction    ${settlement.transaction}`);
  console.log(`hashscan       ${hashscanUrl(settlement.transaction, settlement.network)}`);

  return settlement;
}

async function main(): Promise<void> {
  const url = process.argv[2] ?? `http://localhost:${SMOKE_PORT}/paid-hello`;
  const body = process.argv[3];
  await paySmokeResource(url, body === undefined ? undefined : (JSON.parse(body) as unknown));
}

if (import.meta.url === `file://${process.argv[1]}`) {
  main().catch((error: unknown) => {
    console.error(`\npayment failed: ${error instanceof Error ? error.message : String(error)}`);
    console.error(
      "\ndiagnosis order:\n" +
        "  1. spend controls rejecting HBAR (setSpendControls)\n" +
        "  2. wrong key type — flip HEDERA_KEY_TYPE between ecdsa and ed25519\n" +
        "  3. account unfunded — https://portal.hedera.com\n" +
        "  4. facilitator feePayer mismatch — run `pnpm --filter @cleargate/smoke supported`",
    );
    process.exit(1);
  });
}
