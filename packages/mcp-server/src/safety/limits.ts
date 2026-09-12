/**
 * The agent-facing safety layer: a human isn't watching each call the way
 * they watch the CLI, so this is what stands in for them — a daily cap, and a
 * confirm-before-you-pay gate for anything above a configured threshold.
 *
 * The confirmation store is in-memory and per-process on purpose: a hold is
 * only ever meant to survive the few minutes between one tool call proposing
 * a payment and another confirming it, in the same running server.
 */
import { randomUUID } from "node:crypto";
import { readSpend } from "../vendor/receipt.js";
import type { McpConfig } from "../config.js";

export type OpenSessionAction = {
  kind: "open_session";
  nodeUrl: string;
  seconds: number;
  requireGpu?: boolean;
  localPort?: number;
};

export type TopUpAction = {
  kind: "top_up_session";
  nodeUrl: string;
  sessionId: string;
  token: string;
};

export type PendingAction = OpenSessionAction | TopUpAction;

export type ConfirmationHold = {
  id: string;
  amountTinybars: bigint;
  action: PendingAction;
  expiresAt: number;
};

const CONFIRMATION_TTL_MS = 5 * 60 * 1000;
const holds = new Map<string, ConfirmationHold>();

/** Throws if paying `amountTinybars` now would exceed the configured daily cap. */
export function checkDailyCap(config: McpConfig, amountTinybars: bigint): void {
  if (config.maxTinybarsPerDay === null) return;

  const today = new Date().toISOString().slice(0, 10);
  const spentToday = readSpend()
    .filter((record) => record.paid_at.slice(0, 10) === today)
    .reduce((sum, record) => sum + BigInt(record.amount_tinybars), 0n);

  if (spentToday + amountTinybars > config.maxTinybarsPerDay) {
    throw new Error(
      `daily spending cap reached: already spent ${spentToday} tinybars today, this payment of ` +
        `${amountTinybars} would exceed the cap of ${config.maxTinybarsPerDay}`,
    );
  }
}

export function needsConfirmation(config: McpConfig, amountTinybars: bigint): boolean {
  return config.confirmAboveTinybars !== null && amountTinybars > config.confirmAboveTinybars;
}

export function holdForConfirmation(amountTinybars: bigint, action: PendingAction): ConfirmationHold {
  sweepExpired();
  const hold: ConfirmationHold = {
    id: randomUUID(),
    amountTinybars,
    action,
    expiresAt: Date.now() + CONFIRMATION_TTL_MS,
  };
  holds.set(hold.id, hold);
  return hold;
}

/** Single-use: the hold is consumed whether or not the caller goes on to pay successfully. */
export function takeConfirmedAction(id: string): ConfirmationHold {
  sweepExpired();
  const hold = holds.get(id);
  if (hold === undefined) {
    throw new Error(`no pending confirmation with id "${id}" — it may have expired or already been used`);
  }
  holds.delete(id);
  return hold;
}

export function confirmationPayload(hold: ConfirmationHold): Record<string, unknown> {
  return {
    status: "confirmation_required",
    confirmation_id: hold.id,
    amount_tinybars: hold.amountTinybars.toString(),
    expires_at: new Date(hold.expiresAt).toISOString(),
    message: "This payment is above the configured confirmation threshold. Call confirm_payment with this confirmation_id to actually pay.",
  };
}

function sweepExpired(): void {
  const now = Date.now();
  for (const [id, hold] of holds) {
    if (hold.expiresAt < now) holds.delete(id);
  }
}
