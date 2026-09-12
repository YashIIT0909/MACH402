/**
 * The node-local operator key: generation, storage and loading.
 *
 * This is the provider-side mirror of what `internal/sshca` does for the SSH CA
 * key — generate if absent, load if present, `0600`, never logged, never
 * copied. The two differences are that this key is ECDSA (it needs an EVM
 * address, for ERC-8004 and for the escrow contract) and that it holds a small
 * amount of HBAR to pay its own fees.
 *
 * It is emphatically NOT `pay_to`. A provider's earnings accumulate in `pay_to`,
 * which signs nothing and is never written to disk by anything in this repo.
 * This key pays gas, so a compromise costs the fee float and nothing more.
 */
import { chmodSync, existsSync, mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { dirname } from "node:path";
import { AccountId, PrivateKey } from "@hiero-ledger/sdk";

/** What lives in the key file. Deliberately tiny and human-readable. */
export interface OperatorKeyFile {
    /** DER-encoded ECDSA private key, hex. */
    private_key: string;
    /** The 0x-prefixed EVM address this key maps to. */
    evm_address: string;
    /**
     * The Hedera account id, once it is known. Empty until the account has been
     * funded — an ECDSA key has an EVM address from birth, but no `0.0.x`
     * account exists until someone sends HBAR to it.
     */
    account_id: string;
    created_at: string;
}

export interface Operator {
    key: PrivateKey;
    evmAddress: string;
    accountId: string;
    path: string;
}

/**
 * Loads the operator key, generating it if the file does not exist.
 *
 * Idempotent on purpose, and for the same reason `sshca.Ensure` is: setup and
 * serve both call it, and regenerating would strand the funded account and the
 * registered identity that point at the old key.
 */
export function ensureOperator(path: string): Operator {
    if (existsSync(path)) {
        return loadOperator(path);
    }

    const key = PrivateKey.generateECDSA();
    const file: OperatorKeyFile = {
        private_key: key.toStringDer(),
        evm_address: `0x${key.publicKey.toEvmAddress()}`,
        account_id: "",
        created_at: new Date().toISOString(),
    };

    mkdirSync(dirname(path), { recursive: true, mode: 0o700 });
    // Written and then chmodded rather than trusting the umask, matching how
    // the SSH CA key is handled.
    writeFileSync(path, `${JSON.stringify(file, null, 2)}\n`, { mode: 0o600 });
    chmodSync(path, 0o600);

    return { key, evmAddress: file.evm_address, accountId: "", path };
}

/** Loads an existing operator key. Throws if it is not there. */
export function loadOperator(path: string): Operator {
    if (!existsSync(path)) {
        throw new Error(
            `no operator key at ${path} — run \`cleargate-node setup --enable-hcs\` first`,
        );
    }

    const file = JSON.parse(readFileSync(path, "utf8")) as OperatorKeyFile;
    if (!file.private_key) {
        throw new Error(`operator key file ${path} has no private_key`);
    }

    const key = PrivateKey.fromStringECDSA(file.private_key);
    return {
        key,
        evmAddress: file.evm_address || `0x${key.publicKey.toEvmAddress()}`,
        accountId: file.account_id ?? "",
        path,
    };
}

/**
 * Records the resolved `0.0.x` account id beside the key.
 *
 * Cached rather than re-resolved on every invocation because the sidecar is a
 * short-lived process the daemon spawns per message: a mirror-node round trip
 * to answer a question whose answer never changes would double the cost of
 * publishing a receipt.
 */
export function rememberAccountId(path: string, accountId: string): void {
    const file = JSON.parse(readFileSync(path, "utf8")) as OperatorKeyFile;
    if (file.account_id === accountId) return;
    file.account_id = accountId;
    writeFileSync(path, `${JSON.stringify(file, null, 2)}\n`, { mode: 0o600 });
}

/** `0.0.x` for an account id string, validated. */
export function parseAccountId(accountId: string): AccountId {
    try {
        return AccountId.fromString(accountId);
    } catch {
        throw new Error(`not a Hedera account id: ${accountId}`);
    }
}
