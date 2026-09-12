/**
 * Hedera Consensus Service: create the provider's audit topic, and publish to it.
 *
 * Why the provider holds this key rather than the renter: a renter-published
 * audit trail does not work economically. By the time there is a receipt to
 * publish, the renter already has their compute and has no reason to spend HBAR
 * writing down the provider's earnings. So the write key has to sit with the
 * party who benefits from the record existing.
 */
import {
    TopicCreateTransaction,
    TopicMessageSubmitTransaction,
    type Client,
} from "@hiero-ledger/sdk";

/**
 * Creates the topic a provider's settlements are published to.
 *
 * The submit key is set to the operator's own key, so only this provider can
 * write to their own trail — an open topic would let anyone forge entries into
 * it, which would make the trail worth nothing. The admin key is deliberately
 * left unset: nobody, including the provider, can delete or reconfigure the
 * topic afterwards, which is what makes past entries credible.
 */
export async function createTopic(client: Client, memo: string): Promise<string> {
    const receipt = await (
        await new TopicCreateTransaction()
            .setTopicMemo(memo)
            .setSubmitKey(client.operatorPublicKey!)
            .execute(client)
    ).getReceipt(client);

    const topicId = receipt.topicId;
    if (!topicId) throw new Error("topic creation returned no topic id");
    return topicId.toString();
}

/**
 * Publishes one audit message.
 *
 * The message is passed on stdin rather than argv so a receipt — which names a
 * payer, a payee and an amount — never lands in the host's process table where
 * any local user could read it with `ps`.
 */
export async function publish(client: Client, topicId: string, message: string): Promise<string> {
    const receipt = await (
        await new TopicMessageSubmitTransaction()
            .setTopicId(topicId)
            .setMessage(message)
            .execute(client)
    ).getReceipt(client);

    return receipt.topicSequenceNumber?.toString() ?? "0";
}
