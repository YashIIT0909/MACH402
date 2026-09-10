/**
 * Renter-side receipt mirror.
 *
 * The node keeps its own append-only log of what it earned; this is the paying
 * side of the same record, so a renter can reconcile spend without trusting the
 * node or any website.
 */
import { appendFileSync, mkdirSync, readFileSync } from "node:fs";
import { homedir } from "node:os";
import { dirname, join } from "node:path";
import type { SettleResponse } from "@cleargate/types";

export type SpendRecord = {
  node_url: string;
  job_id: string | null;
  transaction: string;
  payer: string;
  amount_tinybars: string;
  network: string;
  paid_at: string;
};

export function receiptsPath(): string {
  return process.env["CLEARGATE_RECEIPTS"] ?? join(homedir(), ".cleargate", "receipts.jsonl");
}

/** Appends one spend record. Never throws: losing the mirror must not fail a job. */
export function recordSpend(
  settlement: SettleResponse,
  nodeUrl: string,
  jobId: string | null,
  amountTinybars: string,
): void {
  const record: SpendRecord = {
    node_url: nodeUrl,
    job_id: jobId,
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
