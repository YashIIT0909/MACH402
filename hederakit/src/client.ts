/**
 * The Hedera client the sidecar signs with.
 *
 * Built once per process invocation and closed when the command finishes. The
 * sidecar is short-lived by design — the Go daemon spawns it per operation —
 * so there is no connection pooling to manage and no long-running process
 * holding a key in memory between jobs.
 */
import { Client } from "@hiero-ledger/sdk";
import { type Operator, parseAccountId, rememberAccountId } from "./key.js";
import { lookupAccount } from "./mirror.js";

export interface ResolvedOperator extends Operator {
    /** Always present here, unlike on a raw Operator — resolving is the point. */
    accountId: string;
}

/**
 * Finds the operator's Hedera account id, using the value cached beside the key
 * when there is one and the mirror node otherwise.
 *
 * The error when there is no account is the one a provider is most likely to
 * hit, so it says what to do rather than what went wrong.
 */
export async function resolveOperator(
    operator: Operator,
    mirrorBaseUrl?: string,
): Promise<ResolvedOperator> {
    if (operator.accountId) {
        return { ...operator, accountId: operator.accountId };
    }

    const account = await lookupAccount(operator.evmAddress, mirrorBaseUrl);
    if (!account) {
        throw new Error(
            `the operator key at ${operator.path} has no Hedera account yet.\n` +
                `Send a few HBAR to ${operator.evmAddress} to create it — ` +
                `https://portal.hedera.com on testnet — then try again.`,
        );
    }

    rememberAccountId(operator.path, account.accountId);
    return { ...operator, accountId: account.accountId };
}

/** A network-connected client, signing as the operator. */
export function hederaClient(operator: ResolvedOperator, network: string): Client {
    const client = network.endsWith(":mainnet") ? Client.forMainnet() : Client.forTestnet();
    client.setOperator(parseAccountId(operator.accountId), operator.key);
    return client;
}
