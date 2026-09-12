/**
 * Append-only audit log of every tool call this server makes on an agent's
 * behalf, separate from the spend ledger (vendor/receipt.ts) which only
 * records what was actually paid. This is the trail a human reviews to see
 * what their agent has been doing, whether or not each call spent anything.
 */
import { appendFileSync, mkdirSync } from "node:fs";
import { dirname, join } from "node:path";
import { homedir } from "node:os";

export type AuditEntry = {
  tool: string;
  node_url?: string;
  session_id?: string | null;
  amount_tinybars?: string;
  outcome: string;
  at: string;
};

export function auditLogPath(): string {
  return process.env["CLEARGATE_MCP_AUDIT_LOG"] ?? join(homedir(), ".cleargate", "mcp-audit.jsonl");
}

/** Never throws: losing the audit trail must not block a call the caller already approved. */
export function logAudit(entry: Omit<AuditEntry, "at">): void {
  const record: AuditEntry = { ...entry, at: new Date().toISOString() };
  try {
    const path = auditLogPath();
    mkdirSync(dirname(path), { recursive: true });
    appendFileSync(path, `${JSON.stringify(record)}\n`, { mode: 0o600 });
  } catch {
    // Best effort.
  }
}
