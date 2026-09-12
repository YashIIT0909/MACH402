/**
 * A `ClientHederaSigner` backed by a browser wallet instead of a private key.
 *
 * This is the one piece that lets a renter pay an x402 challenge from a web
 * page. CLAUDE.md invariant 2 puts all payment signing in `client/` using
 * `@x402/hedera`, and this does not relax that: it is the same
 * `ExactHederaScheme`, given a different implementation of the same two-member
 * signer interface.
 *
 *   type ClientHederaSigner = {
 *     readonly accountId: string;
 *     createPartiallySignedTransferTransaction(requirements): Promise<string>;
 *   };
 *
 * `createClientHederaSigner` is merely the SDK-and-private-key implementation of
 * that interface. Nothing about the scheme requires a key to be present, which
 * is exactly what makes a wallet viable without hand-rolling frozen transaction
 * bytes — the thing invariant 2 rules out.
 *
 * The transaction built here deliberately mirrors the upstream default
 * byte for byte. Divergence would be invisible until the facilitator rejected a
 * payload at the moment a renter was trying to pay, so the shape is copied
 * rather than reinvented: two HBAR transfers, a transaction id generated
 * against the *facilitator's* account, frozen, then signed.
 */
import {
  AccountId,
  Hbar,
  TokenId,
  TransactionId,
  TransferTransaction,
  type Transaction,
} from "@hiero-ledger/sdk";
import type { PaymentRequirements } from "@cleargate/types";
import { HBAR_ASSET_ID } from "@cleargate/types";

/**
 * The slice of a wallet this module needs.
 *
 * Structural on purpose, so `client/` takes no dependency on any particular
 * wallet library. `DAppSigner` from `@hashgraph/hedera-wallet-connect` already
 * satisfies it, because it implements the Hiero SDK's own `Signer` interface —
 * but so would a WalletConnect session driven by hand, or a test double.
 */
export type WalletSigner = {
  /** The account the wallet will pay from, as `0.0.x`. */
  readonly accountId: string;
  /**
   * Adds the wallet holder's signature to an already-frozen transaction and
   * returns it. It must NOT submit: the facilitator co-signs as fee payer and
   * submits, and a transaction already on consensus cannot be settled again.
   */
  signTransaction(transaction: Transaction): Promise<Transaction>;
};

export type WalletHederaSignerConfig = {
  /**
   * Supplies the frozen transaction's node account ids.
   *
   * Needed because freezing requires knowing which consensus nodes the
   * transaction may be submitted to, and the facilitator's submission is bound
   * to that list. Injected rather than built here so a page can pass the
   * network client it already has and this module stays free of network setup.
   */
  freeze: (transaction: TransferTransaction) => TransferTransaction;
};

/**
 * Builds a signer that asks a wallet to sign, instead of holding a key.
 *
 * @param wallet - The connected wallet.
 * @param config - How to freeze the transaction before signing.
 */
export function createWalletHederaSigner(
  wallet: WalletSigner,
  config: WalletHederaSignerConfig,
): { readonly accountId: string; createPartiallySignedTransferTransaction(r: PaymentRequirements): Promise<string> } {
  const payer = AccountId.fromString(wallet.accountId);

  return {
    accountId: payer.toString(),

    async createPartiallySignedTransferTransaction(requirements: PaymentRequirements): Promise<string> {
      const feePayer = requirements.extra?.feePayer;
      if (typeof feePayer !== "string") {
        // Invariant 5: feePayer comes from the facilitator's /supported and is
        // echoed in the challenge. A challenge without it is unpayable, and
        // guessing one would produce a payload the facilitator refuses.
        throw new Error("the node's 402 challenge carried no extra.feePayer, so it cannot be paid");
      }

      const amount = BigInt(requirements.amount);
      if (amount <= 0n) {
        throw new Error("payment amount must be greater than zero");
      }

      const payTo = AccountId.fromString(requirements.payTo);
      const transaction = new TransferTransaction();

      if (requirements.asset === HBAR_ASSET_ID) {
        transaction.addHbarTransfer(payer, Hbar.fromTinybars((-amount).toString()));
        transaction.addHbarTransfer(payTo, Hbar.fromTinybars(amount.toString()));
      } else {
        const token = TokenId.fromString(requirements.asset);
        transaction.addTokenTransfer(token, payer, -amount);
        transaction.addTokenTransfer(token, payTo, amount);
      }

      // The transaction id belongs to the FACILITATOR, not the renter. That is
      // what makes the facilitator the fee payer, and it is the detail most
      // likely to trip a wallet up: some wallets expect to be the payer of
      // anything they sign and will refuse, or silently re-freeze with
      // themselves in that slot. A re-frozen transaction is a different
      // transaction and the facilitator will reject it.
      transaction.setTransactionId(TransactionId.generate(AccountId.fromString(feePayer)));

      const frozen = config.freeze(transaction);
      const signed = await wallet.signTransaction(frozen);

      assertStillPayableBy(signed, feePayer);

      return base64(signed.toBytes());
    },
  };
}

/**
 * Catches a wallet that re-froze the transaction under its own account.
 *
 * Worth an explicit check rather than letting it through: the failure otherwise
 * surfaces as an opaque facilitator rejection after the renter has already
 * approved a prompt, and the cause — a wallet quietly rewriting the fee payer —
 * is nearly impossible to read from that error.
 */
function assertStillPayableBy(transaction: Transaction, feePayer: string): void {
  const id = transaction.transactionId;
  const actual = id?.accountId?.toString();
  if (actual !== undefined && actual !== feePayer) {
    throw new Error(
      `the wallet re-signed this payment under its own account (${actual}) instead of leaving the ` +
        `facilitator (${feePayer}) as fee payer, so the facilitator would reject it`,
    );
  }
}

/**
 * Base64 without Buffer or btoa.
 *
 * The upstream signer uses `Buffer`, which is Node-only; `btoa` is DOM-only and
 * this package compiles without DOM types. Neither is worth a polyfill for
 * thirty lines of table lookup, and keeping it dependency-free means the same
 * code path is exercised by a plain node test as by the browser.
 */
const BASE64_ALPHABET = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/";

export function base64(bytes: Uint8Array): string {
  let out = "";
  for (let i = 0; i < bytes.length; i += 3) {
    const a = bytes[i] as number;
    const b = bytes[i + 1];
    const c = bytes[i + 2];

    out += BASE64_ALPHABET[a >> 2];
    out += BASE64_ALPHABET[((a & 0x03) << 4) | ((b ?? 0) >> 4)];
    out += b === undefined ? "=" : BASE64_ALPHABET[((b & 0x0f) << 2) | ((c ?? 0) >> 6)];
    out += c === undefined ? "=" : BASE64_ALPHABET[c & 0x3f];
  }
  return out;
}
