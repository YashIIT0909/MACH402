"use client";

import type { SessionCreated, SessionState } from "@cleargate/types";

/**
 * Where a bought session survives a page reload.
 *
 * Without this a refresh loses the session token, and with it the only way to
 * top up or stop — the node happily goes on burning credit for a container the
 * renter can no longer reach or refund. The money is real, so the handle to it
 * cannot live only in React state.
 *
 * `localStorage`, not `sessionStorage`: a renter who closes the tab and comes
 * back an hour later has the same problem as one who hit reload, and their
 * credit is still burning either way.
 *
 * What is stored is a bearer token for one session on one node, plus the
 * Jupyter token — enough to use, top up and stop that session, and nothing
 * else. It cannot move money anywhere but back to the renter, and it is already
 * in this page's DOM. Scoped per node id so two sessions on two nodes do not
 * overwrite each other.
 */
const PREFIX = "mach402.session.v1.";

/** Dropped after this long regardless of state, so a dead entry cannot outlive its usefulness. */
const MAX_AGE_MS = 24 * 60 * 60 * 1000;

export type StoredSession = SessionCreated &
  Partial<SessionState> & {
    /*
     * Set when the node stopped answering for this session id.
     *
     * Not a status the node reports — it is what the *absence* of an answer
     * means. A node releases its single lease slot the moment a session ends,
     * and `GET /v1/sessions/:id` is answered from that slot, so a finished
     * session 404s rather than reporting "expired". The browser has to read
     * that 404 as the ending it is, and mark why it is showing numbers it can
     * no longer refresh.
     */
    ended_remotely?: boolean;
  };

type Envelope = { saved_at: number; session: StoredSession };

function keyFor(nodeId: string): string {
  return `${PREFIX}${nodeId}`;
}

export function loadSession(nodeId: string): StoredSession | null {
  try {
    const raw = window.localStorage.getItem(keyFor(nodeId));
    if (raw === null) return null;

    const envelope = JSON.parse(raw) as Envelope;
    if (typeof envelope.saved_at !== "number" || envelope.session?.session_id === undefined) {
      window.localStorage.removeItem(keyFor(nodeId));
      return null;
    }
    if (Date.now() - envelope.saved_at > MAX_AGE_MS) {
      window.localStorage.removeItem(keyFor(nodeId));
      return null;
    }
    return envelope.session;
  } catch {
    // A private window, blocked site data, or a half-written entry. Losing the
    // handle is bad, but throwing here would take the whole rent page with it.
    return null;
  }
}

export function saveSession(nodeId: string, session: StoredSession): void {
  try {
    const envelope: Envelope = { saved_at: Date.now(), session };
    window.localStorage.setItem(keyFor(nodeId), JSON.stringify(envelope));
  } catch {
    // Same reasoning as above: a session that cannot be persisted still works
    // in this tab, and that is strictly better than refusing to open one.
  }
}

export function clearSession(nodeId: string): void {
  try {
    window.localStorage.removeItem(keyFor(nodeId));
  } catch {
    // Nothing to do — the entry expires on its own age check.
  }
}
