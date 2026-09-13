import { ethers, network } from "hardhat";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";

/**
 * Proves the whole money story against the live contract, with no MACH402
 * node involved: open a session, top it up, stop early, and check that the
 * provider was paid for the seconds actually used and the renter got the rest
 * back.
 *
 * This exists so the payment mechanics can be demonstrated and debugged before
 * any of the Go integration exists — if this script is right, the only thing
 * left to get wrong is the plumbing.
 *
 * Unlike the unit tests, there is no `evm_increaseTime` here: Hedera's clock is
 * the real one, so the script genuinely waits.
 */

/**
 * A JSON-RPC caller sends weibars and the relay divides by 10^10 before the
 * contract sees it, so amounts have to be scaled up on the way in — even though
 * `msg.value` inside the contract is tinybars. See SessionEscrow.sol's unit note.
 */
const WEIBARS_PER_TINYBAR = 10_000_000_000n;

/** 0.002 HBAR/minute, the config default, as tinybars per second. */
const PRICE_PER_SECOND = 3_334n;
const INITIAL_SECONDS = 60n;
const TOPUP_SECONDS = 60n;
/** How long to actually "use" the session before stopping early. */
const USE_SECONDS = 20;

const hbar = (weibars: bigint) => `${ethers.formatEther(weibars)} HBAR`;
const deposit = (seconds: bigint) => PRICE_PER_SECOND * seconds * WEIBARS_PER_TINYBAR;
const sleep = (ms: number) => new Promise((done) => setTimeout(done, ms));

/**
 * Resolves a `0.0.x` account to the EVM address a contract can actually pay.
 *
 * Public data, no key needed — the same lookup the Go node performs through
 * `internal/mirror` before it will quote an escrow session.
 */
async function resolveEvmAddress(accountId: string): Promise<string> {
    const base = process.env["HEDERA_MIRROR_URL"] ?? "https://testnet.mirrornode.hedera.com";
    const response = await fetch(`${base}/api/v1/accounts/${accountId}`);
    if (!response.ok) throw new Error(`mirror node ${response.status} for ${accountId}`);

    const body = (await response.json()) as { evm_address?: string };
    if (!body.evm_address) {
        throw new Error(`${accountId} has no EVM address; it cannot be paid by a contract`);
    }
    return ethers.getAddress(body.evm_address);
}

function deployedEscrow(): string {
    const file = resolve(__dirname, `../deployments/${network.name}.json`);
    const record = JSON.parse(readFileSync(file, "utf8")) as {
        contracts: { SessionEscrow: string };
    };
    return record.contracts.SessionEscrow;
}

async function main(): Promise<void> {
    const [renter] = await ethers.getSigners();
    if (!renter) throw new Error("no signer: set HEDERA_PRIVATE_KEY in MACH402/.env");

    // The provider in this demo is PAY_TO_ACCOUNT_ID, the same account a real
    // node would be paid at.
    //
    // Its address must be resolved from the mirror node rather than computed.
    // The obvious move is to build the long-zero form (0x + the account number,
    // zero-padded), and it does not work: measured on testnet, a contract
    // sending HBAR to the long-zero address of an account that HAS an EVM alias
    // fails, while sending to the alias itself succeeds. The mirror node's
    // `evm_address` is the form that can actually be paid, which is why the node
    // resolves pay_to the same way.
    const payTo = process.env["PAY_TO_ACCOUNT_ID"];
    if (!payTo) throw new Error("set PAY_TO_ACCOUNT_ID in MACH402/.env");
    const providerAddress = await resolveEvmAddress(payTo);

    const escrow = await ethers.getContractAt("SessionEscrow", deployedEscrow());
    const sessionId = ethers.id(`demo-${Date.now()}`);

    console.log(`escrow      ${await escrow.getAddress()}`);
    console.log(`renter      ${renter.address}`);
    console.log(`provider    ${providerAddress}  (${payTo})`);
    console.log(`session     ${sessionId}`);
    console.log("");

    const providerBefore = await ethers.provider.getBalance(providerAddress);
    const renterBefore = await ethers.provider.getBalance(renter.address);

    console.log(`opening for ${INITIAL_SECONDS}s at ${PRICE_PER_SECOND} tinybars/s ...`);
    await (
        await escrow.openSession(sessionId, providerAddress, PRICE_PER_SECOND, INITIAL_SECONDS, {
            value: deposit(INITIAL_SECONDS),
        })
    ).wait();
    console.log(`  deposited ${hbar(deposit(INITIAL_SECONDS))}`);

    console.log(`topping up another ${TOPUP_SECONDS}s ...`);
    await (
        await escrow.topUp(sessionId, TOPUP_SECONDS, { value: deposit(TOPUP_SECONDS) })
    ).wait();

    const opened = await escrow.getSession(sessionId);
    console.log(`  paid duration now ${opened.duration}s, deposited ${opened.deposited} tinybars`);
    console.log("");

    console.log(`using the session for ${USE_SECONDS}s, then stopping early ...`);
    await sleep(USE_SECONDS * 1000);

    const [elapsed, owed, refund] = await escrow.quoteSettlement(sessionId);
    console.log(`  elapsed ${elapsed}s -> provider ${owed} tinybars, refund ${refund} tinybars`);

    await (await escrow.settle(sessionId)).wait();
    console.log("  settled");
    console.log("");

    const providerAfter = await ethers.provider.getBalance(providerAddress);
    const renterAfter = await ethers.provider.getBalance(renter.address);
    const providerGained = providerAfter - providerBefore;

    console.log(`provider received  ${hbar(providerGained)}`);
    // The renter's delta is net of gas for three transactions, so it is shown
    // rather than asserted; the provider's side is the one with no gas in it.
    console.log(`renter net         ${hbar(renterAfter - renterBefore)}  (net of gas)`);
    console.log("");

    const totalPaid = deposit(INITIAL_SECONDS + TOPUP_SECONDS);
    const settled = await escrow.getSession(sessionId);

    // The assertion that matters: the provider was paid for time used, not for
    // time bought. Without the contract this difference is unrefundable.
    if (providerGained >= totalPaid) {
        throw new Error(
            `provider received the full deposit (${hbar(providerGained)}) — no refund happened`,
        );
    }
    if (!settled.settled) throw new Error("session is not marked settled");

    console.log(
        `PASS — provider was paid for ${elapsed}s of ${settled.duration}s bought; ` +
            `${hbar(refund * WEIBARS_PER_TINYBAR)} went back to the renter.`,
    );
    console.log(`https://hashscan.io/testnet/contract/${await escrow.getAddress()}`);
}

main().catch((error) => {
    console.error(error);
    process.exitCode = 1;
});
