#!/usr/bin/env -S node --experimental-strip-types
/**
 * cleargate — rent GPU time from a ClearGate node and pay for it over x402.
 *
 *   cleargate quote --node <url>
 *   cleargate run   --node <url> --image <img> [--script train.py]
 *   cleargate logs  --node <url> --job <id> --token <token>
 *   cleargate stop  --node <url> --job <id> --token <token>
 *   cleargate spend
 */
import { readFileSync, writeFileSync } from "node:fs";
import { basename, resolve } from "node:path";
import { Command } from "commander";
import type { JobSpec } from "@cleargate/types";
import { formatTinybars, hashscanUrl } from "@cleargate/types";
import { createPayer } from "./pay.js";
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
