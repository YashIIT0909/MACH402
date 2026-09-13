/**
 * A plain HBAR transfer, signed by the node's operator key.
 *
 * This is what pays a metered session's refund: when a session ends, the credit
 * the renter paid for and did not burn is theirs, and returning it is an
 * ordinary CryptoTransfer rather than anything clever. There is no facilitator
 * in this direction — a facilitator co-signs as fee payer for a renter who has
 * no account on the network's terms, and the provider paying a refund out of
 * their own funded account needs nobody's help.
 *
 * It is deliberately the narrowest possible capability: one recipient, one
 * amount, no memo the caller controls beyond a session id, no token transfers,
 * no contract calls. The operator key can already do all of those through the
 * SDK; what matters is that this sidecar command cannot be talked into them.
 */
import {
    Hbar,
    HbarUnit,
    TransferTransaction,
    type Client,
} from "@hiero-ledger/sdk";
import { parseAccountId } from "./key.js";

export interface RefundResult {
    transaction: string;
    amountTinybars: string;
}

/**
 * Sends `tinybars` from the operator's account to `to`.
 *
 * Amounts stay strings the whole way, like everywhere else in MACH402: the
 * SDK's Hbar.from* helpers take a Long, and going through a JavaScript number
 * would silently round a tinybar figure above 2^53.
 */
export async function sendHbar(
    client: Client,
    from: string,
    to: string,
    tinybars: string,
    memo: string,
): Promise<RefundResult> {
    const amount = BigInt(tinybars);
    if (amount <= 0n) {
        throw new Error(`refusing to transfer ${tinybars} tinybars; the amount must be positive`);
    }

    const value = Hbar.from(amount.toString(), HbarUnit.Tinybar);
    const response = await new TransferTransaction()
        .addHbarTransfer(parseAccountId(from), value.negated())
        .addHbarTransfer(parseAccountId(to), value)
        // The memo puts the session id on the transfer itself, so the refund on
        // a provider's account statement can be matched to the burn checkpoints
        // on their audit topic without holding both sides of the story.
        .setTransactionMemo(memo.slice(0, 100))
        .execute(client);

    // Waited on rather than fired and forgotten: the node records this refund
    // as paid, and recording a transfer that the network then rejected would
    // put a wrong number on a public audit trail.
    const receipt = await response.getReceipt(client);
    if (receipt.status.toString() !== "SUCCESS") {
        throw new Error(`transfer failed with status ${receipt.status.toString()}`);
    }

    return {
        transaction: response.transactionId.toString(),
        amountTinybars: amount.toString(),
    };
}
