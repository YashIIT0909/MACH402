"use client";

import { useEffect, useState } from "react";
import type { BrowserSSHKeypair } from "@cleargate/client/browser";
import type { NodeListing, SessionCreated, SessionState } from "@cleargate/types";
import { hashscanUrl, isSessionTerminal } from "@cleargate/types";

import { Button } from "@/components/ui/button";
import { hbar } from "@/lib/registry";

/**
 * The purchase plus whatever the polling loop in `SessionFlow` has learned
 * since. `SessionCreated` is what the 402 cycle handed back once; `SessionState`
 * is what the node reports on every poll, and this is the two glued together so
 * the panel never has to ask "which one am I looking at".
 */
export type SessionView = SessionCreated & Partial<SessionState>;

/**
 * What the renter bought, and how to use it — the metered twin of `LeasePanel`.
 *
 * The number that matters here is `credit_tinybars`, not a countdown: it is
 * what the node owes back *right now* if the renter stops this instant, and it
 * is the same number the node is publishing to its own HCS topic every fifteen
 * seconds. Showing it as a running balance rather than a timer is deliberate —
 * a timer implies the thing left when it hits zero is nothing, and here the
 * thing left is a refund.
 */
export function SessionPanel({
  session,
  identity,
  node,
  busy,
  error,
  onTopUp,
  onStop,
}: {
  session: SessionView;
  identity: BrowserSSHKeypair | null;
  node: NodeListing;
  busy: string | null;
  error: string | null;
  onTopUp: () => Promise<void>;
  onStop: () => Promise<void>;
}) {
  const ended = isSessionTerminal(session.status);
  const frozen = session.status === "paused";
  const jupyter = `${session.jupyter_url}/?token=${session.jupyter_token}`;
  const settled = session.settle_state === "done";
  const remaining = useCountdown(session.expires_at);

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
            <div className="type-label mb-2 text-muted-foreground">
              {frozen ? "Frozen" : "Credit remaining"}
            </div>
            <div className="type-stat font-mono">
              {frozen ? "0 HBAR" : `${hbar(session.credit_tinybars)} HBAR`}
            </div>
            <p className="mt-2 text-xs text-muted-foreground">
              {frozen
                ? "Your credit ran out and the container was paused — nothing in it is lost. A top-up thaws it exactly as you left it. It is destroyed if left frozen too long."
                : `≈${remaining} left at the current rate. Unlike a lease this is a refund owed to you, not time you have already spent — stop any time and get it back.`}
            </p>
            {session.low_credits === true && !frozen ? (
              <p className="mt-2 text-xs text-accent">
                Running low — the next top-up should fire automatically. If your wallet is prompting,
                that is why.
              </p>
            ) : null}
          </div>
        ) : (
          <div>
            <div className="type-label mb-2 text-muted-foreground">
              {settled ? "Refunded" : "Owed to you"}
            </div>
            <div className="type-stat font-mono">
              {hbar(settled ? (session.refunded_tinybars ?? "0") : session.credit_tinybars)} HBAR
            </div>
            <p className="mt-2 text-xs text-muted-foreground">
              {settled
                ? "Paid back to your account. Burned and refunded together add up to every payment you made."
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
              <Button variant="outline" disabled={busy !== null} onClick={() => void onTopUp()}>
                Top up now
              </Button>
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

            {session.ssh_host !== undefined && session.ssh_host !== "" && identity !== null ? (
              <Field label="SSH">
                <p className="mb-2 text-xs text-muted-foreground">
                  Download both files, then connect. This needs <code>cloudflared</code> installed
                  locally — a browser cannot drive it.
                </p>
                <div className="mb-2 flex gap-2">
                  <Download name="cleargate_session" body={identity.privateKeyPem} label="Private key" />
                  <Download
                    name="cleargate_session-cert.pub"
                    body={`${session.certificate}\n`}
                    label="Certificate"
                  />
                </div>
                <pre className="overflow-x-auto bg-foreground/[0.03] p-3 font-mono text-xs">
                  {`ssh -i cleargate_session \\
  -o ProxyCommand="cloudflared access ssh --hostname ${session.ssh_host}" \\
  ${session.ssh_user}@${session.ssh_host}`}
                </pre>
              </Field>
            ) : (
              <Field label="SSH">
                <span className="text-xs text-muted-foreground">
                  Not available: this node publishes over a tunnel that carries HTTP only, so
                  Jupyter works and SSH does not. The certificate was still issued and scoped to
                  this session.
                </span>
              </Field>
            )}
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

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div>
      <div className="type-label mb-1 text-muted-foreground">{label}</div>
      {children}
    </div>
  );
}

/**
 * A download without a server round trip.
 *
 * The private key never leaves the browser, so writing it to a blob is the only
 * honest way to hand it over — posting it anywhere to get a download link would
 * defeat the point of generating it here.
 */
function Download({ name, body, label }: { name: string; body: string; label: string }) {
  const [href, setHref] = useState<string | null>(null);

  useEffect(() => {
    const url = URL.createObjectURL(new Blob([body], { type: "application/octet-stream" }));
    setHref(url);
    return () => URL.revokeObjectURL(url);
  }, [body]);

  if (href === null) return null;

  return (
    <Button asChild variant="outline" size="sm">
      <a href={href} download={name}>
        {label}
      </a>
    </Button>
  );
}

/**
 * Roughly how long the current credit buys at the price the session opened at.
 *
 * Cosmetic only — the node's own meter is the authority on when a session
 * freezes, exactly as `LeasePanel`'s countdown does not drive the lease sweep.
 * This one is even more clearly a display detail than that one: the "true"
 * number is `credit_tinybars`, and this converts it into something a human
 * reads faster than a tinybar count.
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
