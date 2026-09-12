/**
 * Escrow-backed sessions: the refundable half of interactive rental.
 *
 * A session buys the same thing a lease does — an SSH shell and a Jupyter
 * server on a provider's GPU — and differs only in how it is paid for. A lease
 * pays forward per slice through the facilitator and never refunds. A session
 * deposits into the SessionEscrow contract, and when it ends the contract pays
 * the provider for the seconds actually elapsed and returns the rest.
 *
 * Every amount here is a string in tinybars, like everywhere else in ClearGate.
 * Never parse one into a float.
 *
 * These types are mirrored field for field by Go structs in
 * `agent/internal/httpapi/sessions.go`. Changing one without the other is a
 * cross-team break — the same rule the x402 challenge shape follows.
 */

/** What a renter asks for when opening a session. */
export interface SessionSpec {
    /** How long to buy up front, in seconds. Bounded by the node's offer. */
    seconds: number;
    /** The renter's SSH public key. The private half never leaves their machine. */
    public_key: string;
    require_gpu?: boolean;
}

/**
 * The node's quote, returned in the 402 challenge before any money moves.
 *
 * The renter's client echoes `session_id`, `provider_address` and
 * `price_tinybars_per_second` into its `openSession` call, and the node then
 * checks the on-chain record against this same quote before provisioning
 * anything — so a renter cannot deposit at a rate the node never offered.
 */
export interface SessionQuote {
    /** Minted by the node, 0x-prefixed 32 bytes. The contract's key. */
    session_id: string;
    /** The SessionEscrow deployment to deposit into. */
    escrow_contract: string;
    /**
     * The provider's EVM address.
     *
     * Resolved by the node from the mirror node, never derived: a contract's
     * HBAR transfer to the "long-zero" form of an account that has an EVM alias
     * fails, so the alias is the only form that can actually be paid.
     */
    provider_address: string;
    price_tinybars_per_second: string;
    min_seconds: number;
    max_seconds: number;
    /** Total this node will sell across top-ups, in seconds. */
    max_total_seconds: number;
    network: string;
}

/** The 200 body of a paid POST /v1/sessions. Mirrors LeaseCreated. */
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
    tunnel_mode: "named" | "quick" | "off";

    /** The renter's own openSession transaction, echoed back. */
    deposit_transaction: string;
    price_tinybars_per_second: string;
    deposited_tinybars: string;
    seconds: number;
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
 * What GET /v1/sessions/:id reports. The renter's CLI polls this to decide
 * when to top up, and reads `settle_state` to know whether it still owes the
 * chain a closing call on exit.
 */
export interface SessionState {
    session_id: string;
    lease_id: string;
    status: SessionStatus;
    created_at: string;
    expires_at: string;
    seconds_remaining: number;
    paid_seconds: number;
    gpu: boolean;
    /**
     * "pending" while the contract still holds the deposit, "done" once the
     * split has happened. Anyone may call settle, so a client seeing "pending"
     * on a finished session can simply close it itself.
     */
    settle_state: "pending" | "done";
    error: string | null;
}

/** What a node advertises about the sessions it sells, alongside LeaseOffer. */
export interface SessionOffer {
    price_tinybars_per_second: string;
    min_seconds: number;
    max_seconds: number;
    max_total_seconds: number;
    escrow_contract: string;
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
