#!/usr/bin/env node
/**
 * Regenerates agent/internal/escrow/testdata/selectors.json from the Solidity
 * sources.
 *
 * The Go agent carries the four function selectors it needs as constants,
 * because computing them would mean linking Keccak-256 into a binary that
 * deliberately has no crypto dependencies. Constants rot, so this pins them:
 * change a contract signature without regenerating, and the Go test fails.
 *
 * Run via `make escrow-selectors`.
 */
import { id } from "ethers";
import { writeFileSync } from "node:fs";
import { resolve, dirname } from "node:path";
import { fileURLToPath } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));

const SIGNATURES = [
    "openSession(bytes32,address,uint256,uint256)",
    "topUp(bytes32,uint256)",
    "getSession(bytes32)",
    "settle(bytes32)",
];

const selectors = Object.fromEntries(
    SIGNATURES.map((signature) => [signature, id(signature).slice(2, 10)]),
);

const out = resolve(here, "../../agent/internal/escrow/testdata/selectors.json");
writeFileSync(out, `${JSON.stringify(selectors, null, 2)}\n`);
console.log(`wrote ${out}`);
for (const [signature, selector] of Object.entries(selectors)) {
    console.log(`  ${selector}  ${signature}`);
}
