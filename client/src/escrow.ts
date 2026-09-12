/**
 * Renter-side calls to the SessionEscrow contract.
 *
 * This is the second place in ClearGate that signs a Hedera transaction, beside
 * `pay.ts`, and it holds to the same rule: all payment signing lives in
 * `client/`, in TypeScript, on the renter's own machine (CLAUDE.md invariants 1
 * and 2). A `ContractExecuteTransaction` is signed exactly where and how a
 * `TransferTransaction` already is.
 *
 * The difference from `pay.ts` is that there is no facilitator here. The x402
 * flow needs one because the renter signs a transfer that somebody else has to
 * co-sign as fee payer and submit. A contract call the renter signs and submits
 * themselves is already final on Hedera consensus the moment it succeeds, so
 * there is nothing left for a third party to do — the provider's node simply
 * reads the result from the public mirror node.
 *
 * Units: every amount here is TINYBARS, because that is what `msg.value` is
 * inside the Hedera EVM. Nothing in this file converts to weibars, and nothing
 * should — see the unit note at the top of SessionEscrow.sol.
 */
import Long from "long";
import {
    AccountId,
    Client,
    ContractCallQuery,
    ContractExecuteTransaction,
    ContractFunctionParameters,
    ContractId,
    Hbar,
    HbarUnit,
    PrivateKey,
    type TransactionResponse,
} from "@hiero-ledger/sdk";
import { renterCredentials } from "./env.js";

/** Gas for a write. Unused gas is not charged on Hedera. */
const WRITE_GAS = 400_000;
/** Gas for a read. Cheaper, and paid in HBAR like everything else. */
const QUERY_GAS = 120_000;

/** A session as the contract holds it. All amounts tinybars. */
export interface OnChainSession {
    renter: string;
    provider: string;
    pricePerSecond: bigint;
    startTime: bigint;
    duration: bigint;
    deposited: bigint;
    settled: boolean;
}

/** What settling right now would pay out. */
export interface SettlementQuote {
    elapsed: bigint;
    providerAmount: bigint;
    refundAmount: bigint;
}

/**
 * A signing connection to Hedera, built from the renter's own credentials.
 *
 * Deliberately separate from the `x402Client` in `pay.ts`: that one exists to
 * negotiate 402 challenges and knows about payment policies, and none of that
 * applies to a contract call the renter makes directly.
 */
export class EscrowClient {
    private readonly client: Client;
    private readonly contract: ContractId;

    constructor(contractIdOrAddress: string, network: string) {
        const { accountId, privateKey, keyType } = renterCredentials();
        const key =
            keyType === "ed25519"
                ? PrivateKey.fromStringED25519(privateKey)
                : PrivateKey.fromStringECDSA(privateKey);

        this.client = network.endsWith(":mainnet") ? Client.forMainnet() : Client.forTestnet();
        this.client.setOperator(AccountId.fromString(accountId), key);
        // Config may hold either form depending on how the contract was
        // deployed, so accept both rather than making the provider normalise it.
        this.contract = contractIdOrAddress.startsWith("0x")
            ? ContractId.fromEvmAddress(0, 0, contractIdOrAddress)
            : ContractId.fromString(contractIdOrAddress);
    }

    close(): void {
        this.client.close();
    }

    /**
     * Deposits for a session.
     *
     * `sessionId`, `providerAddress` and `pricePerSecond` all come from the
     * node's 402 quote and are echoed here unchanged. The node then checks the
     * on-chain record against that same quote before it provisions anything, so
     * altering any of them locally only produces a session the node refuses —
     * which is the point: neither side has to trust the other's copy of the
     * terms.
     */
    async openSession(args: {
        sessionId: string;
        providerAddress: string;
        pricePerSecond: bigint;
        seconds: number;
    }): Promise<string> {
        const deposit = args.pricePerSecond * BigInt(args.seconds);

        const response = await new ContractExecuteTransaction()
            .setContractId(this.contract)
            .setGas(WRITE_GAS)
            .setPayableAmount(Hbar.from(deposit.toString(), HbarUnit.Tinybar))
            .setFunction(
                "openSession",
                new ContractFunctionParameters()
                    .addBytes32(hexToBytes(args.sessionId))
                    .addAddress(args.providerAddress)
                    .addUint256(Long.fromString(args.pricePerSecond.toString()))
                    .addUint256(args.seconds),
            )
            .execute(this.client);

        return this.confirm(response, "openSession");
    }

    /**
     * Buys more time on a live session, at the price fixed when it opened.
     *
     * The contract refuses a top-up at any other rate, so a session's price
     * cannot be changed out from under either party mid-run.
     */
    async topUp(args: {
        sessionId: string;
        pricePerSecond: bigint;
        seconds: number;
    }): Promise<string> {
        const deposit = args.pricePerSecond * BigInt(args.seconds);

        const response = await new ContractExecuteTransaction()
            .setContractId(this.contract)
            .setGas(WRITE_GAS)
            .setPayableAmount(Hbar.from(deposit.toString(), HbarUnit.Tinybar))
            .setFunction(
                "topUp",
                new ContractFunctionParameters()
                    .addBytes32(hexToBytes(args.sessionId))
                    .addUint256(args.seconds),
            )
            .execute(this.client);

        return this.confirm(response, "topUp");
    }

    /**
     * Closes a session, paying the provider for elapsed time and refunding the
     * rest to whoever opened it.
     *
     * Permissionless at the contract, so a renter can always call this
     * themselves — they never have to wait for the provider, or trust them to
     * do it. Calling early only ever refunds MORE, so there is no version of
     * this that is worse for the caller than waiting.
     *
     * Safe to call on an already-settled session: the contract's one-shot guard
     * reverts, which this reports as success rather than an error, because from
     * the renter's point of view the outcome they wanted has happened.
     */
    async settle(sessionId: string): Promise<{ transaction: string; alreadySettled: boolean }> {
        try {
            const response = await new ContractExecuteTransaction()
                .setContractId(this.contract)
                .setGas(WRITE_GAS)
                .setFunction(
                    "settle",
                    new ContractFunctionParameters().addBytes32(hexToBytes(sessionId)),
                )
                .execute(this.client);

            return { transaction: await this.confirm(response, "settle"), alreadySettled: false };
        } catch (error) {
            if (isAlreadySettled(error)) {
                return { transaction: "", alreadySettled: true };
            }
            throw error;
        }
    }

    /** Reads a session's current state. */
    async readSession(sessionId: string): Promise<OnChainSession> {
        const result = await new ContractCallQuery()
            .setContractId(this.contract)
            .setGas(QUERY_GAS)
            .setFunction(
                "getSession",
                new ContractFunctionParameters().addBytes32(hexToBytes(sessionId)),
            )
            .execute(this.client);

        return {
            renter: result.getAddress(0),
            provider: result.getAddress(1),
            pricePerSecond: BigInt(result.getUint256(2).toString()),
            startTime: BigInt(result.getUint256(3).toString()),
            duration: BigInt(result.getUint256(4).toString()),
            deposited: BigInt(result.getUint256(5).toString()),
            settled: result.getBool(6),
        };
    }

    /**
     * What settling right now would pay each side, without settling.
     *
     * Shown to the renter before they stop, so the refund is something they can
     * check rather than something they have to take on faith.
     */
    async quoteSettlement(sessionId: string): Promise<SettlementQuote> {
        const result = await new ContractCallQuery()
            .setContractId(this.contract)
            .setGas(QUERY_GAS)
            .setFunction(
                "quoteSettlement",
                new ContractFunctionParameters().addBytes32(hexToBytes(sessionId)),
            )
            .execute(this.client);

        return {
            elapsed: BigInt(result.getUint256(0).toString()),
            providerAmount: BigInt(result.getUint256(1).toString()),
            refundAmount: BigInt(result.getUint256(2).toString()),
        };
    }

    /**
     * Waits for consensus and returns the transaction id.
     *
     * The receipt is what turns "submitted" into "final"; returning before it
     * would hand the node a transaction id the mirror may never show, because
     * the call could still fail.
     */
    private async confirm(response: TransactionResponse, what: string): Promise<string> {
        const receipt = await response.getReceipt(this.client);
        if (receipt.status.toString() !== "SUCCESS") {
            throw new Error(`${what} failed on-chain: ${receipt.status.toString()}`);
        }
        return response.transactionId.toString();
    }
}

/** Parses a 0x-prefixed 32-byte session id. */
function hexToBytes(value: string): Uint8Array {
    const hex = value.replace(/^0x/i, "");
    if (!/^[0-9a-fA-F]{64}$/.test(hex)) {
        throw new Error(`session id must be 32 hex bytes, got "${value}"`);
    }
    return Uint8Array.from(hex.match(/.{2}/g)!.map((byte) => parseInt(byte, 16)));
}

/**
 * Recognises the contract's one-shot guard firing.
 *
 * Hedera reports a custom-error revert as CONTRACT_REVERT_EXECUTED without
 * decoding which error it was, so this matches on that plus the fact that the
 * only way `settle` reverts for a session that exists is `AlreadySettled`. A
 * session that never existed reverts too, but a renter calling settle on a
 * session id they were just given is not in that position.
 */
function isAlreadySettled(error: unknown): boolean {
    const message = error instanceof Error ? error.message : String(error);
    return message.includes("CONTRACT_REVERT_EXECUTED");
}
