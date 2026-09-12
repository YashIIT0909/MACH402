/**
 * Mirror-node lookups the sidecar needs before it can sign anything.
 *
 * An ECDSA key has an EVM address from the moment it is generated, but no
 * Hedera account exists until someone funds it. The mirror node is how we find
 * out whether that has happened and what the resulting `0.0.x` is — public data,
 * no key required, a plain HTTP GET.
 */

const DEFAULT_MIRROR_URL = "https://testnet.mirrornode.hedera.com";

export interface MirrorAccount {
    accountId: string;
    evmAddress: string;
    balanceTinybars: string;
    receiverSigRequired: boolean;
}

interface MirrorAccountResponse {
    account?: string;
    evm_address?: string;
    receiver_sig_required?: boolean;
    balance?: { balance?: number };
}

export function mirrorUrl(override?: string): string {
    return (override ?? process.env["HEDERA_MIRROR_URL"] ?? DEFAULT_MIRROR_URL).replace(/\/+$/, "");
}

/**
 * Resolves an account by id or by EVM address.
 *
 * Returns null for "no such account" rather than throwing, because the most
 * common reason to ask is a freshly generated key that nobody has funded yet —
 * which is a state to report to the provider, not an error.
 */
export async function lookupAccount(
    idOrAddress: string,
    baseUrl?: string,
): Promise<MirrorAccount | null> {
    const url = `${mirrorUrl(baseUrl)}/api/v1/accounts/${encodeURIComponent(idOrAddress)}`;
    const response = await fetch(url, {
        headers: { accept: "application/json" },
        signal: AbortSignal.timeout(15_000),
    });

    if (response.status === 404) return null;
    if (!response.ok) {
        throw new Error(`mirror node ${response.status} for ${idOrAddress}`);
    }

    const body = (await response.json()) as MirrorAccountResponse;
    if (!body.account) return null;

    return {
        accountId: body.account,
        evmAddress: body.evm_address ?? "",
        balanceTinybars: String(body.balance?.balance ?? 0),
        receiverSigRequired: body.receiver_sig_required === true,
    };
}
