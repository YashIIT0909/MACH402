/**
 * Contract calls the provider's node makes: ERC-8004 registration, and the
 * opt-in escrow self-settle.
 *
 * Two libraries, each doing the half it is good at. `ethers` encodes and
 * decodes the ABI — hand-rolling selectors and word packing for calls that move
 * real money is avoidable risk, and it would need a Keccak implementation of its
 * own. The Hedera SDK submits, because these go to Hedera consensus rather than
 * through a JSON-RPC relay.
 */
import {
    ContractCallQuery,
    ContractExecuteTransaction,
    ContractId,
    type Client,
} from "@hiero-ledger/sdk";
import { Interface } from "ethers";

/** Gas for a registration write. Unused gas is not charged on Hedera. */
const REGISTER_GAS = 400_000;
/** Gas for settle: two value transfers plus storage writes. */
const SETTLE_GAS = 400_000;
/** Gas for a read. Queries are paid in HBAR but cost far less. */
const QUERY_GAS = 120_000;

const IDENTITY_ABI = new Interface([
    "function agentIdOf(address agentAddress) view returns (uint256)",
    "function newAgent(string agentDomain, address agentAddress) returns (uint256)",
    "function updateAgent(uint256 agentId, string newDomain, address newAddress) returns (bool)",
]);

const ESCROW_ABI = new Interface(["function settle(bytes32 sessionId)"]);

function encode(abi: Interface, fn: string, args: unknown[]): Buffer {
    return Buffer.from(abi.encodeFunctionData(fn, args).slice(2), "hex");
}

/**
 * Hedera contract ids and EVM addresses are the same thing wearing different
 * clothes; accept either so config can hold whichever form a deploy produced.
 */
function contractId(idOrAddress: string): ContractId {
    return idOrAddress.startsWith("0x")
        ? ContractId.fromEvmAddress(0, 0, idOrAddress)
        : ContractId.fromString(idOrAddress);
}

/**
 * Asks the identity registry whether this address already has an agent id.
 *
 * `agentIdOf` answers 0 rather than reverting for an unregistered address, which
 * is what makes `cleargate-node register` safe to re-run: a provider who lost
 * their config.yaml recovers their existing identity instead of minting a second
 * one and orphaning the first.
 */
export async function agentIdOf(
    client: Client,
    registry: string,
    address: string,
): Promise<bigint> {
    const result = await new ContractCallQuery()
        .setContractId(contractId(registry))
        .setGas(QUERY_GAS)
        .setFunctionParameters(encode(IDENTITY_ABI, "agentIdOf", [address]))
        .execute(client);

    const [agentId] = IDENTITY_ABI.decodeFunctionResult(
        "agentIdOf",
        `0x${Buffer.from(result.asBytes()).toString("hex")}`,
    );
    return BigInt(agentId as bigint);
}

/** Registers this node as a new ERC-8004 agent. Returns the minted id. */
export async function newAgent(
    client: Client,
    registry: string,
    domain: string,
    address: string,
): Promise<{ agentId: bigint; transaction: string }> {
    const response = await new ContractExecuteTransaction()
        .setContractId(contractId(registry))
        .setGas(REGISTER_GAS)
        .setFunctionParameters(encode(IDENTITY_ABI, "newAgent", [domain, address]))
        .execute(client);

    // Wait for consensus before reading back, so the query below does not race
    // the write it is meant to confirm.
    await response.getReceipt(client);
    const agentId = await agentIdOf(client, registry, address);

    return { agentId, transaction: response.transactionId.toString() };
}

/** Moves an existing agent to a new domain and/or operator address. */
export async function updateAgent(
    client: Client,
    registry: string,
    agentId: bigint,
    domain: string,
    address: string,
): Promise<string> {
    const response = await new ContractExecuteTransaction()
        .setContractId(contractId(registry))
        .setGas(REGISTER_GAS)
        .setFunctionParameters(encode(IDENTITY_ABI, "updateAgent", [agentId, domain, address]))
        .execute(client);

    await response.getReceipt(client);
    return response.transactionId.toString();
}

/**
 * Closes an escrow session, paying the provider for time actually elapsed and
 * refunding the renter the rest.
 *
 * Permissionless at the contract, so this is not a privilege — it is the node
 * claiming money it has already earned. Calling early would only pay the node
 * less, which is why nobody needs to be trusted with it.
 */
export async function settleSession(
    client: Client,
    escrow: string,
    sessionId: string,
): Promise<string> {
    const response = await new ContractExecuteTransaction()
        .setContractId(contractId(escrow))
        .setGas(SETTLE_GAS)
        .setFunctionParameters(encode(ESCROW_ABI, "settle", [sessionId]))
        .execute(client);

    await response.getReceipt(client);
    return response.transactionId.toString();
}
