"use client";

import { useEffect, useState } from "react";
import type { BrowserSSHKeypair } from "@cleargate/client/browser";
import type { LeaseCreated, NodeListing } from "@cleargate/types";
import { hashscanUrl } from "@cleargate/types";

import { Button } from "@/components/ui/button";

/**
 * What the renter bought, and how to use it.
 *
 * The Jupyter link is the product here. A browser cannot use the SSH
 * certificate — reaching a lease over SSH means `cloudflared access ssh` as a
 * ProxyCommand, which is a local binary — so the certificate and key are
 * offered as downloads for a renter who wants a terminal, and the notebook is
 * what this page actually opens.
 */
export function LeasePanel({
  lease,
  identity,
  node,
  busy,
  error,
  onExtend,
  onStop,
}: {
  lease: LeaseCreated;
  identity: BrowserSSHKeypair | null;
  node: NodeListing;
  busy: string | null;
  error: string | null;
  onExtend: (minutes: number) => Promise<void>;
  onStop: () => Promise<void>;
}) {
  const remaining = useCountdown(lease.expires_at);
  const ended = lease.status === "stopped" || lease.status === "expired" || lease.status === "failed";
  // Frozen, not gone: the container is paused with the renter's work intact,
  // and extending thaws it exactly as it was. Worth its own state in the UI —
  // "your session is over" and "your session is on hold until you pay" call for
  // different actions from the renter.
  const frozen = lease.status === "paused";
  const jupyter = `${lease.jupyter_url}/?token=${lease.jupyter_token}`;
  const offer = node.leases;

  return (
    <div className="border border-foreground/10">
      <div className="flex items-center justify-between border-b border-foreground/10 px-6 py-4">
        <span className="type-label text-muted-foreground">Your session</span>
        <span className="flex items-center gap-2 font-mono text-xs">
          <span
            className={`h-2 w-2 rounded-full ${ended ? "bg-muted-foreground" : "bg-accent"}`}
          />
          {lease.status}
        </span>
      </div>

      <div className="space-y-8 p-6">
        {!ended ? (
          <div>
            <div className="type-label mb-2 text-muted-foreground">
              {frozen ? "Frozen" : "Time left"}
            </div>
            <div className="type-stat font-mono">{frozen ? "00:00" : remaining}</div>
            <p className="mt-2 text-xs text-muted-foreground">
              {frozen
                ? "Your paid time ran out and the container was paused — nothing in it is lost. Buy more time below and it thaws exactly as you left it. It is destroyed if you leave it frozen too long."
                : "At zero the container is frozen rather than killed, so nothing in it is lost — extend and it picks up where it was."}
            </p>
          </div>
        ) : null}

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
        ) : (
          <p className="text-sm text-muted-foreground">
            This session is over. The container is gone, the tunnel is withdrawn and the workspace
            was wiped. Time already bought is not refunded — forward payment is what removes the
            need for escrow on this path — but nothing further will be charged.
          </p>
        )}

        {!ended ? (
          <div>
            <div className="type-label mb-3 text-muted-foreground">Buy more time</div>
            <div className="flex flex-wrap gap-2">
              {[15, 30, 60]
                .filter(
                  (value) =>
                    offer === undefined ||
                    (value >= offer.min_minutes && value <= offer.max_minutes),
                )
                .map((value) => (
                  <Button
                    key={value}
                    variant="outline"
                    disabled={busy !== null}
                    onClick={() => void onExtend(value)}
                  >
                    +{value}m
                  </Button>
                ))}
              <Button variant="quiet" disabled={busy !== null} onClick={() => void onStop()}>
                Stop now
              </Button>
            </div>
            <p className="mt-3 text-xs text-muted-foreground">
              Each extension is its own x402 payment and issues a fresh certificate with the later
              expiry. Stopping ends the meter immediately.
            </p>
          </div>
        ) : null}

        <details className="border-t border-foreground/10 pt-5">
          <summary className="type-label cursor-pointer text-muted-foreground">
            SSH access and receipt
          </summary>

          <div className="mt-4 space-y-4 text-sm">
            <Field label="Paid">
              <a
                className="font-mono underline underline-offset-4 hover:no-underline"
                href={hashscanUrl(lease.transaction, node.network)}
                target="_blank"
                rel="noreferrer"
              >
                {lease.transaction}
              </a>
              <span className="mt-1 block font-mono text-xs text-muted-foreground">
                {lease.amount_tinybars} tinybars for {lease.minutes} minutes
              </span>
            </Field>

            <Field label="Lease">
              <span className="font-mono text-xs break-all">{lease.lease_id}</span>
            </Field>

            {lease.ssh_host !== undefined && lease.ssh_host !== "" && identity !== null ? (
              <Field label="SSH">
                <p className="mb-2 text-xs text-muted-foreground">
                  Download both files, then connect. This needs <code>cloudflared</code> installed
                  locally — a browser cannot drive it.
                </p>
                <div className="mb-2 flex gap-2">
                  <Download name="cleargate_lease" body={identity.privateKeyPem} label="Private key" />
                  <Download
                    name="cleargate_lease-cert.pub"
                    body={`${lease.certificate}\n`}
                    label="Certificate"
                  />
                </div>
                <pre className="overflow-x-auto bg-foreground/[0.03] p-3 font-mono text-xs">
                  {`ssh -i cleargate_lease \\
  -o ProxyCommand="cloudflared access ssh --hostname ${lease.ssh_host}" \\
  ${lease.ssh_user}@${lease.ssh_host}`}
                </pre>
              </Field>
            ) : (
              <Field label="SSH">
                <span className="text-xs text-muted-foreground">
                  Not available: this node publishes over a tunnel that carries HTTP only, so
                  Jupyter works and SSH does not. The certificate was still issued and scoped to
                  this lease.
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
 * Counts down to the lease's expiry.
 *
 * Purely a display of `expires_at`, which the node is the authority on. It does
 * not drive anything: the node's own 15-second sweep is what freezes and reaps,
 * and a page that had drifted would otherwise quietly disagree with it.
 */
function useCountdown(expiresAt: string): string {
  const [now, setNow] = useState(() => Date.now());

  useEffect(() => {
    const timer = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(timer);
  }, []);

  const left = new Date(expiresAt).getTime() - now;
  if (Number.isNaN(left)) return "—";
  if (left <= 0) return "00:00";

  const total = Math.floor(left / 1000);
  const hours = Math.floor(total / 3600);
  const minutes = Math.floor((total % 3600) / 60);
  const seconds = total % 60;
  const pad = (value: number) => String(value).padStart(2, "0");

  return hours > 0 ? `${hours}:${pad(minutes)}:${pad(seconds)}` : `${pad(minutes)}:${pad(seconds)}`;
}
