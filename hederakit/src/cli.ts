/**
 * `cleargate-hedera` — the provider node's Hedera signing sidecar.
 *
 * This exists so the Go daemon does not have to. `CLAUDE.md` invariant 1 says
 * the agent never imports a Hedera SDK, and the agent still does not: it spawns
 * this process the same way `internal/sshca` spawns `ssh-keygen` and
 * `internal/tunnel` spawns `cloudflared`. The Go binary stays small and holds
 * no key; exactly one process on a provider's machine reads the key file.
 *
 * The contract with the caller is deliberately rigid, because a Go program
 * parses it:
 *
 *   - stdout carries exactly one JSON object and nothing else, ever
 *   - stderr carries human-readable diagnostics
 *   - exit code 0 means the JSON on stdout is a result; non-zero means it is
 *     an error object and should be logged, not parsed for values
 *
 * Anything that would otherwise print to stdout (SDK chatter, warnings) would
 * corrupt that, so nothing here logs to stdout except the final `emit`.
 */
import { Command } from "commander";
import { readFileSync } from "node:fs";
import { ensureOperator, loadOperator } from "./key.js";
import { hederaClient, resolveOperator } from "./client.js";
import { lookupAccount } from "./mirror.js";
import { createTopic, publish } from "./hcs.js";
import { agentIdOf, newAgent, settleSession, updateAgent } from "./contract.js";
import { sendHbar } from "./transfer.js";

interface GlobalOptions {
    keyFile: string;
    network: string;
    mirrorUrl?: string;
}

function emit(payload: Record<string, unknown>): void {
    process.stdout.write(`${JSON.stringify(payload)}\n`);
}

function fail(error: unknown): never {
    const message = error instanceof Error ? error.message : String(error);
    process.stderr.write(`${message}\n`);
    emit({ error: message });
    process.exit(1);
}

/** Builds a signing client, resolving the operator's account id on the way. */
async function connect(options: GlobalOptions) {
    const operator = await resolveOperator(loadOperator(options.keyFile), options.mirrorUrl);
    return { operator, client: hederaClient(operator, options.network) };
}

const program = new Command()
    .name("cleargate-hedera")
    .description("Hedera signing sidecar for the ClearGate provider node")
    .version("0.1.0")
    .requiredOption("-k, --key-file <path>", "node-local operator key file")
    .option("-n, --network <network>", "hedera:testnet | hedera:mainnet", "hedera:testnet")
    .option("--mirror-url <url>", "mirror node base URL");

/**
 * Generates the operator key if it is not there, and reports it either way.
 *
 * Idempotent for the same reason `sshca.Ensure` is: setup runs it, serve runs
 * it again, and regenerating would strand both the funded account and the
 * ERC-8004 identity that point at the old key.
 */
program
    .command("keygen")
    .description("create the node-local operator key if absent, then describe it")
    .option("--out <path>", "write key to path (defaults to --key-file)")
    .action(async (opts: { out?: string }) => {
        const options = program.opts<GlobalOptions>();
        try {
            const keyPath = opts.out ?? options.keyFile;
            const operator = ensureOperator(keyPath);
            const account = await lookupAccount(operator.evmAddress, options.mirrorUrl);
            emit({
                evm_address: operator.evmAddress,
                account_id: account?.accountId ?? "",
                balance_tinybars: account?.balanceTinybars ?? "0",
                // An ECDSA key has an address from birth but no Hedera account
                // until someone funds it. Setup prints this as an instruction,
                // not as a failure.
                funded: account !== null,
            });
        } catch (error) {
            fail(error);
        }
    });

program
    .command("account-info")
    .description("resolve an account's id, EVM address, balance and receiver-sig flag")
    .option("--account <idOrAddress>", "account to look up (default: the operator's own)")
    .action(async (opts: { account?: string }) => {
        const options = program.opts<GlobalOptions>();
        try {
            const target = opts.account ?? loadOperator(options.keyFile).evmAddress;
            const account = await lookupAccount(target, options.mirrorUrl);
            if (!account) {
                emit({ found: false, queried: target });
                return;
            }
            emit({
                found: true,
                account_id: account.accountId,
                evm_address: account.evmAddress,
                balance_tinybars: account.balanceTinybars,
                // A pay_to with this set cannot be paid by the escrow contract,
                // which would make every session unsettleable. Setup refuses to
                // enable escrow mode when it is true.
                receiver_sig_required: account.receiverSigRequired,
            });
        } catch (error) {
            fail(error);
        }
    });

program
    .command("hcs-create-topic")
    .description("create this provider's audit topic — one time, at setup")
    .requiredOption("--memo <memo>", "topic memo, e.g. the node id")
    .action(async (opts: { memo: string }) => {
        const options = program.opts<GlobalOptions>();
        try {
            const { client } = await connect(options);
            const topicId = await createTopic(client, opts.memo);
            client.close();
            emit({ topic_id: topicId });
        } catch (error) {
            fail(error);
        }
    });

/**
 * Publishes one audit message, read from stdin.
 *
 * stdin rather than a flag on purpose: a receipt names a payer, a payee and an
 * amount, and argv is world-readable in the host's process table.
 */
program
    .command("hcs-publish")
    .description("publish one audit message, read from stdin")
    .requiredOption("--topic <topicId>", "topic to publish to")
    .action(async (opts: { topic: string }) => {
        const options = program.opts<GlobalOptions>();
        try {
            const message = readFileSync(0, "utf8").trim();
            if (!message) throw new Error("no message on stdin");

            const { client } = await connect(options);
            const sequenceNumber = await publish(client, opts.topic, message);
            client.close();
            emit({ topic_id: opts.topic, sequence_number: sequenceNumber });
        } catch (error) {
            fail(error);
        }
    });

/**
 * Registers this node's ERC-8004 identity, or adopts the one it already has.
 *
 * The check-then-register is what makes `cleargate-node register` re-runnable:
 * a provider who lost their config.yaml gets their existing id back rather than
 * minting a second one and orphaning the first.
 */
program
    .command("identity-register")
    .description("register this node as an ERC-8004 agent, or report its existing id")
    .requiredOption("--registry <contract>", "IdentityRegistry contract id or EVM address")
    .requiredOption("--domain <host>", "the host of this node's public_url")
    .action(async (opts: { registry: string; domain: string }) => {
        const options = program.opts<GlobalOptions>();
        try {
            const { operator, client } = await connect(options);

            const existing = await agentIdOf(client, opts.registry, operator.evmAddress);
            if (existing > 0n) {
                // Already registered. If the node has since moved, correct the
                // domain in place so the on-chain record still resolves to a
                // reachable agent card — but keep the id.
                const transaction = await updateAgent(
                    client,
                    opts.registry,
                    existing,
                    opts.domain,
                    operator.evmAddress,
                );
                client.close();
                emit({
                    agent_id: existing.toString(),
                    agent_address: operator.evmAddress,
                    transaction,
                    created: false,
                });
                return;
            }

            const { agentId, transaction } = await newAgent(
                client,
                opts.registry,
                opts.domain,
                operator.evmAddress,
            );
            client.close();
            emit({
                agent_id: agentId.toString(),
                agent_address: operator.evmAddress,
                transaction,
                created: true,
            });
        } catch (error) {
            fail(error);
        }
    });

/**
 * Returns a metered session's unburned credit to the renter who paid it.
 *
 * The recipient is not a flag the renter controls: the node passes the payer
 * the facilitator confirmed for the chunk, so a refund can only ever go back
 * where the money came from.
 */
program
    .command("refund")
    .description("send HBAR from the operator account back to a session's payer")
    .requiredOption("--to <accountId>", "the account to refund")
    .requiredOption("--tinybars <amount>", "how much to send, in tinybars")
    .option("--memo <memo>", "transaction memo, normally the session id", "")
    .action(async (opts: { to: string; tinybars: string; memo: string }) => {
        const options = program.opts<GlobalOptions>();
        try {
            const { operator, client } = await connect(options);
            const result = await sendHbar(
                client,
                operator.accountId,
                opts.to,
                opts.tinybars,
                opts.memo,
            );
            client.close();
            emit({
                transaction: result.transaction,
                amount_tinybars: result.amountTinybars,
                to: opts.to,
            });
        } catch (error) {
            fail(error);
        }
    });

program
    .command("escrow-settle")
    .description("close an escrow session, paying elapsed time and refunding the rest")
    .requiredOption("--contract <contract>", "SessionEscrow contract id or EVM address")
    .requiredOption("--session <bytes32>", "session id")
    .action(async (opts: { contract: string; session: string }) => {
        const options = program.opts<GlobalOptions>();
        try {
            const { client } = await connect(options);
            const transaction = await settleSession(client, opts.contract, opts.session);
            client.close();
            emit({ session_id: opts.session, transaction });
        } catch (error) {
            fail(error);
        }
    });

program.parseAsync(process.argv).catch(fail);
