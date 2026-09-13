/**
 * This package's own append-only spend mirror, vendored from
 * client/src/receipt.ts and pointed at its own file (`mach402_MCP_RECEIPTS`)
 * so an agent's spend never gets invisibly mixed into a human renter's
 * `~/.mach402/receipts.jsonl`. This is also the daily-cap check's source of
 * truth — see safety/limits.ts.
 */
import { appendFileSync, mkdirSync, readFileSync } from "node:fs";
import { homedir } from "node:os";
import { dirname, join } from "node:path";
import type { SettleResponse } from "@x402/core/types";

export type SpendRecord = {
  node_url: string;
  session_id: string | null;
  transaction: string;
  payer: string;
  amount_tinybars: string;
  network: string;
  paid_at: string;
};

export function receiptsPath(): string {
  return process.env["mach402_MCP_RECEIPTS"] ?? join(homedir(), ".mach402", "mcp-receipts.jsonl");
}

/** Appends one spend record. Never throws: losing the mirror must not fail a payment. */
export function recordSpend(
  settlement: SettleResponse,
  nodeUrl: string,
  sessionId: string | null,
  amountTinybars: string,
): void {
  const record: SpendRecord = {
    node_url: nodeUrl,
    session_id: sessionId,
    transaction: settlement.transaction,
    payer: settlement.payer ?? "",
    amount_tinybars: amountTinybars,
    network: settlement.network,
    paid_at: new Date().toISOString(),
  };

  try {
    const path = receiptsPath();
    mkdirSync(dirname(path), { recursive: true });
    appendFileSync(path, `${JSON.stringify(record)}\n`, { mode: 0o600 });
  } catch {
    // Best effort. The authoritative record is on chain.
  }
}

export function readSpend(): SpendRecord[] {
  try {
    return readFileSync(receiptsPath(), "utf8")
      .split("\n")
      .filter((line) => line.trim() !== "")
      .flatMap((line) => {
        try {
          return [JSON.parse(line) as SpendRecord];
        } catch {
          return [];
        }
      });
  } catch {
    return [];
  }
}
