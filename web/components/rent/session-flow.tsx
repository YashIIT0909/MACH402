"use client";

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import type { LeaseOffer, NodeListing, SessionCreated, SessionState } from "@cleargate/types";
import { isSessionTerminal } from "@cleargate/types";
import {
  createWalletPayer,
  generateSSHKeypair,
  payFor,
  type SubtleCryptoLike,
} from "@cleargate/client/browser";

import { Button } from "@/components/ui/button";
import { useWallet } from "@/components/wallet/wallet-provider";
import { hbar, hbarShort } from "@/lib/registry";
import { clearSession, loadSession, saveSession } from "@/lib/session-store";
import { ConnectWallet } from "./connect-wallet";
import { MinutesPicker } from "./minutes-picker";
import { SessionPanel, type SessionView } from "./session-panel";
import { describe } from "./describe-error";

/**
 * Buying interactive time from the browser — the only way it is sold.
 *
 * The order here is the node's order, not a UI convenience: the node validates
 * the spec, answers 402, verifies, starts the container, signs the certificate,
 * points the tunnel, *proves it is reachable*, and only then settles. A renter
 * is never charged for a session that never came up. What a payment buys is a
 * *credit* the node burns down by the second, refunding the remainder when the
 * session ends. The renter chooses the session's length; it is paid for in
 * chunks of at most `offer.chunk_seconds`, topped up automatically as each runs
 * low until that length is covered — this component fires those top-ups
 * itself, which means the wallet may prompt again while a session is open. That
 * is expected, not a bug. The session ends when the chosen time is used.
 *
 * The thing that makes prepaying a stranger checkable is not this component:
 * it is that the node publishes what it owes, every fifteen seconds, to its
 * own Hedera Consensus Service topic. `SessionPanel` links to it so a renter
 * can verify the running balance independently of anything this page claims.
 */
export function SessionFlow({ node, offer }: { node: NodeListing; offer: LeaseOffer }) {
  const { state: wallet } = useWallet();

  const minSeconds = offer.min_minutes * 60;
  const pricePerSecond = BigInt(offer.price_tinybars_per_second ?? "0");
  /*
   * The chunk is the node's, not ours.
   *
   * There used to be a `?? 300` here, which meant a node that published no
   * chunk size silently got someone else's five minutes — the page would then
   * explain a payment split the node had never agreed to. A missing chunk size
   * is not a number to guess: it means this node has not said, so the page
   * stops claiming to know.
   */
  const chunkSeconds = offer.chunk_seconds !== undefined && offer.chunk_seconds > 0
    ? offer.chunk_seconds
    : null;

  // The picker chooses the session's length, within the node's own bounds. It
  // is paid for in chunks of at most chunk_seconds — the most of a renter's
  // money a node ever holds ahead of the compute — and the node stops taking
  // top-ups once the chosen length is covered.
  const pickerMinMinutes = Math.max(1, Math.ceil(minSeconds / 60));
  const pickerMaxMinutes = Math.max(pickerMinMinutes, offer.max_minutes);
  const hasChoice = pickerMinMinutes < pickerMaxMinutes;

  const [minutes, setMinutes] = useState(() =>
    Math.min(Math.max(30, pickerMinMinutes), pickerMaxMinutes),
  );
  const sessionSeconds = minutes * 60;
  const totalCost = pricePerSecond * BigInt(sessionSeconds);
  const firstPaymentSeconds = chunkSeconds === null ? sessionSeconds : Math.min(sessionSeconds, chunkSeconds);
  const payments = chunkSeconds === null ? null : Math.ceil(sessionSeconds / chunkSeconds);
  const firstPaymentCost = pricePerSecond * BigInt(firstPaymentSeconds);

  const [requireGpu, setRequireGpu] = useState(false);
  const [busy, setBusy] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [session, setSession] = useState<SessionView | null>(null);

  // Whether the stored session has been looked for yet. Rendering the buy form
  // before that answer is in would show "connect your wallet and pay" to
  // someone who already has a session running on this node — the exact thing
  // this is here to prevent — so the first paint waits for it.
  const [hydrated, setHydrated] = useState(false);

  // Guards the auto-top-up effect against firing twice for the same low-credit
  // moment while the first payment is still in flight — the poll that notices
  // low_credits runs every few seconds, which is faster than a wallet prompt.
  const toppingUp = useRef(false);

  /*
   * Pick the session back up after a reload.
   *
   * Only the handle is restored: session id, token, and the connection details
   * that do not change. Every number on the panel is refreshed by the poll
   * below within a second of mounting, so a stale credit figure from before the
   * reload is never what the renter acts on.
   */
  useEffect(() => {
    const stored = loadSession(node.node_id);
    if (stored !== null) setSession(stored);
    setHydrated(true);
  }, [node.node_id]);

  // Written on every change rather than at open: a top-up, a freeze and a stop
  // all move numbers the next page load should not contradict.
  useEffect(() => {
    if (!hydrated) return;
    if (session === null) return;
    saveSession(node.node_id, session);
  }, [hydrated, session, node.node_id]);

  const payer = useMemo(() => {
    if (wallet.status !== "connected") return null;
    return createWalletPayer(wallet.signer, {
      network: node.network,
      // Headroom over one payment: a top-up costs the same as opening, and this
      // cap is the renter's own guard against a node quoting a different price
      // in the 402 than it advertised in its heartbeat. A node that never
      // published a chunk size may ask for the whole session at once, so the
      // cap falls back to what the whole session was quoted at — still bounded
      // by what the renter chose, never by a number this page made up.
      maxTinybarsPerPayment: firstPaymentCost * 2n,
    });
  }, [wallet, node.network, firstPaymentCost]);

  const open = useCallback(async () => {
    if (payer === null) return;

    setError(null);
    setBusy("Generating a session key…");

    try {
      const keypair = await generateSSHKeypair(
        window.crypto.subtle as unknown as SubtleCryptoLike,
      );

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

      const { response } = await payFor(payer, `${node.public_url}/v1/sessions`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          seconds: sessionSeconds,
          public_key: keypair.publicKey,
          require_gpu: requireGpu,
        }),
      });

      setSession((await response.json()) as SessionCreated);
    } catch (caught) {
      setError(describe(caught));
    } finally {
      setBusy(null);
    }
  }, [payer, node.public_url, sessionSeconds, requireGpu]);

  const topUp = useCallback(async () => {
    if (payer === null || session === null || session.token === undefined) return;
    if (toppingUp.current) return;
    toppingUp.current = true;

    setError(null);
    setBusy("Buying more credit…");
    try {
      const { response } = await payFor(
        payer,
        `${node.public_url}/v1/sessions/${session.session_id}/topup`,
        { method: "POST", headers: { Authorization: `Bearer ${session.token}` } },
      );
      const updated = (await response.json()) as SessionState;
      setSession((current) => (current === null ? current : { ...current, ...updated }));
    } catch (caught) {
      setError(describe(caught));
    } finally {
      toppingUp.current = false;
      setBusy(null);
    }
  }, [payer, session, node.public_url]);

  const stop = useCallback(async () => {
    if (session === null || session.token === undefined) return;

    setError(null);
    setBusy("Ending the session…");
    try {
      const response = await fetch(`${node.public_url}/v1/sessions/${session.session_id}/stop`, {
        method: "POST",
        headers: { Authorization: `Bearer ${session.token}` },
      });
      if (!response.ok) {
        throw new Error(`the node answered ${response.status} ${response.statusText}`);
      }
      const state = (await response.json()) as SessionState;
      setSession((current) => (current === null ? current : { ...current, ...state }));
    } catch (caught) {
      setError(describe(caught));
    } finally {
      setBusy(null);
    }
  }, [session, node.public_url]);

  /**
   * Polls the node for what it thinks the session is worth, and fires the next
   * top-up itself.
   *
   * `low_credits` is computed by the node against its own threshold — which has
   * to clear its 15-second sweep interval plus a payment round trip — rather
   * than guessed at here. The loop runs in the browser, so the wallet may prompt
   * again mid-session, and that is the price of not making a renter babysit a
   * tab to avoid being frozen mid-run.
   */
  useEffect(() => {
    if (session === null || session.token === undefined) return;
    /*
     * A finished session is still worth watching until the refund lands. The
     * node settles on its own sweep and retries a transfer that failed, so
     * "owed to you" becomes "refunded" a few seconds after a stop — and a poll
     * that stopped at the terminal status would leave the renter looking at a
     * debt that had already been paid, with a reload as the only way to find
     * out.
     */
    // Nothing more to learn from a node that has stopped answering for this id.
    if (session.ended_remotely === true) return;
    const awaitingRefund = isSessionTerminal(session.status) && session.settle_state !== "done";
    if (isSessionTerminal(session.status) && !awaitingRefund) return;

    const token = session.token;
    const id = session.session_id;
    let cancelled = false;

    const check = async () => {
      try {
        const response = await fetch(`${node.public_url}/v1/sessions/${id}`, {
          headers: { Authorization: `Bearer ${token}` },
        });
        if (cancelled) return;
        /*
         * The node no longer answers for this session id.
         *
         * This is the ordinary end of every session, not an error: the node
         * serves session state out of its single active-lease slot and releases
         * that slot the instant a session is stopped, expires, or is reaped —
         * so the reply to "how is my session" turns from 200 to 404 between one
         * poll and the next. Deleting the session here, as this used to, threw
         * away the renter's own record of what they were owed at the exact
         * moment it became unrefreshable.
         *
         * So it is recorded as an ending instead, and the panel says the
         * numbers are the last ones the node gave rather than current.
         */
        if (response.status === 404 || response.status === 401 || response.status === 403) {
          setSession((current) => {
            if (current === null || current.session_id !== id) return current;
            if (current.ended_remotely === true) return current;
            return {
              ...current,
              status: isSessionTerminal(current.status) ? current.status : "expired",
              ended_remotely: true,
            };
          });
          return;
        }
        if (!response.ok) return;
        const state = (await response.json()) as SessionState;
        setSession((current) =>
          current === null || current.session_id !== id ? current : { ...current, ...state },
        );
        if (state.low_credits && state.fully_paid !== true && state.status === "active" && payer !== null) {
          void topUp();
        }
      } catch {
        // A poll that fails changes nothing — the node may be restarting, and
        // an error banner for a background check would bury state the renter
        // cannot act on.
      }
    };

    void check();
    const timer = setInterval(() => void check(), awaitingRefund ? 5_000 : 7_000);
    return () => {
      cancelled = true;
      clearInterval(timer);
    };
    // topUp is intentionally left out: it is stable across a session's life
    // (same session id and token throughout) and including it would restart
    // this poll on every render it causes.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [
    session?.session_id,
    session?.token,
    session?.status,
    session?.settle_state,
    session?.ended_remotely,
    node.public_url,
    payer,
  ]);

  const dismiss = useCallback(() => {
    clearSession(node.node_id);
    setSession(null);
    setError(null);
  }, [node.node_id]);

  // Nothing at all until the stored session has been looked for — one frame,
  // and it is the difference between a renter seeing their session and seeing
  // an invitation to buy a second one.
  if (!hydrated) return null;

  if (session !== null) {
    return (
      <SessionPanel
        session={session}
        node={node}
        busy={busy}
        error={error}
        onTopUp={topUp}
        onStop={stop}
        onDismiss={dismiss}
      />
    );
  }

  return (
    <div className="border border-foreground/10">
      <div className="border-b border-foreground/10 px-6 py-4">
        <span className="type-label text-muted-foreground">Buy time</span>
      </div>

      <div className="space-y-8 p-6">
        {hasChoice ? (
          <MinutesPicker
            minutes={minutes}
            min={pickerMinMinutes}
            max={pickerMaxMinutes}
            onChange={setMinutes}
          />
        ) : (
          <p className="text-sm text-muted-foreground">
            This node sells sessions of exactly {pickerMinMinutes} minutes.
          </p>
        )}

        <div className="border-y border-foreground/10 py-5">
          <div className="flex items-baseline justify-between gap-4">
            <span className="type-label text-muted-foreground">{minutes}-minute session</span>
            <span className="type-stat tabular-nums" title={`${hbar(totalCost.toString())} HBAR`}>
              {hbarShort(totalCost.toString())} HBAR
            </span>
          </div>
          <p className="mt-2 font-mono text-xs text-muted-foreground">
            {hbarShort(pricePerSecond.toString(), 6)} HBAR/s ·{" "}
            {payments === null
              ? "paid on demand"
              : payments === 1
                ? "one payment"
                : `${payments} payments of up to ${hbarShort(firstPaymentCost.toString())} HBAR`}
          </p>
          {/*
           * Spelled out in the renter's own minutes, because "chunk_seconds"
           * shown as a bare second count next to a minute count is the thing
           * that reads as a hardcoded five minutes somebody forgot to change.
           * It is this node's setting, it is a cap on exposure rather than a
           * limit on the session, and saying both is what stops it looking
           * like a bug.
           */}
          <p className="mt-3 text-xs text-muted-foreground">
            {payments === null ? (
              <>This node has not published how it splits payments, so it may ask for the whole session at once. </>
            ) : payments === 1 ? (
              <>One payment covers the whole session. </>
            ) : (
              <>
                Paid in {payments} parts: {formatMinutes(firstPaymentSeconds)} now, then the rest
                automatically as each runs low. Your session is still the full {minutes} minutes —{" "}
                {formatMinutes(chunkSeconds ?? 0)} is this node&apos;s cap on how far ahead of the
                compute it is ever holding your money.{" "}
              </>
            )}
            The session ends by itself when your {minutes} minutes are used, and{" "}
            <strong className="text-foreground">stopping early refunds what you did not use</strong>.
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
            This node sells CPU time. A session container here has no usable GPU, so
            <code className="mx-1 font-mono text-xs">require_gpu</code>
            would be refused before you were asked to pay.
          </p>
        )}

        {node.audit_topic !== undefined && node.audit_topic !== "" ? (
          <p className="text-xs text-muted-foreground">
            What this node owes you is published to its own audit topic every 15 seconds this
            session runs —{" "}
            <a
              href={`https://hashscan.io/testnet/topic/${node.audit_topic}`}
              target="_blank"
              rel="noreferrer"
              className="underline underline-offset-4 hover:text-accent"
            >
              {node.audit_topic}
            </a>
            . That record exists whether or not you ever look at it.
          </p>
        ) : (
          <p className="text-xs text-destructive">
            This node reports no audit topic. Nothing but its word says what it owes you if you
            stop early — consider another node if that matters to you.
          </p>
        )}

        <ConnectWallet />

        {wallet.status === "connected" ? (
          <Button onClick={open} disabled={busy !== null} size="lg" className="w-full">
            {busy ?? `Start a ${minutes}-minute session`}
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

/**
 * Seconds as the renter said them — minutes where they divide cleanly, because
 * "300s" beside "7 minutes" makes the reader do arithmetic to find out whether
 * the two numbers are even about the same thing.
 */
function formatMinutes(seconds: number): string {
  if (seconds <= 0) return "0 minutes";
  if (seconds % 60 !== 0) return `${seconds} seconds`;
  const minutes = seconds / 60;
  return minutes === 1 ? "1 minute" : `${minutes} minutes`;
}
