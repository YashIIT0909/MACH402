/**
 * Typed client for a mach402 provider node's session endpoints.
 *
 * Session-only subset, vendored and trimmed from client/src/node.ts — this
 * package never opens a flat-fee job or a `direct`-mode lease. `quoteSession`
 * and `quoteTopUp` are new here: a plain POST with no `PAYMENT-SIGNATURE`
 * header. The node validates the spec and, finding no payment header, answers
 * with the 402 challenge before starting anything — so this is a safe,
 * side-effect-free price check (see CLAUDE.md: "verify before work").
 */
import type { PaymentRequired, SettleResponse } from "@x402/core/types";
import type { NodeSpec, SessionCreated, SessionSpec, SessionState } from "./types.js";
import { HEADER_PAYMENT_REQUIRED, payFor, type Payer } from "./payer.js";

export class SessionClient {
  readonly baseUrl: string;

  constructor(baseUrl: string) {
    this.baseUrl = baseUrl.replace(/\/$/, "");
  }

  /** Free: discovery must not cost money, or agents cannot shop around. */
  async specs(): Promise<NodeSpec> {
    const response = await fetch(`${this.baseUrl}/v1/specs`);
    if (!response.ok) {
      throw new Error(`GET ${this.baseUrl}/v1/specs failed: ${response.status} ${response.statusText}`);
    }
    return (await response.json()) as NodeSpec;
  }

  /** Free, no side effects: prices opening a session without paying for one. */
  async quoteSession(spec: SessionSpec): Promise<PaymentRequired> {
    const response = await fetch(`${this.baseUrl}/v1/sessions`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(spec),
    });
    return decodeChallenge(response, "quoting the session");
  }

  /** Free, no side effects: prices the next top-up chunk on a live session. */
  async quoteTopUp(sessionId: string, token: string): Promise<PaymentRequired> {
    const response = await fetch(`${this.baseUrl}/v1/sessions/${sessionId}/topup`, {
      method: "POST",
      headers: { Authorization: `Bearer ${token}` },
    });
    return decodeChallenge(response, "quoting the top-up");
  }

  /**
   * x402-gated: buys the first chunk of a metered session.
   *
   * The node settles only once the container is up and proven reachable, so a
   * failure here means nothing was charged.
   */
  async createSession(
    payer: Payer,
    spec: SessionSpec,
  ): Promise<{ session: SessionCreated; settlement: SettleResponse }> {
    const { response, settlement } = await payFor(payer, `${this.baseUrl}/v1/sessions`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(spec),
    });
    return { session: (await response.json()) as SessionCreated, settlement };
  }

  /** x402-gated: buys another chunk of credit on a live session. */
  async topUpSession(
    payer: Payer,
    sessionId: string,
    token: string,
  ): Promise<{ state: SessionState; settlement: SettleResponse }> {
    const { response, settlement } = await payFor(
      payer,
      `${this.baseUrl}/v1/sessions/${sessionId}/topup`,
      {
        method: "POST",
        headers: { Authorization: `Bearer ${token}` },
      },
    );
    return { state: (await response.json()) as SessionState, settlement };
  }

  /** Free: what a polling loop reads to decide whether to top up. */
  async sessionState(sessionId: string, token: string): Promise<SessionState> {
    const response = await fetch(`${this.baseUrl}/v1/sessions/${sessionId}`, {
      headers: { Authorization: `Bearer ${token}` },
    });
    if (!response.ok) {
      throw new Error(await describeFailure(response, "reading the session"));
    }
    return (await response.json()) as SessionState;
  }

  /**
   * Free: ends the session. This is what triggers the refund — the node burns
   * the final seconds, returns whatever credit is left, and answers with the
   * settled state.
   */
  async stopSession(sessionId: string, token: string): Promise<SessionState> {
    const response = await fetch(`${this.baseUrl}/v1/sessions/${sessionId}/stop`, {
      method: "POST",
      headers: { Authorization: `Bearer ${token}` },
    });
    if (!response.ok) {
      throw new Error(await describeFailure(response, "stopping the session"));
    }
    return (await response.json()) as SessionState;
  }
}

async function decodeChallenge(response: Response, what: string): Promise<PaymentRequired> {
  if (response.status !== 402) {
    throw new Error(await describeFailure(response, what));
  }
  const header = response.headers.get(HEADER_PAYMENT_REQUIRED);
  if (header === null) {
    throw new Error(`${what} failed: the node answered 402 without a ${HEADER_PAYMENT_REQUIRED} header`);
  }
  return JSON.parse(Buffer.from(header, "base64").toString("utf8")) as PaymentRequired;
}

/**
 * Turns a node's error response into something a renter can act on — the node
 * puts a human-readable reason in the body.
 */
async function describeFailure(response: Response, what: string): Promise<string> {
  let detail = "";
  try {
    const body = (await response.json()) as { error?: string };
    detail = body.error ?? "";
  } catch {
    // A node that answered with something other than JSON: the status is all there is.
  }
  const suffix = detail ? `: ${detail}` : "";
  return `${what} failed (${response.status} ${response.statusText})${suffix}`;
}
