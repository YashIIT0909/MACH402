/**
 * createPayer — the 402 fetch wrapper the CLI uses, backed by a local key.
 *
 * All Hedera signing in ClearGate lives in `client/` (CLAUDE.md invariant 1 and
 * 2): the provider agent never holds a key and never links a Hedera SDK. This
 * module is the local-key half of that — the browser-wallet half is
 * `browser/wallet-signer.ts`, and both hand the same `ClientHederaSigner`
 * interface to the same `ExactHederaScheme`.
 *
 * Everything that does not depend on where the signature comes from lives in
 * `payment.ts` and is re-exported here, so existing importers are unaffected.
 */
import { PrivateKey } from "@hiero-ledger/sdk";
import { createClientHederaSigner } from "@x402/hedera";
import { HEDERA_TESTNET } from "@cleargate/types";
import { renterCredentials } from "./env.js";
import { payerFromSigner, type Payer, type PayerOptions } from "./payment.js";

export {
  DEFAULT_MAX_TINYBARS_PER_PAYMENT,
  PaymentError,
  payFor,
  payerFromSigner,
  readSettlement,
  type HederaSigner,
  type Payer,
  type PayerOptions,
} from "./payment.js";

/**
 * Builds a paying fetch from the renter's key in the environment.
 *
 * The key comes from `HEDERA_PRIVATE_KEY` and is never written to disk or
 * logged — see CLAUDE.md, "Secrets".
 */
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
