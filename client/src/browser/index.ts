/**
 * The browser half of the renter side: paying a node's x402 challenge from a
 * web page, with a wallet holding the key instead of the environment.
 *
 * Kept in `client/` rather than in `web/` on purpose. CLAUDE.md invariant 2
 * puts all payment signing in this package, and a second signing path living
 * next to the UI is exactly how the two drift apart. `web/` imports from here
 * and contains no payment logic of its own.
 *
 * Nothing in this module or its imports touches `node:` builtins, `dotenv` or
 * `process.env`, which is what makes it bundleable. Import from
 * `@cleargate/client/browser`, never from a node-side module.
 */
import { Client } from "@hiero-ledger/sdk";
import { HEDERA_TESTNET } from "@cleargate/types";
import { payerFromSigner, type Payer, type PayerOptions } from "../payment.js";
import { createWalletHederaSigner, type WalletSigner } from "./wallet-signer.js";

export { createWalletHederaSigner, base64, type WalletSigner } from "./wallet-signer.js";
export {
  generateSSHKeypair,
  encodeOpenSSHPublicKey,
  type BrowserSSHKeypair,
  type SubtleCryptoLike,
  type CryptoKeyLike,
} from "./ssh-key.js";
export {
  DEFAULT_MAX_TINYBARS_PER_PAYMENT,
  PaymentError,
  payFor,
  payerFromSigner,
  readSettlement,
  type HederaSigner,
  type Payer,
  type PayerOptions,
} from "../payment.js";

/**
 * Builds a paying fetch backed by a connected browser wallet.
 *
 * The returned `fetch` answers a node's 402 by asking the wallet to sign, so a
 * page calls `POST /v1/sessions` once and the payment happens inside that call.
 *
 * @param wallet - The connected wallet: an account id and a sign-without-submit.
 * @param options - Network and the renter's own per-payment cap.
 */
export function createWalletPayer(wallet: WalletSigner, options: PayerOptions = {}): Payer {
  const network = options.network ?? HEDERA_TESTNET;

  const signer = createWalletHederaSigner(wallet, {
    // Freezing needs the network's node account ids, and the facilitator's
    // submission is bound to whichever ones end up in the transaction. Building
    // the client per payment rather than once keeps this module free of
    // lifecycle: a payment is a user gesture, not a hot path, and an
    // unclosed client in a page that the renter navigates away from is a leak.
    freeze: (transaction) => {
      const client = clientFor(network);
      try {
        return transaction.freezeWith(client);
      } finally {
        client.close();
      }
    },
  });

  return payerFromSigner(signer, options);
}

/**
 * A Hedera client for freezing only — no transaction is ever submitted through
 * it, because the facilitator is what submits.
 *
 * `@hiero-ledger/sdk` ships a browser build (its package `browser` field
 * remaps the entry point), so this resolves to the gRPC-web client in a bundle
 * and the plain one under node. Freezing performs no network I/O either way.
 */
function clientFor(network: string): Client {
  if (network === HEDERA_TESTNET) {
    return Client.forTestnet();
  }
  // Mainnet is out of scope (CLAUDE.md, "Deliberately out of scope"), so this
  // is reachable only by a caller passing a network this build does not sell on.
  throw new Error(`ClearGate does not pay on ${network}; this build is testnet only`);
}
