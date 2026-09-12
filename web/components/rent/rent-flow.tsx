"use client";

import { useCallback, useEffect, useMemo, useState } from "react";
import type { LeaseCreated, LeaseState, NodeListing } from "@cleargate/types";
import {
  createWalletPayer,
  generateSSHKeypair,
  payFor,
  PaymentError,
  type BrowserSSHKeypair,
  type SubtleCryptoLike,
} from "@cleargate/client/browser";

import { Button } from "@/components/ui/button";
import { useWallet } from "@/components/wallet/wallet-provider";
import { hbar, totalTinybars } from "@/lib/registry";
import { ConnectWallet } from "./connect-wallet";
import { LeasePanel } from "./lease-panel";

/**
 * Buying minutes on one node, from the browser.
 *
 * The order here is the node's order, not a UI convenience: the node validates
 * the spec, answers 402, verifies, starts the container, signs the certificate,
 * points the tunnel, *proves it is reachable*, and only then settles. A renter
 * is never charged for a lease that never came up — so a failure in the middle
 * of this shows up as an error with nothing spent, and that is worth saying in
 * the UI rather than leaving people to wonder.
 */
export function RentFlow({ node }: { node: NodeListing }) {
  const offer = node.leases;
  const { state: wallet } = useWallet();

  const [minutes, setMinutes] = useState(offer?.min_minutes ?? 15);
  const [requireGpu, setRequireGpu] = useState(false);
  const [busy, setBusy] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [lease, setLease] = useState<LeaseCreated | null>(null);
  const [identity, setIdentity] = useState<BrowserSSHKeypair | null>(null);

  const total = useMemo(
    () => (offer === undefined ? "0" : totalTinybars(offer.price_tinybars_per_minute, minutes)),
    [offer, minutes],
  );

  /**
   * A paying fetch bound to the connected wallet.
   *
   * The renter's per-payment cap is set from what this page is actually asking
   * for, with headroom. It is the renter's own guard rail against a node that
   * quotes one price in its heartbeat and a different one in the 402 — the
   * registry listing is not authoritative, the challenge is.
   */
  const payer = useMemo(() => {
    if (wallet.status !== "connected") return null;
    return createWalletPayer(wallet.signer, {
      network: node.network,
      maxTinybarsPerPayment: BigInt(total) * 2n,
    });
  }, [wallet, node.network, total]);

  const rent = useCallback(async () => {
    if (payer === null || offer === undefined) return;

    setError(null);
    setBusy("Generating a session key…");

    try {
      // The node signs this into a certificate scoped to one lease and never
      // sees the private half. Generated per lease, not reused.
      const keypair = await generateSSHKeypair(
        window.crypto.subtle as unknown as SubtleCryptoLike,
      );
      setIdentity(keypair);

      // Asked before the payment, purely so the two failures are told apart. If
      // this succeeds, the browser can reach the node and CORS is fine, and any
      // later error is about the payment rather than the connection.
      setBusy("Checking the node answers…");
      try {
        const probe = await fetch(`${node.public_url}/health`);
        if (!probe.ok) {
          throw new Error(`the node answered ${probe.status} on /health`);
        }
      } catch (unreachable) {
        throw new Error(
          `The browser could not reach ${node.public_url}. The node's own logs may show nothing ` +
            `at all, because the request never arrived — check the Network tab for the real ` +
            `reason. (${unreachable instanceof Error ? unreachable.message : String(unreachable)})`,
        );
      }

      setBusy("Waiting for your wallet, then for the node to come up…");

      const { response } = await payFor(payer, `${node.public_url}/v1/leases`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          minutes,
          public_key: keypair.publicKey,
          require_gpu: requireGpu,
        }),
      });

      setLease((await response.json()) as LeaseCreated);
    } catch (caught) {
      setError(describe(caught));
    } finally {
      setBusy(null);
    }
  }, [payer, offer, node.public_url, minutes, requireGpu]);

  const extend = useCallback(
    async (extraMinutes: number) => {
      if (payer === null || lease === null || identity === null) return;

      setError(null);
      setBusy("Buying more time…");
      try {
        const { response } = await payFor(
          payer,
          `${node.public_url}/v1/leases/${lease.lease_id}/extend`,
          {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify({ minutes: extraMinutes, public_key: identity.publicKey }),
          },
        );
        const extended = (await response.json()) as LeaseCreated;
        // An extension issues a fresh certificate and a later expiry but no new
        // token, so the original one is carried forward rather than blanked.
        setLease({ ...extended, token: extended.token ?? lease.token });
      } catch (caught) {
        setError(describe(caught));
      } finally {
        setBusy(null);
      }
    },
    [payer, lease, identity, node.public_url],
  );

  const stop = useCallback(async () => {
    if (lease === null || lease.token === undefined) return;

    setError(null);
    setBusy("Ending the lease…");
    try {
      const response = await fetch(`${node.public_url}/v1/leases/${lease.lease_id}/stop`, {
        method: "POST",
        headers: { Authorization: `Bearer ${lease.token}` },
      });
      if (!response.ok) {
        throw new Error(`the node answered ${response.status} ${response.statusText}`);
      }
      const state = (await response.json()) as LeaseState;
      setLease({ ...lease, status: state.status });
    } catch (caught) {
      setError(describe(caught));
    } finally {
      setBusy(null);
    }
  }, [lease, node.public_url]);

  /**
   * Keeps the page honest about what the node thinks.
   *
   * Without this the panel shows a countdown that reaches zero and then nothing
   * changes: the lease is frozen and later destroyed by the node's sweep, while
   * the page still offers Extend as though the session were live. The node is
   * the authority on lease status, so the page asks it rather than inferring
   * anything from its own clock.
   */
  useEffect(() => {
    if (lease === null || lease.token === undefined) return;
    if (lease.status === "stopped" || lease.status === "expired" || lease.status === "failed") {
      return;
    }

    const token = lease.token;
    const id = lease.lease_id;
    let cancelled = false;

    const check = async () => {
      try {
        const response = await fetch(`${node.public_url}/v1/leases/${id}`, {
          headers: { Authorization: `Bearer ${token}` },
        });
        if (!response.ok || cancelled) return;
        const state = (await response.json()) as LeaseState;
        setLease((current) =>
          current === null || current.lease_id !== id
            ? current
            : { ...current, status: state.status, expires_at: state.expires_at },
        );
      } catch {
        // A poll that fails changes nothing. The node may be restarting, and
        // showing an error for a background check would bury the real state
        // under noise the renter cannot act on.
      }
    };

    void check();
    const timer = setInterval(() => void check(), 10_000);
    return () => {
      cancelled = true;
      clearInterval(timer);
    };
  }, [lease, node.public_url]);

  if (offer === undefined) {
    return (
      <Notice title="This node does not sell interactive time">
        It runs batch jobs only. Leasing is opt-in per provider and must never be switched on as a
        side effect of anything else, so a node without it is a normal node rather than a
        misconfigured one.
      </Notice>
    );
  }

  if (!node.online) {
    return (
      <Notice title="This node is offline">
        It has either withdrawn or missed its heartbeats. Nothing can be rented until it checks in
        again — and nothing you do here would be charged.
      </Notice>
    );
  }

  if (lease !== null) {
    return (
      <LeasePanel
        lease={lease}
        identity={identity}
        node={node}
        busy={busy}
        error={error}
        onExtend={extend}
        onStop={stop}
      />
    );
  }

  return (
    <div className="border border-foreground/10">
      <div className="border-b border-foreground/10 px-6 py-4">
        <span className="type-label text-muted-foreground">Buy time</span>
      </div>

      <div className="space-y-8 p-6">
        <MinutesPicker
          minutes={minutes}
          min={offer.min_minutes}
          max={offer.max_minutes}
          onChange={setMinutes}
        />

        <div className="border-y border-foreground/10 py-5">
          <div className="flex items-baseline justify-between gap-4">
            <span className="type-label text-muted-foreground">Total</span>
            <span className="type-stat">{hbar(total)} HBAR</span>
          </div>
          <p className="mt-2 font-mono text-xs text-muted-foreground">
            {total} tinybars · {hbar(offer.price_tinybars_per_minute)} HBAR per minute
          </p>
        </div>

        {offer.gpu ? (
          <label className="flex cursor-pointer items-start gap-3 text-sm">
            <input
              type="checkbox"
              checked={requireGpu}
              onChange={(event) => setRequireGpu(event.target.checked)}
              className="mt-1"
            />
            <span>
              Refuse this node if its GPU is not actually usable.
              <span className="mt-1 block text-muted-foreground">
                Checked before the 402, so a rejection costs nothing.
              </span>
            </span>
          </label>
        ) : (
          <p className="text-sm text-muted-foreground">
            This node sells CPU time. A lease container here has no usable GPU, so
            <code className="mx-1 font-mono text-xs">require_gpu</code>
            would be refused before you were asked to pay.
          </p>
        )}

        <ConnectWallet />

        {wallet.status === "connected" ? (
          <Button onClick={rent} disabled={busy !== null} size="lg" className="w-full">
            {busy ?? `Rent for ${minutes} minutes`}
          </Button>
        ) : null}

        {busy !== null ? (
          <p className="text-sm text-muted-foreground">
            The node starts the container and proves it is reachable <em>before</em> taking payment,
            so this can take a moment. Nothing is charged until it answers.
          </p>
        ) : null}

        {error !== null ? (
          <div className="border border-destructive/40 bg-destructive/5 p-4">
            <p className="type-label mb-2 text-destructive">Nothing was charged</p>
            <p className="font-mono text-xs break-words text-destructive">{error}</p>
          </div>
        ) : null}
      </div>
    </div>
  );
}

function MinutesPicker({
  minutes,
  min,
  max,
  onChange,
}: {
  minutes: number;
  min: number;
  max: number;
  onChange: (value: number) => void;
}) {
  // Presets a renter would actually pick, filtered to what this node sells.
  const presets = [15, 30, 60, 120].filter((value) => value >= min && value <= max);

  return (
    <div>
      <div className="mb-4 flex items-baseline justify-between">
        <span className="type-label text-muted-foreground">Minutes</span>
        <span className="font-mono text-2xl">{minutes}</span>
      </div>

      <input
        type="range"
        min={min}
        max={max}
        step={1}
        value={minutes}
        onChange={(event) => onChange(Number(event.target.value))}
        className="w-full accent-accent"
      />

      <div className="mt-2 flex justify-between font-mono text-xs text-muted-foreground">
        <span>{min} min</span>
        <span>{max} min</span>
      </div>

      {presets.length > 0 ? (
        <div className="mt-4 flex flex-wrap gap-2">
          {presets.map((preset) => (
            <Button
              key={preset}
              variant={minutes === preset ? "default" : "outline"}
              size="sm"
              onClick={() => onChange(preset)}
            >
              {preset}m
            </Button>
          ))}
        </div>
      ) : null}
    </div>
  );
}

function Notice({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <div className="border border-foreground/10 p-6">
      <h3 className="mb-3 text-lg">{title}</h3>
      <p className="text-sm text-muted-foreground">{children}</p>
    </div>
  );
}

/**
 * Turns a thrown value into something a renter can act on.
 *
 * `PaymentError` carries the node's own body, which is where the useful part
 * lives — "this node already has an active lease", "minutes must be between 5
 * and 120", "the lease image is not built yet". Showing only the status code
 * would throw all of that away.
 *
 * Everything else is reported as it actually arrived, including the error's
 * name and any `cause`. An earlier version pattern-matched "Failed to fetch"
 * and asserted the node was unreachable or misconfigured for CORS, which sent
 * at least one person checking a node that was answering correctly. A browser
 * raises that same bare TypeError for a blocked mixed-content request, an
 * extension-cancelled request, a DNS failure and a dropped connection, and this
 * code cannot tell those apart — so it no longer pretends to.
 */
function describe(caught: unknown): string {
  if (caught instanceof PaymentError) {
    return caught.body !== "" ? `${caught.message}\n${caught.body}` : caught.message;
  }
  if (caught instanceof Error) {
    const cause = caught.cause instanceof Error ? `\ncaused by: ${caught.cause.message}` : "";
    return `${caught.name}: ${caught.message}${cause}`;
  }
  return String(caught);
}
