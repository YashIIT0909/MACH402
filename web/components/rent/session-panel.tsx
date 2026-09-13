"use client";

import { useEffect, useState } from "react";
import type { NodeListing } from "@cleargate/types";
import { hashscanUrl, isSessionTerminal } from "@cleargate/types";

import { Button } from "@/components/ui/button";
import { hbar, hbarShort } from "@/lib/registry";
import type { StoredSession } from "@/lib/session-store";

/**
 * The purchase plus whatever the polling loop in `SessionFlow` has learned
 * since. `SessionCreated` is what the 402 cycle handed back once; `SessionState`
 * is what the node reports on every poll, and this is the two glued together so
 * the panel never has to ask "which one am I looking at".
 *
 * It is the stored shape by definition: what survives a reload and what this
 * panel renders have to be the same thing, or a restored session would be
 * missing fields the panel assumes.
 */
export type SessionView = StoredSession;

/**
 * What the renter bought, and how to use it.
 *
 * `credit_tinybars` is the authoritative number: what the node owes back *right
 * now* if the renter stops this instant, and the same number it publishes to
 * its own HCS topic every fifteen seconds. The countdown beside it is derived
 * from `expires_at`, which the node keeps pinned to what that credit buys.
 *
 * Both are shown, and the balance keeps the emphasis, because a timer alone
 * implies the thing left when it hits zero is nothing — here the thing left is
 * a refund.
 */
export function SessionPanel({
  session,
  node,
  busy,
  error,
  onTopUp,
  onStop,
  onDismiss,
}: {
  session: SessionView;
  node: NodeListing;
  busy: string | null;
  error: string | null;
  onTopUp: () => Promise<void>;
  onStop: () => Promise<void>;
  /** Forget this session and go back to buying — only offered once it has ended. */
  onDismiss: () => void;
}) {
  const ended = isSessionTerminal(session.status);
  const frozen = session.status === "paused";
  const jupyter = `${session.jupyter_url}/?token=${session.jupyter_token}`;
  const settled = session.settle_state === "done";
  const remaining = useCountdown(session.expires_at);
  const elapsed = useElapsed(session.created_at, ended);

  return (
    <div className="border border-foreground/10">
      <div className="flex items-center justify-between border-b border-foreground/10 px-6 py-4">
        <span className="type-label text-muted-foreground">Your session</span>
        <span className="flex items-center gap-2 font-mono text-xs">
          <span
            className={`h-2 w-2 rounded-full ${ended ? "bg-muted-foreground" : "bg-accent"}`}
          />
          {session.status}
        </span>
      </div>

      <div className="space-y-8 p-6">
        {!ended ? (
          <div>
            {/*
             * Time and money, together.
             *
             * The credit is the authoritative number — it is what the node
             * publishes to its topic and what it owes back — but "0.04 HBAR
             * left" answers a question nobody asked; a renter mid-run wants to
             * know how long they have. Neither alone is the honest answer, so
             * both are the headline and the conversion between them (the price
             * per second) sits underneath.
             */}
            {/*
             * `sm:grid-cols-2`, not `grid-cols-2`: at display size these two
             * numbers do not fit side by side in a phone-width panel, and a
             * balance that runs off its own edge is worse than one that stacks.
             * The unit sits on its own line at label size for the same reason —
             * "HBAR" set at 66px is three quarters of the width and none of the
             * information.
             */}
            <div className="grid grid-cols-1 gap-6 sm:grid-cols-2">
              <Stat
                label={frozen ? "Frozen" : "Time remaining"}
                value={frozen ? "0:00" : remaining}
              />
              <Stat
                label="Credit left"
                value={frozen ? "0" : hbarShort(session.credit_tinybars)}
                unit="HBAR"
                title={`${hbar(session.credit_tinybars)} HBAR`}
              />
            </div>

            <p className="mt-3 text-xs text-muted-foreground">
              {frozen
                ? "Your credit ran out and the container was paused — nothing in it is lost. A top-up thaws it exactly as you left it. It is destroyed if left frozen too long."
                : "That credit is a refund owed to you, not time you have already spent — stop any time and get it back."}
            </p>

            {session.low_credits === true && session.fully_paid !== true && !frozen ? (
              <p className="mt-2 text-xs text-accent">
                Running low — the next top-up should fire automatically. If your wallet is prompting,
                that is why.
              </p>
            ) : null}

            <dl className="mt-5 grid grid-cols-2 gap-x-6 gap-y-3 border-t border-foreground/10 pt-5">
              <Detail
                label="Session length"
                value={session.session_seconds !== undefined && session.session_seconds > 0
                  ? `${Math.round(session.session_seconds / 60)} min`
                  : "—"}
              />
              <Detail
                label="Paid for"
                value={
                  session.paid_seconds !== undefined ? clock(session.paid_seconds) : "—"
                }
                note={session.fully_paid === true ? "all of it — no more top-ups" : "topping up as it burns"}
              />
              <Detail label="Used so far" value={elapsed} />
              <Detail
                label="Spent so far"
                value={
                  session.burned_tinybars !== undefined
                    ? `${hbarShort(session.burned_tinybars)} HBAR`
                    : "—"
                }
                title={
                  session.burned_tinybars !== undefined
                    ? `${hbar(session.burned_tinybars)} HBAR`
                    : undefined
                }
              />
              <Detail
                label="Rate"
                value={`${hbarShort(session.price_tinybars_per_second, 6)} HBAR/s`}
                title={`${hbar(session.price_tinybars_per_second)} HBAR/s`}
              />
              <Detail label="Runs out" value={time(session.expires_at)} />
            </dl>
          </div>
        ) : (
          <div>
            <Stat
              label={settled ? "Refunded" : session.ended_remotely === true ? "Owed at the last check" : "Owed to you"}
              value={hbarShort(settled ? (session.refunded_tinybars ?? "0") : session.credit_tinybars)}
              unit="HBAR"
              title={`${hbar(settled ? (session.refunded_tinybars ?? "0") : session.credit_tinybars)} HBAR`}
            />
            <p className="mt-2 text-xs text-muted-foreground">
              {settled
                ? "Paid back to your account. Burned and refunded together add up to every payment you made."
                : session.ended_remotely === true
                  ? "The node has finished this session and no longer answers for it, which is what a " +
                    "session that ended looks like — it releases the slot for the next renter. This is " +
                    "the last figure it gave, not a live one: the refund it settled is in your account " +
                    "and on the audit topic below."
                  : "This is what the node owes you, but it has not paid it out yet. It is on the node's " +
                    "own audit topic below, published before you ever stopped — that record is what you " +
                    "have if it does not pay."}
            </p>
          </div>
        )}

        {!ended && !frozen ? (
          <div>
            <div className="type-label mb-3 text-muted-foreground">Open your notebook</div>
            <Button asChild size="lg" variant="accent" className="w-full">
              <a href={jupyter} target="_blank" rel="noreferrer">
                Open Jupyter
              </a>
            </Button>
            <p className="mt-3 text-xs text-muted-foreground">
              Use <strong className="text-foreground">New → Terminal</strong> inside Jupyter for a
              shell. <code className="font-mono">pip install</code> works — the container reaches
              package registries through the node&apos;s deny-by-default proxy and nothing else.
            </p>
            <p className="mt-2 font-mono text-xs break-all text-muted-foreground">{jupyter}</p>
          </div>
        ) : null}

        {!ended ? (
          <div>
            <div className="type-label mb-3 text-muted-foreground">Manage</div>
            <div className="flex flex-wrap gap-2">
              {session.fully_paid === true ? null : (
                <Button variant="outline" disabled={busy !== null} onClick={() => void onTopUp()}>
                  Top up now
                </Button>
              )}
              <Button variant="quiet" disabled={busy !== null} onClick={() => void onStop()}>
                Stop &amp; get refund
              </Button>
            </div>
            <p className="mt-3 text-xs text-muted-foreground">
              Stopping burns the final seconds and returns the rest immediately — this is a real
              payment back to your account, not a credit that expires unused.
            </p>
          </div>
        ) : null}

        {ended ? (
          <div>
            <Button variant="outline" className="w-full" onClick={onDismiss}>
              Rent this node again
            </Button>
            <p className="mt-2 text-xs text-muted-foreground">
              Clears this session from the browser and goes back to buying. The receipts below stay
              on Hedera either way.
            </p>
          </div>
        ) : null}

        <details className="border-t border-foreground/10 pt-5">
          <summary className="type-label cursor-pointer text-muted-foreground">
            SSH access, receipts and the audit trail
          </summary>

          <div className="mt-4 space-y-4 text-sm">
            <Field label="Paid so far">
              <a
                className="font-mono underline underline-offset-4 hover:no-underline"
                href={hashscanUrl(session.transaction, node.network)}
                target="_blank"
                rel="noreferrer"
              >
                {session.transaction}
              </a>
              <span className="mt-1 block font-mono text-xs text-muted-foreground">
                opening payment: {session.amount_tinybars} tinybars
                {session.burned_tinybars !== undefined
                  ? ` · burned so far: ${session.burned_tinybars} tinybars`
                  : ""}
              </span>
            </Field>

            <Field label="Session">
              <span className="font-mono text-xs break-all">{session.session_id}</span>
            </Field>

            {node.audit_topic !== undefined && node.audit_topic !== "" ? (
              <Field label="Refund-owed trail">
                <p className="mb-1 text-xs text-muted-foreground">
                  The node publishes what it owes you here every 15 seconds this session runs — a
                  record it cannot edit after the fact, independent of anything this page says.
                </p>
                <a
                  className="font-mono text-xs break-all underline underline-offset-4 hover:no-underline"
                  href={`https://hashscan.io/testnet/topic/${node.audit_topic}`}
                  target="_blank"
                  rel="noreferrer"
                >
                  {node.audit_topic}
                </a>
              </Field>
            ) : null}

            <Field label="SSH">
              <span className="text-xs text-muted-foreground">
                Not available: every session is published through a quick tunnel, which carries HTTP
                only — use the terminal inside Jupyter.
              </span>
            </Field>
          </div>
        </details>

        {busy !== null ? <p className="text-sm text-muted-foreground">{busy}</p> : null}
        {error !== null ? (
          <p className="font-mono text-xs break-words text-destructive">{error}</p>
        ) : null}
      </div>
    </div>
  );
}

/**
 * One display-size figure.
 *
 * The unit is a separate line at label size rather than part of the number:
 * it is the same three characters on every stat, it never changes, and set at
 * display size it is what pushes the figure off the edge of the panel.
 */
function Stat({
  label,
  value,
  unit,
  title,
}: {
  label: string;
  value: string;
  unit?: string;
  title?: string;
}) {
  return (
    <div className="min-w-0">
      {label !== "" ? <div className="type-label mb-2 text-muted-foreground">{label}</div> : null}
      <div className="flex items-baseline gap-2" title={title}>
        <span className="type-stat truncate font-mono tabular-nums">{value}</span>
        {unit !== undefined ? (
          <span className="type-label shrink-0 text-muted-foreground">{unit}</span>
        ) : null}
      </div>
    </div>
  );
}

function Detail({
  label,
  value,
  note,
  title,
}: {
  label: string;
  value: string;
  note?: string;
  title?: string;
}) {
  return (
    <div className="min-w-0">
      <dt className="type-label text-muted-foreground">{label}</dt>
      <dd className="mt-1 truncate font-mono text-sm tabular-nums" title={title}>
        {value}
      </dd>
      {note !== undefined ? (
        <dd className="mt-0.5 text-xs text-muted-foreground">{note}</dd>
      ) : null}
    </div>
  );
}

/** h:mm:ss / m:ss for a count of seconds. */
function clock(seconds: number): string {
  if (!Number.isFinite(seconds) || seconds <= 0) return "0:00";
  const total = Math.floor(seconds);
  const hours = Math.floor(total / 3600);
  const minutes = Math.floor((total % 3600) / 60);
  const rest = total % 60;
  const pad = (value: number) => String(value).padStart(2, "0");
  return hours > 0 ? `${hours}:${pad(minutes)}:${pad(rest)}` : `${minutes}:${pad(rest)}`;
}

/** Wall-clock time of day, for "runs out" — a date nobody can read helps nobody. */
function time(iso: string): string {
  const at = new Date(iso);
  if (Number.isNaN(at.getTime())) return "—";
  return at.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit", second: "2-digit" });
}

/**
 * How long the session has been open.
 *
 * Wall-clock since it was created, which is not the same as time billed — a
 * frozen session does not burn. It is here because a renter asking "how long
 * have I been at this" is asking about the wall clock; what they were charged
 * for is the burn figure beside it.
 */
function useElapsed(createdAt: string | undefined, stopped: boolean): string {
  const [now, setNow] = useState(() => Date.now());

  useEffect(() => {
    if (stopped) return;
    const timer = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(timer);
  }, [stopped]);

  if (createdAt === undefined) return "—";
  const started = new Date(createdAt).getTime();
  if (Number.isNaN(started)) return "—";
  return clock((now - started) / 1000);
}

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div>
      <div className="type-label mb-1 text-muted-foreground">{label}</div>
      {children}
    </div>
  );
}

/**
 * Roughly how long the current credit buys at the price the session opened at.
 *
 * Cosmetic only — the node's own meter is the authority on when a session
 * freezes. The "true" number is `credit_tinybars`; this converts it into
 * something a human reads faster than a tinybar count.
 */
function useCountdown(expiresAt: string): string {
  const [now, setNow] = useState(() => Date.now());

  useEffect(() => {
    const timer = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(timer);
  }, []);

  const left = new Date(expiresAt).getTime() - now;
  if (Number.isNaN(left)) return "—";
  if (left <= 0) return "0:00";

  const total = Math.floor(left / 1000);
  const hours = Math.floor(total / 3600);
  const minutes = Math.floor((total % 3600) / 60);
  const seconds = total % 60;
  const pad = (value: number) => String(value).padStart(2, "0");

  return hours > 0 ? `${hours}:${pad(minutes)}:${pad(seconds)}` : `${minutes}:${pad(seconds)}`;
}
