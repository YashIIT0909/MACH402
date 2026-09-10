/**
 * `make smoke` — the whole payment spike in one command, and the CI regression
 * test that keeps facilitator changes from surfacing as a failed demo.
 *
 * Boots the reference server in-process, pays it, asserts a real transaction id
 * came back, shuts down. Pass a URL to point it at an already-running resource
 * server instead (this is how the Go agent gets cross-verified in stage 5).
 */
import { createServer, type Server } from "node:http";
import { fetchHederaKind } from "./supported.js";
import { buildApp, SMOKE_PRICE_TINYBARS } from "./server.js";
import { paySmokeResource } from "./client.js";
import { FACILITATOR_URL, SMOKE_PORT, payToAccountId } from "./env.js";
import { formatTinybars } from "@cleargate/types";

function step(n: number, title: string): void {
  console.log(`\n[${n}/3] ${title}`);
}

async function listen(server: Server, port: number): Promise<void> {
  await new Promise<void>((resolve, reject) => {
    server.once("error", reject);
    server.listen(port, resolve);
  });
}

async function close(server: Server): Promise<void> {
  await new Promise<void>((resolve) => server.close(() => resolve()));
}

/**
 * The job spec used when cross-verifying against a live ClearGate agent. It has
 * to be an image on the agent's default allowlist.
 */
const AGENT_JOB_SPEC = {
  image: "python:3.11-slim",
  cmd: ["python", "-c", "print('paid by the official x402 client')"],
  timeout_seconds: 60,
};

async function main(): Promise<void> {
  const externalUrl = process.argv[2];
  // A ClearGate agent sells jobs over POST; the reference server sells a GET.
  const body = externalUrl?.endsWith("/v1/jobs") === true ? AGENT_JOB_SPEC : undefined;

  step(1, "facilitator /supported");
  const kind = await fetchHederaKind();
  console.log(`  ${FACILITATOR_URL} advertises exact/${kind.network}, feePayer ${kind.feePayer}`);

  let server: Server | null = null;
  let url: string;

  if (externalUrl === undefined) {
    step(2, "reference resource server");
    const payTo = payToAccountId();
    server = createServer(buildApp(payTo));
    await listen(server, SMOKE_PORT);
    url = `http://localhost:${SMOKE_PORT}/paid-hello`;
    console.log(`  listening on ${SMOKE_PORT}, ${formatTinybars(SMOKE_PRICE_TINYBARS)} to ${payTo}`);
  } else {
    step(2, "external resource server");
    url = externalUrl;
    console.log(`  ${url}`);
  }

  try {
    step(3, "pay and settle");
    const settlement = await paySmokeResource(url, body);
    console.log(`\nSMOKE PASS  ${settlement.transaction}`);
  } finally {
    if (server !== null) {
      await close(server);
    }
  }
}

main().catch((error: unknown) => {
  console.error(`\nSMOKE FAIL  ${error instanceof Error ? error.message : String(error)}`);
  process.exit(1);
});
