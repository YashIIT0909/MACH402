/**
 * Step 1 of the payment spike: ask the facilitator what it supports.
 *
 * `extra.feePayer` is read from here and never hardcoded — if it does not match
 * the facilitator's advertised Hedera signer, the client SDK throws before
 * signing (CLAUDE.md invariant 5). Running this first turns a facilitator change
 * into a clear failure here rather than a mysterious one mid-demo.
 */
import type { SupportedResponse } from "@cleargate/types";
import { HEDERA_TESTNET } from "@cleargate/types";
import { FACILITATOR_URL } from "./env.js";

export type HederaKind = {
  x402Version: number;
  scheme: string;
  network: string;
  feePayer: string;
};

export async function fetchHederaKind(
  facilitatorUrl = FACILITATOR_URL,
  network: string = HEDERA_TESTNET,
): Promise<HederaKind> {
  const url = `${facilitatorUrl.replace(/\/$/, "")}/supported`;
  const response = await fetch(url);
  if (!response.ok) {
    throw new Error(`GET ${url} returned ${response.status} ${response.statusText}`);
  }

  const body = (await response.json()) as SupportedResponse;
  const kind = body.kinds.find((k) => k.network === network && k.scheme === "exact");
  if (!kind) {
    const seen = body.kinds.map((k) => `${k.scheme}/${k.network}`).join(", ");
    throw new Error(`facilitator ${facilitatorUrl} does not advertise exact/${network}. Saw: ${seen}`);
  }

  const feePayer = kind.extra?.["feePayer"];
  if (typeof feePayer !== "string" || feePayer === "") {
    throw new Error(`exact/${network} kind has no extra.feePayer; cannot build a challenge`);
  }

  return { x402Version: kind.x402Version, scheme: kind.scheme, network: kind.network, feePayer };
}

async function main(): Promise<void> {
  const kind = await fetchHederaKind();
  console.log(`facilitator     ${FACILITATOR_URL}`);
  console.log(`network         ${kind.network}`);
  console.log(`scheme          ${kind.scheme} (x402 v${kind.x402Version})`);
  console.log(`extra.feePayer  ${kind.feePayer}`);
}

if (import.meta.url === `file://${process.argv[1]}`) {
  main().catch((error: unknown) => {
    console.error(`supported check failed: ${error instanceof Error ? error.message : String(error)}`);
    process.exit(1);
  });
}
