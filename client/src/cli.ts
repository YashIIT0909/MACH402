#!/usr/bin/env -S node --experimental-strip-types
/**
 * cleargate — rent GPU time from a ClearGate node and pay for it over x402.
 *
 *   cleargate quote      --node <url>
 *   cleargate run        --node <url> --image <img> [--script train.py]
 *   cleargate rent       --node <url> [--minutes 30] [--budget <tinybars>]
 *   cleargate logs       --node <url> --job <id> --token <token>
 *   cleargate stop       --node <url> --job <id> --token <token>
 *   cleargate stop-lease --node <url> --lease <id> --token <token>
 *   cleargate spend
 *
 * `run` and `rent` are the two ways to buy compute here, and they trade off in
 * opposite directions. `run` sends a script to the node and gets output back;
 * `rent` sends nothing and gets a shell.
 */
import { readFileSync, writeFileSync } from "node:fs";
import { basename, resolve } from "node:path";
import { Command } from "commander";
import type { JobSpec, LeaseCreated, LeaseSpec, SessionSpec } from "@cleargate/types";
import { formatTinybars, hashscanUrl, isLeaseTerminal, isSessionTerminal } from "@cleargate/types";
import { EscrowClient } from "./escrow.js";
import { createPayer } from "./pay.js";
import {
  colabUrl,
  createLeaseIdentity,
  discardLeaseIdentity,
  localColabUrl,
  saveCertificate,
  sshCommand,
  type LeaseIdentity,
} from "./lease.js";
import { NodeClient } from "./node.js";
import { readSpend, recordSpend } from "./receipt.js";

const program = new Command()
  .name("cleargate")
  .description("Rent GPU time and pay per job over x402 on Hedera")
  .version("0.1.0");

program
  .command("quote")
  .description("Show a node's price, hardware and limits — free, no payment")
  .requiredOption("-n, --node <url>", "node base URL, e.g. http://localhost:8402")
  .action(async (options: { node: string }) => {
    const specs = await new NodeClient(options.node).specs();

    console.log(`node        ${specs.node_id} (agent ${specs.agent_version})`);
    console.log(`price       ${formatTinybars(specs.price_tinybars)} per job (${specs.price_tinybars} tinybars)`);
    console.log(`pay to      ${specs.pay_to}`);
    console.log(`network     ${specs.network}, asset ${specs.asset}`);
    if (specs.gpu.available) {
      console.log(`gpu         ${specs.gpu.model} (${specs.gpu.vram_mb} MB)`);
    } else {
      console.log(`gpu         none — CPU-fallback mode (${specs.gpu.reason ?? "unspecified"})`);
    }
    console.log(`limits      ${specs.limits.max_seconds}s, ${specs.limits.memory_mb} MB, ${specs.limits.cpu_cores} cores`);
    console.log(`images      ${specs.image_allowlist.join(", ")}`);

    // Two different products on one node, so say which are on offer. A node
    // that never opted into leasing has no `leases` block at all.
    if (specs.leases === undefined) {
      console.log(`leases      not offered — this node sells batch jobs only`);
      return;
    }
    const offer = specs.leases;
    console.log(
      `leases      ${formatTinybars(offer.price_tinybars_per_minute)} per minute ` +
        `(${offer.min_minutes}–${offer.max_minutes} min a slice, ${offer.max_total_minutes} min cap)`,
    );
    console.log(
      `            ${offer.cpu_cores} cores, ${offer.memory_mb} MB, ${offer.workspace_gb} GB workspace`,
    );
    console.log(
      `            ${offer.ssh ? "ssh + jupyter" : "jupyter only (this node's tunnel carries no ssh)"}`,
    );
    // The host having a card is not the same question as a lease being able to
    // use it, and this is the line that tells them apart before any money moves.
    if (offer.gpu) {
      console.log(`            gpu usable inside the lease`);
    } else if (specs.gpu.available) {
      console.log(
        `            no gpu inside the lease — this node has a card, but its lease`,
      );
      console.log(
        `            image has no CUDA runtime, so leases here are CPU-only`,
      );
    } else {
      console.log(`            cpu only`);
    }
  });

program
  .command("run")
  .description("Pay for and run one job, streaming its output")
  .requiredOption("-n, --node <url>", "node base URL")
  .requiredOption("-i, --image <image>", "container image (must be on the node's allowlist)")
  .option("-s, --script <path>", "local script to upload and run")
  .option("-c, --cmd <command...>", "command override")
  .option("-t, --timeout <seconds>", "wall-clock limit", parseIntArg)
  .option("-o, --output <path>", "write the artifact tarball here")
  .option("-d, --dataset <url>", "dataset the node downloads and mounts at /data")
  .option("--dataset-sha256 <hex>", "verify the dataset against this checksum")
  .option("--dataset-name <filename>", "name the dataset file inside /data")
  .option("--no-extract", "keep a downloaded archive intact instead of unpacking it")
  .option("-g, --gpu", "require a usable GPU; refuse CPU-fallback nodes before paying")
  .option("--budget <tinybars>", "refuse to pay more than this for the job")
  .option("--no-follow", "return as soon as the job starts instead of streaming logs")
  .action(runJob);

program
  .command("rent")
  .description("Rent a shell and a Jupyter server on a node's GPU, by the minute")
  .requiredOption("-n, --node <url>", "node base URL")
  .option("-m, --minutes <n>", "minutes to buy up front", parseIntArg, 15)
  .option("-g, --gpu", "require a usable GPU; refuse CPU-fallback nodes before paying")
  .option("--budget <tinybars>", "stop extending once this much has been spent in total")
  .option("--extend-minutes <n>", "size of each auto-extension", parseIntArg)
  .option("--no-extend", "buy one slice and let the lease lapse instead of topping it up")
  .option("--local-port <port>", "local port to forward Jupyter to over SSH", parseIntArg, 8888)
  .action(rent);

program
  .command("logs")
  .description("Stream an existing job's output")
  .requiredOption("-n, --node <url>", "node base URL")
  .requiredOption("-j, --job <id>", "job id")
  .requiredOption("-t, --token <token>", "job token issued at payment")
  .action(async (options: { node: string; job: string; token: string }) => {
    const node = new NodeClient(options.node);
    for await (const event of node.followLogs(options.job, options.token)) {
      if (event.event === "log") {
        printLog(event.data.stream, event.data.text);
      } else {
        console.log(`\njob ${event.data.status}`);
      }
    }
  });

program
  .command("stop")
  .description("Kill a running job")
  .requiredOption("-n, --node <url>", "node base URL")
  .requiredOption("-j, --job <id>", "job id")
  .requiredOption("-t, --token <token>", "job token issued at payment")
  .action(async (options: { node: string; job: string; token: string }) => {
    const state = await new NodeClient(options.node).stop(options.job, options.token);
    console.log(`job ${state.job_id} is now ${state.status}`);
    console.log("flat-fee jobs are paid up front, so stopping does not refund — " +
      "metered leases are where stopping saves money");
  });

program
  .command("stop-lease")
  .description("End a lease early and stop its meter")
  .requiredOption("-n, --node <url>", "node base URL")
  .requiredOption("-l, --lease <id>", "lease id")
  .requiredOption("-t, --token <token>", "lease token issued at purchase")
  .action(async (options: { node: string; lease: string; token: string }) => {
    const state = await new NodeClient(options.node).stopLease(options.lease, options.token);
    console.log(`lease ${state.lease_id} is now ${state.status}`);
    console.log(
      `you paid for ${state.paid_minutes} minutes; time already bought is not refunded, ` +
        "but nothing further will be charged",
    );
  });

program
  .command("session")
  .description("rent a GPU by the second, with the unused time refunded when you stop")
  .requiredOption("-n, --node <url>", "node base URL")
  .option("-m, --minutes <n>", "minutes to buy up front", parseIntArg, 15)
  .option("-g, --gpu", "require a GPU")
  .option("--budget <tinybars>", "stop topping up before total deposits pass this", parseIntArg)
  .option("--top-up-minutes <n>", "minutes to add each time (default: same as --minutes)", parseIntArg)
  .option("--no-top-up", "let the session lapse instead of topping it up")
  .option("--local-port <port>", "local port for the forwarded Jupyter", parseIntArg, 8888)
  .action(async (options: SessionCommandOptions) => {
    await session({
      node: options.node,
      minutes: options.minutes,
      gpu: options.gpu,
      budget: options.budget,
      topUpMinutes: options.topUpMinutes,
      topUp: options.topUp,
      localPort: options.localPort,
    });
  });

program
  .command("settle")
  .description("close an escrow session on-chain and collect your refund")
  .requiredOption("-n, --node <url>", "node the session was rented from")
  .requiredOption("-s, --session <id>", "session id")
  .action(async (options: { node: string; session: string }) => {
    await settleSession(options.node, options.session);
  });

program
  .command("spend")
  .description("Show what this machine has paid for compute")
  .action(() => {
    const records = readSpend();
    if (records.length === 0) {
      console.log("no payments recorded yet");
      return;
    }
    let total = 0n;
    for (const record of records) {
      total += BigInt(record.amount_tinybars);
      console.log(
        `${record.paid_at.slice(0, 19)}  ${record.amount_tinybars.padStart(12)}  ${record.transaction}`,
      );
    }
    console.log(`\n${records.length} payment(s), ${total} tinybars (${formatTinybars(total.toString())})`);
  });

type RunOptions = {
  node: string;
  image: string;
  script?: string;
  cmd?: string[];
  timeout?: number;
  output?: string;
  dataset?: string;
  datasetSha256?: string;
  datasetName?: string;
  extract: boolean;
  gpu?: boolean;
  budget?: string;
  follow: boolean;
};

async function runJob(options: RunOptions): Promise<void> {
  const node = new NodeClient(options.node);

  // Quote before paying, so the renter sees the price and can be refused by
  // their own budget rather than by a failed transaction.
  const specs = await node.specs();
  const price = BigInt(specs.price_tinybars);
  console.log(`node        ${specs.node_id}`);
  console.log(`price       ${formatTinybars(specs.price_tinybars)}`);

  if (options.budget !== undefined && price > BigInt(options.budget)) {
    throw new Error(
      `node asks ${specs.price_tinybars} tinybars, above your --budget of ${options.budget}`,
    );
  }
  if (!specs.image_allowlist.includes(options.image)) {
    throw new Error(
      `node does not allow image "${options.image}". It runs: ${specs.image_allowlist.join(", ")}`,
    );
  }
  // Check the GPU here as well as on the node. Both refusals are free, but this
  // one explains itself better and happens before a round trip.
  if (options.gpu === true && !specs.gpu.available) {
    throw new Error(
      `you asked for a GPU, but this node is in CPU-fallback mode` +
        `${specs.gpu.reason === undefined ? "" : ` (${specs.gpu.reason})`}. ` +
        `Drop --gpu to run on CPU, or rent a node whose quote shows a GPU.`,
    );
  }

  const spec: JobSpec = { image: options.image };
  if (options.cmd !== undefined) spec.cmd = options.cmd;
  if (options.timeout !== undefined) spec.timeout_seconds = options.timeout;
  if (options.gpu === true) spec.require_gpu = true;
  if (options.dataset !== undefined) {
    spec.dataset = { url: options.dataset };
    if (options.datasetSha256 !== undefined) spec.dataset.sha256 = options.datasetSha256;
    if (options.datasetName !== undefined) spec.dataset.filename = options.datasetName;
    if (!options.extract) spec.dataset.no_extract = true;
    console.log(`dataset     ${options.dataset}`);
    console.log(`            the node downloads this and mounts it at /data — your`);
    console.log(`            container has no network and cannot fetch it itself`);
  }
  if (options.script !== undefined) {
    const path = resolve(options.script);
    const filename = basename(path);
    spec.script = { filename, content_base64: readFileSync(path).toString("base64") };
    // Default to running the uploaded script when no command was given.
    if (spec.cmd === undefined) {
      spec.cmd = ["python", filename];
    }
  }

  const payer = createPayer(
    options.budget === undefined ? {} : { maxTinybarsPerPayment: BigInt(options.budget) },
  );
  console.log(`paying as   ${payer.accountId}`);

  const { job, settlement } = await node.createJob(payer, spec);
  recordSpend(settlement, options.node, job.job_id, job.amount_tinybars);

  console.log(`\njob         ${job.job_id}`);
  console.log(`token       ${job.token}`);
  console.log(`transaction ${settlement.transaction}`);
  console.log(`hashscan    ${hashscanUrl(settlement.transaction, settlement.network)}`);

  if (!options.follow) {
    console.log(`\nfollow it with:  cleargate logs -n ${options.node} -j ${job.job_id} -t ${job.token}`);
    return;
  }

  console.log("\n--- output ---");
  if (options.dataset !== undefined) {
    console.log("(staging the dataset — the job starts once it is in place)");
  }
  let finalStatus = "unknown";
  for await (const event of node.followLogs(job.job_id, job.token)) {
    if (event.event === "log") {
      printLog(event.data.stream, event.data.text);
    } else {
      finalStatus = event.data.status;
    }
  }
  console.log(`--- ${finalStatus} ---`);

  if (options.output !== undefined) {
    const tarball = await node.artifact(job.job_id, job.token);
    writeFileSync(options.output, Buffer.from(tarball));
    console.log(`artifact    ${options.output} (${Buffer.from(tarball).length} bytes)`);
  }
}

type RentOptions = {
  node: string;
  minutes: number;
  gpu?: boolean;
  budget?: string;
  extendMinutes?: number;
  extend: boolean;
  localPort: number;
};

/**
 * Rents interactive time on a node.
 *
 * Nothing of the renter's leaves this machine: no script is uploaded, no
 * dataset URL is handed over. What is sent is one ephemeral SSH public key, and
 * what comes back is a certificate for it that expires when the paid time does.
 *
 * The lease is then held open by buying another slice before the current one
 * lapses, and released — deliberately, including on ctrl-c — when the renter is
 * done, so a provider is never left running a container nobody is paying for.
 */
async function rent(options: RentOptions): Promise<void> {
  const node = new NodeClient(options.node);
  const specs = await node.specs();

  if (specs.leases === undefined) {
    throw new Error(
      `node ${specs.node_id} does not offer interactive leases. ` +
        `It sells batch jobs — try \`cleargate run\`.`,
    );
  }
  const offer = specs.leases;

  if (options.gpu === true && !specs.gpu.available) {
    throw new Error(
      "you asked for a GPU, but this node is in CPU-fallback mode" +
        `${specs.gpu.reason === undefined ? "" : ` (${specs.gpu.reason})`}.`,
    );
  }
  // Refused here as well as on the node, because this is the version that can
  // explain itself: the card is real, and the lease image simply cannot reach it.
  if (options.gpu === true && !offer.gpu) {
    throw new Error(
      "this node has a working GPU, but its lease image has no CUDA runtime, so a " +
        "container on it cannot compute on the card. The provider needs to rebuild " +
        "with `make lease-image`. Drop --gpu to rent it as CPU.",
    );
  }
  if (options.minutes < offer.min_minutes || options.minutes > offer.max_minutes) {
    throw new Error(
      `this node sells between ${offer.min_minutes} and ${offer.max_minutes} minutes at a time`,
    );
  }

  const perMinute = BigInt(offer.price_tinybars_per_minute);
  const slicePrice = perMinute * BigInt(options.minutes);

  console.log(`node        ${specs.node_id}`);
  console.log(`price       ${formatTinybars(offer.price_tinybars_per_minute)} per minute`);
  console.log(`first slice ${options.minutes} min, ${formatTinybars(slicePrice.toString())}`);
  if (offer.gpu) {
    console.log(`gpu         ${specs.gpu.model} (${specs.gpu.vram_mb} MB)`);
  } else if (specs.gpu.available) {
    console.log(`gpu         ${specs.gpu.model}, but NOT usable from this lease —`);
    console.log(`            the node's lease image has no CUDA runtime`);
  } else {
    console.log(`gpu         none — CPU-fallback mode`);
  }
  console.log(`egress      ${offer.egress_allowlist.slice(0, 4).join(", ")}${
    offer.egress_allowlist.length > 4 ? `, +${offer.egress_allowlist.length - 4} more` : ""
  }`);

  if (options.budget !== undefined && slicePrice > BigInt(options.budget)) {
    throw new Error(
      `the first slice costs ${slicePrice} tinybars, above your --budget of ${options.budget}`,
    );
  }

  // Generated here, used once, thrown away. The node signs the public half and
  // never sees the private one.
  const identity = createLeaseIdentity();

  const payer = createPayer(
    options.budget === undefined ? {} : { maxTinybarsPerPayment: BigInt(options.budget) },
  );
  console.log(`paying as   ${payer.accountId}`);

  const spec: LeaseSpec = { minutes: options.minutes, public_key: identity.publicKey };
  if (options.gpu === true) spec.require_gpu = true;

  const { lease, settlement } = await node.createLease(payer, spec);
  recordSpend(settlement, options.node, lease.lease_id, lease.amount_tinybars);
  saveCertificate(identity, lease.certificate);

  const leaseToken = lease.token ?? "";
  let spent = BigInt(lease.amount_tinybars);

  console.log(`\nlease       ${lease.lease_id}`);
  console.log(`expires     ${lease.expires_at}`);
  console.log(`transaction ${settlement.transaction}`);
  console.log(`hashscan    ${hashscanUrl(settlement.transaction, settlement.network)}`);

  printConnectionDetails(lease, identity, options.localPort);

  // Releasing the lease is the renter's responsibility and the provider's
  // interest: without this, ctrl-c would walk away from a running container the
  // node keeps holding until its grace period expires.
  let released = false;
  const release = async (): Promise<void> => {
    if (released) return;
    released = true;
    try {
      await node.stopLease(lease.lease_id, leaseToken);
      console.log(`\nlease ${lease.lease_id} stopped; total spent ${formatTinybars(spent.toString())}`);
    } catch (error: unknown) {
      console.error(`\ncould not stop the lease cleanly: ${String(error)}`);
      console.error("the node freezes and then reclaims it on its own, so nothing keeps billing");
    } finally {
      discardLeaseIdentity(identity);
    }
  };

  for (const signal of ["SIGINT", "SIGTERM"] as const) {
    process.on(signal, () => {
      void release().then(() => process.exit(0));
    });
  }

  if (!options.extend) {
    console.log("\n--no-extend: this lease will lapse when its first slice runs out.");
    console.log(`stop it early with:  cleargate stop-lease -n ${options.node} -l ${lease.lease_id} -t ${leaseToken}`);
    return;
  }

  const extendMinutes = options.extendMinutes ?? options.minutes;
  const extendPrice = perMinute * BigInt(extendMinutes);
  console.log(`\nauto-extending by ${extendMinutes} min (${formatTinybars(extendPrice.toString())}) as time runs low.`);
  console.log("ctrl-c to stop the lease and stop paying.\n");

  spent = await holdLease({
    node,
    payer,
    lease,
    identity,
    token: leaseToken,
    nodeUrl: options.node,
    extendMinutes,
    extendPrice,
    budget: options.budget === undefined ? null : BigInt(options.budget),
    spent,
  });

  await release();
}

/**
 * Keeps a lease alive by buying another slice before the current one runs out,
 * and returns the total spent when it stops.
 *
 * The budget is the renter's own guard rail and is enforced here, on this
 * machine, rather than trusted to the node: it stops extending before the next
 * slice would take the total past the cap. The node's own freeze-and-reap
 * timers are a separate mechanism with a different job — they protect the
 * provider from an unpaid container, not the renter from overspending.
 */
async function holdLease(args: {
  node: NodeClient;
  payer: ReturnType<typeof createPayer>;
  lease: LeaseCreated;
  identity: LeaseIdentity;
  token: string;
  nodeUrl: string;
  extendMinutes: number;
  extendPrice: bigint;
  budget: bigint | null;
  spent: bigint;
}): Promise<bigint> {
  let spent = args.spent;

  while (true) {
    const state = await args.node.leaseState(args.lease.lease_id, args.token);
    if (isLeaseTerminal(state.status)) {
      console.log(`lease ${state.status}`);
      return spent;
    }

    // Top up with a slice's worth of runway to spare, so a slow settlement
    // never lets the lease lapse into a freeze the renter did not intend.
    if (state.seconds_remaining > EXTEND_THRESHOLD_SECONDS) {
      await sleep(POLL_INTERVAL_MS);
      continue;
    }

    if (args.budget !== null && spent + args.extendPrice > args.budget) {
      console.log(
        `\nbudget reached: ${formatTinybars(spent.toString())} spent, and another ` +
          `${formatTinybars(args.extendPrice.toString())} would pass your cap.`,
      );
      console.log("not extending; the lease will lapse.");
      return spent;
    }

    try {
      const { lease: extended, settlement } = await args.node.extendLease(
        args.payer,
        args.lease.lease_id,
        args.token,
        { minutes: args.extendMinutes, public_key: args.identity.publicKey },
      );
      // The certificate that came back replaces the one on disk: the old one
      // expires with the slice it was minted for.
      saveCertificate(args.identity, extended.certificate);
      recordSpend(settlement, args.nodeUrl, extended.lease_id, extended.amount_tinybars);
      spent += BigInt(extended.amount_tinybars);
      console.log(
        `extended to ${extended.expires_at}  (+${args.extendMinutes} min, ` +
          `${formatTinybars(spent.toString())} spent so far)`,
      );
    } catch (error: unknown) {
      console.error(`could not extend the lease: ${String(error)}`);
      console.error("the node freezes the container rather than killing it, so retrying may still recover it");
      await sleep(POLL_INTERVAL_MS);
    }
  }
}

/** Buy the next slice once this little time is left on the current one. */
const EXTEND_THRESHOLD_SECONDS = 60;
const POLL_INTERVAL_MS = 10_000;


type SessionCommandOptions = {
  node: string;
  minutes: number;
  gpu?: boolean;
  budget?: number;
  topUpMinutes?: number;
  topUp: boolean;
  localPort: number;
};

type SessionOptions = {
  node: string;
  minutes: number;
  gpu?: boolean;
  budget?: number;
  topUpMinutes?: number;
  topUp: boolean;
  localPort: number;
};

/**
 * Rents a GPU through the escrow contract.
 *
 * The shape is `rent`'s, with one difference that runs through everything: the
 * money goes into a contract rather than to the provider, so stopping early
 * gives the unused time back. That turns the exit path from a courtesy into the
 * thing the renter actually cares about — hence the settle on ctrl-c, and the
 * refund printed at the end.
 *
 * Order matters and is the inverse of `rent`. Here the renter deposits FIRST
 * and the node verifies afterwards by reading the chain, because a contract
 * call the renter signs and submits themselves is already final — there is no
 * facilitator to co-sign it and no window in which it could expire.
 */
async function session(options: SessionOptions): Promise<void> {
  const node = new NodeClient(options.node);
  const specs = await node.specs();

  if (options.gpu === true && !specs.gpu.available) {
    throw new Error(
      "you asked for a GPU, but this node is in CPU-fallback mode" +
        `${specs.gpu.reason === undefined ? "" : ` (${specs.gpu.reason})`}.`,
    );
  }

  const seconds = options.minutes * 60;
  const identity = createLeaseIdentity();
  const spec: SessionSpec = {
    seconds,
    public_key: identity.publicKey,
    ...(options.gpu === true ? { require_gpu: true } : {}),
  };

  // Free: ask before paying. The quote is what the deposit has to match, and
  // the node checks the chain against this same quote before provisioning.
  const quote = await node.quoteSession(spec);
  const pricePerSecond = BigInt(quote.price_tinybars_per_second);
  const deposit = pricePerSecond * BigInt(seconds);

  console.log(`node        ${specs.node_id}`);
  console.log(`price       ${formatTinybars(pricePerSecond.toString())} per second`);
  console.log(`first block ${options.minutes} min, ${formatTinybars(deposit.toString())} deposited`);
  console.log(`escrow      ${quote.escrow_contract}`);
  console.log(`session     ${quote.session_id}`);

  if (options.budget !== undefined && deposit > BigInt(options.budget)) {
    discardLeaseIdentity(identity);
    throw new Error(
      `the first deposit alone is ${formatTinybars(deposit.toString())}, over your ` +
        `--budget of ${formatTinybars(String(options.budget))}. Nothing was spent.`,
    );
  }

  const escrow = new EscrowClient(quote.escrow_contract, quote.network);
  let deposited = 0n;
  let settled = false;

  /**
   * Closes the session on-chain and reports the refund.
   *
   * Permissionless at the contract, so this always works — even if the node has
   * vanished. Calling it early only ever refunds more, so there is never a
   * reason to wait.
   */
  const settleNow = async (): Promise<void> => {
    if (settled) return;
    settled = true;
    try {
      const final = await escrow.quoteSettlement(quote.session_id);
      const result = await escrow.settle(quote.session_id);
      if (result.alreadySettled) {
        console.log("\nsession already settled on-chain by the provider.");
        return;
      }
      console.log(
        `\nsettled: ${final.elapsed}s used, ` +
          `${formatTinybars(final.providerAmount.toString())} to the provider, ` +
          `${formatTinybars(final.refundAmount.toString())} refunded to you.`,
      );
      console.log(`hashscan    ${hashscanUrl(result.transaction, quote.network)}`);
    } catch (error: unknown) {
      // The money is still in the contract, which is the safe place for it to
      // be. Say exactly how to get it, because nobody else will do it for them.
      console.error(`\ncould not settle: ${String(error)}`);
      console.error(
        `your deposit is still in the escrow contract — retry with:\n` +
          `  cleargate settle -n ${options.node} -s ${quote.session_id}`,
      );
    }
  };

  let created;
  try {
    const depositTx = await escrow.openSession({
      sessionId: quote.session_id,
      providerAddress: quote.provider_address,
      pricePerSecond,
      seconds,
    });
    deposited = deposit;
    console.log(`deposit     ${depositTx}`);
    console.log("provisioning container & quick tunnel (~15-25s) ...");

    created = await node.claimSession(spec, quote.session_id, depositTx);
  } catch (error: unknown) {
    // Anything that fails after the deposit landed leaves money in the
    // contract, so settle before giving up rather than leaving it stranded.
    if (deposited > 0n) await settleNow();
    discardLeaseIdentity(identity);
    escrow.close();
    throw error;
  }

  saveCertificate(identity, created.certificate);
  console.log(`\nsession     ${created.session_id}`);
  console.log(`status      ${created.status}`);
  console.log(`expires     ${created.expires_at}`);

  printConnectionDetails(
    {
      lease_id: created.lease_id,
      status: created.status,
      expires_at: created.expires_at,
      certificate: created.certificate,
      ssh_user: created.ssh_user,
      ...(created.ssh_host === undefined ? {} : { ssh_host: created.ssh_host }),
      ssh_principal: created.ssh_principal,
      jupyter_url: created.jupyter_url,
      jupyter_token: created.jupyter_token,
      tunnel_mode: created.tunnel_mode,
      transaction: created.deposit_transaction,
      payer: "",
      amount_tinybars: created.deposited_tinybars,
      minutes: options.minutes,
    },
    identity,
    options.localPort,
  );

  let released = false;
  const release = async (): Promise<void> => {
    if (released) return;
    released = true;
    try {
      await node.stopSession(created.session_id, created.token ?? "");
    } catch (error: unknown) {
      console.error(`\ncould not stop the session cleanly: ${String(error)}`);
    }
    // Settle regardless of whether the node answered. The refund is the
    // renter's money and does not depend on the provider cooperating.
    await settleNow();
    discardLeaseIdentity(identity);
    escrow.close();
  };

  for (const signal of ["SIGINT", "SIGTERM"] as const) {
    process.on(signal, () => {
      void release().then(() => process.exit(0));
    });
  }

  if (!options.topUp) {
    console.log("\n--no-top-up: this session lapses when the deposited time runs out.");
    console.log(`then collect your refund with:  cleargate settle -n ${options.node} -s ${created.session_id}`);
    // Close the Hedera client explicitly. Its gRPC connections keep Node's
    // event loop alive, so simply returning here leaves the CLI hanging with a
    // live session it is no longer managing — which then looks to the node like
    // a renter who is still connected.
    escrow.close();
    return;
  }

  const topUpMinutes = options.topUpMinutes ?? options.minutes;
  const topUpCost = pricePerSecond * BigInt(topUpMinutes * 60);
  console.log(
    `\nauto-topping up by ${topUpMinutes} min (${formatTinybars(topUpCost.toString())}) as time runs low.`,
  );
  console.log("ctrl-c to stop and get the unused time back.\n");

  deposited = await holdSession({
    node,
    escrow,
    sessionId: created.session_id,
    token: created.token ?? "",
    pricePerSecond,
    topUpSeconds: topUpMinutes * 60,
    budget: options.budget === undefined ? null : BigInt(options.budget),
    deposited,
  });

  await release();
}

/**
 * Keeps a session funded, buying more time before the deposited time runs out.
 *
 * The escrow twin of holdLease, and the budget works the same way: enforced
 * here, on the renter's machine, against cumulative DEPOSITS rather than
 * cumulative spend. That distinction is the point of escrow — a deposit is not
 * yet a payment, and whatever is not used comes back at settle.
 */
async function holdSession(args: {
  node: NodeClient;
  escrow: EscrowClient;
  sessionId: string;
  token: string;
  pricePerSecond: bigint;
  topUpSeconds: number;
  budget: bigint | null;
  deposited: bigint;
}): Promise<bigint> {
  let deposited = args.deposited;
  const topUpCost = args.pricePerSecond * BigInt(args.topUpSeconds);

  for (;;) {
    let state;
    try {
      state = await args.node.sessionState(args.sessionId, args.token);
    } catch (error: unknown) {
      // A node that has gone away does not strand the renter: their deposit is
      // in the contract and settle is permissionless.
      console.error(`\nlost contact with the node: ${String(error)}`);
      return deposited;
    }

    if (isSessionTerminal(state.status)) {
      console.log(`\nsession ${state.status}.`);
      return deposited;
    }

    if (state.seconds_remaining > TOP_UP_THRESHOLD_SECONDS) {
      await sleep(POLL_INTERVAL_MS);
      continue;
    }

    if (args.budget !== null && deposited + topUpCost > args.budget) {
      console.log(
        `\nbudget reached: ${formatTinybars(deposited.toString())} deposited, and another ` +
          `${formatTinybars(topUpCost.toString())} would pass your cap.`,
      );
      console.log("the session will lapse; whatever you did not use is refunded at settle.");
      return deposited;
    }

    try {
      await args.escrow.topUp({
        sessionId: args.sessionId,
        pricePerSecond: args.pricePerSecond,
        seconds: args.topUpSeconds,
      });
      deposited += topUpCost;
      // Tell the node to re-read the contract. Its own sweep would notice
      // eventually, but a renter mid-run should not be frozen while it does.
      const updated = await args.node.topUpSession(args.sessionId, args.token);
      console.log(
        `topped up to ${updated.expires_at} ` +
          `(${formatTinybars(deposited.toString())} deposited so far)`,
      );
    } catch (error: unknown) {
      console.error(`\ncould not top up: ${String(error)}`);
      console.error("the session will lapse; unused time is still refunded at settle.");
      return deposited;
    }
  }
}

/**
 * Closes a session the renter left open.
 *
 * The recovery path for every way a `session` run can end badly — the process
 * was killed, the node vanished mid-run, a claim failed after the deposit
 * landed. `settle` is permissionless, so this works without the provider.
 */
async function settleSession(nodeUrl: string, sessionId: string): Promise<void> {
  const specs = await new NodeClient(nodeUrl).specs();
  const contract = specs.leases?.escrow_contract;
  if (contract === undefined || contract === "") {
    throw new Error(`node ${specs.node_id} does not sell escrow sessions, so it has no contract to settle against`);
  }

  const escrow = new EscrowClient(contract, specs.network);
  try {
    const quote = await escrow.quoteSettlement(sessionId);
    const result = await escrow.settle(sessionId);
    if (result.alreadySettled) {
      console.log("already settled — nothing left to collect.");
      return;
    }
    console.log(
      `settled: ${quote.elapsed}s used, ` +
        `${formatTinybars(quote.providerAmount.toString())} to the provider, ` +
        `${formatTinybars(quote.refundAmount.toString())} refunded to you.`,
    );
    console.log(`hashscan    ${hashscanUrl(result.transaction, specs.network)}`);
  } finally {
    escrow.close();
  }
}

/** How low the deposited time gets before another block is bought. */
const TOP_UP_THRESHOLD_SECONDS = 60;

function sleep(ms: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

/**
 * Prints how to actually use the lease.
 *
 * Two paths, and which one is available depends on how the node published it.
 * A named tunnel carries TCP, so there is an SSH terminal and Jupyter can be
 * reached either directly or through a forwarded port. A quick tunnel is HTTP
 * only: the notebook works, and there is no terminal to offer.
 */
function printConnectionDetails(
  lease: LeaseCreated,
  identity: LeaseIdentity,
  localPort: number,
): void {
  console.log("\n--- connect ---");

  if (lease.ssh_host !== undefined && lease.ssh_host !== "") {
    console.log("shell (also forwards Jupyter to your own localhost):");
    console.log(`  ${sshCommand(lease, identity, localPort)}`);
    console.log("");
    console.log("Colab -> Connect to a local runtime, with that ssh running:");
    console.log(`  ${localColabUrl(lease, localPort)}`);
    console.log("");
    console.log("or straight over the tunnel, with no ssh at all:");
    console.log(`  ${colabUrl(lease)}`);
  } else {
    console.log(`this node publishes over a ${lease.tunnel_mode} tunnel, which carries HTTP only —`);
    console.log("Jupyter works, and there is no SSH terminal on this one.");
    console.log("");
    console.log("Colab -> Connect to a local runtime:");
    console.log(`  ${colabUrl(lease)}`);
  }

  console.log("");
  console.log(`your key    ${identity.keyPath} (generated here; the node never saw the private half)`);
  console.log(`certificate valid until ${lease.expires_at}, for principal ${lease.ssh_principal}`);
}

function printLog(stream: string, text: string): void {
  if (stream === "stderr") {
    process.stderr.write(`${text}\n`);
  } else {
    process.stdout.write(`${text}\n`);
  }
}

function parseIntArg(value: string): number {
  const parsed = Number.parseInt(value, 10);
  if (Number.isNaN(parsed)) {
    throw new Error(`expected a number, got "${value}"`);
  }
  return parsed;
}

program.parseAsync(process.argv).catch((error: unknown) => {
  console.error(`\nerror: ${error instanceof Error ? error.message : String(error)}`);
  process.exit(1);
});
