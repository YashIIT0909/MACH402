/**
 * Metered sessions: interactive time bought in chunks, with the unburned
 * remainder refundable.
 *
 * A session is how interactive time on a provider's GPU is sold — a container
 * reached through Jupyter. The renter pays forward per chunk *into a credit*,
 * the node burns that credit second by second while the container is actually
 * running, and whatever is left when the session ends is transferred back to
 * the renter.
 *
 * The honest description of the trust shape: this is forward payment with a
 * provider-issued refund, not an escrow. The node holds the money between the
 * chunk payment and the refund. What makes that checkable rather than merely
 * promised is that the node publishes the amount it would owe right now, to its
 * own HCS audit topic, every fifteen seconds the session is live — so a
 * provider who later refuses to refund has already signed and ordered, on
 * consensus, the number they are refusing to honour.
 *
 * Every amount here is a string in tinybars, like everywhere else in MACH402.
 * Never parse one into a float.
 *
 * These types are mirrored field for field by Go structs in
 * `agent/internal/httpapi/sessions.go`. Changing one without the other is a
 * cross-team break — the same rule the x402 challenge shape follows.
 */

/** What a renter asks for when opening a session. */
export interface SessionSpec {
    /**
     * The session's length, in seconds, within the node's `min_minutes` and
     * `max_minutes`. It is paid for in chunks of at most `chunk_seconds`, and
     * the session ends once this much time has been used.
     */
    seconds: number;
    /** The renter's SSH public key. The private half never leaves their machine. */
    public_key: string;
    require_gpu?: boolean;
}

/** The 200 body of a paid POST /v1/sessions. */
export interface SessionCreated {
    session_id: string;
    lease_id: string;
    token?: string;
    status: SessionStatus;
    expires_at: string;

    certificate: string;
    ssh_user: string;
    ssh_host?: string;
    ssh_principal: string;

    jupyter_url: string;
    jupyter_token: string;
    tunnel_mode: "quick";

    /** The facilitator's settlement for the chunk just bought. */
    transaction: string;
    payer: string;
    /** What that chunk cost — the same figure `credit_tinybars` started at. */
    amount_tinybars: string;

    price_tinybars_per_second: string;
    /** Unburned credit: what the node owes back if the session stopped now. */
    credit_tinybars: string;
    /** True once the remaining credit is under the node's top-up threshold. */
    low_credits: boolean;
    /** Seconds the credit currently buys. */
    seconds: number;
    /** The session length chosen, in seconds. Absent on nodes older than this field. */
    session_seconds?: number;
    /** True once every chunk of the chosen length has been bought; no more top-ups follow. */
    fully_paid?: boolean;
}

/** A session runs through the same states a lease does. */
export type SessionStatus =
    | "provisioning"
    | "active"
    | "paused"
    | "stopped"
    | "expired"
    | "failed";

/**
 * What GET /v1/sessions/:id reports. The renter's client polls this to decide
 * when to top up: `low_credits` is the signal, and it is computed by the node
 * against its own threshold rather than guessed at by the client.
 */
export interface SessionState {
    session_id: string;
    lease_id: string;
    status: SessionStatus;
    created_at: string;
    expires_at: string;
    seconds_remaining: number;
    paid_seconds: number;
    /** The session length chosen, in seconds. Absent on nodes older than this field. */
    session_seconds?: number;
    /** True once every chunk of the chosen length has been bought; no more top-ups follow. */
    fully_paid?: boolean;
    gpu: boolean;

    price_tinybars_per_second: string;
    /** What the node owes back if the session stopped right now. */
    credit_tinybars: string;
    /** Cumulative burn — what the provider has actually earned so far. */
    burned_tinybars: string;
    /**
     * What was actually paid back, once `settle_state` is "done" — "0" until
     * then, and "0" on a session whose credit ran out with nothing left to
     * return. Distinct from `credit_tinybars` on purpose: that field answers
     * "what is owed right now," which becomes zero the instant it is paid, so
     * it cannot also answer "what did I get back" once a session has ended.
     */
    refunded_tinybars: string;
    low_credits: boolean;

    /**
     * "pending" while the node still holds unburned credit, "done" once the
     * refund has been transferred (or there was nothing left to return).
     */
    settle_state: "pending" | "done";
    error: string | null;
}

/**
 * What a node advertises about the sessions it sells.
 *
 * The only place these terms are published. There is no separate quote
 * endpoint: the authoritative price of a particular payment is in the x402
 * challenge on the 402, and the shopping-ahead terms are here, on the free
 * /v1/specs. A second source for the same numbers would be a second thing to
 * keep in agreement with the first.
 */
export interface SessionOffer {
    price_tinybars_per_second: string;
    min_seconds: number;
    max_seconds: number;
    max_total_seconds: number;
    chunk_seconds: number;
    ssh: boolean;
    jupyter: boolean;
    gpu: boolean;
}

/** Sessions that will never run again. */
export const TERMINAL_SESSION_STATUSES: readonly SessionStatus[] = [
    "stopped",
    "expired",
    "failed",
];

export function isSessionTerminal(status: SessionStatus): boolean {
    return TERMINAL_SESSION_STATUSES.includes(status);
}
