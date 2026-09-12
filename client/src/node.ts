/**
 * Typed client for a ClearGate provider node's /v1 API.
 *
 * Only `createJob` costs money; everything else is either free (specs) or
 * authorized by the job token minted at payment.
 */
import type {
  JobSpec,
  JobState,
  LeaseCreated,
  LeaseSpec,
  LeaseState,
  NodeSpec,
  SessionCreated,
  SessionSpec,
  SessionState,
  SettleResponse,
} from "@cleargate/types";
import { payFor, type Payer } from "./pay.js";

export type JobHandle = {
  job_id: string;
  token: string;
  status: string;
  transaction: string;
  payer: string;
  amount_tinybars: string;
};

export class NodeClient {
  constructor(readonly baseUrl: string) {
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

  /** x402-gated: this is the call that moves HBAR. */
  async createJob(
    payer: Payer,
    spec: JobSpec,
  ): Promise<{ job: JobHandle; settlement: SettleResponse }> {
    const { response, settlement } = await payFor(payer, `${this.baseUrl}/v1/jobs`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(spec),
    });
    return { job: (await response.json()) as JobHandle, settlement };
  }

  /**
   * x402-gated: buys the first slice of an interactive lease.
   *
   * The node only settles once the container is up *and* proven reachable, so a
   * failure here means nothing was charged.
   */
  async createLease(
    payer: Payer,
    spec: LeaseSpec,
  ): Promise<{ lease: LeaseCreated; settlement: SettleResponse }> {
    const { response, settlement } = await payFor(payer, `${this.baseUrl}/v1/leases`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(spec),
    });
    return { lease: (await response.json()) as LeaseCreated, settlement };
  }

  /**
   * x402-gated: buys another slice on a live lease.
   *
   * The node re-signs a fresh certificate with the later expiry rather than
   * extending the old one, so what comes back has to replace what the renter
   * currently holds.
   */
  async extendLease(
    payer: Payer,
    leaseId: string,
    token: string,
    spec: LeaseSpec,
  ): Promise<{ lease: LeaseCreated; settlement: SettleResponse }> {
    const { response, settlement } = await payFor(
      payer,
      `${this.baseUrl}/v1/leases/${leaseId}/extend`,
      {
        method: "POST",
        headers: { "Content-Type": "application/json", Authorization: `Bearer ${token}` },
        body: JSON.stringify(spec),
      },
    );
    return { lease: (await response.json()) as LeaseCreated, settlement };
  }

  /** Free: what the auto-extend loop polls to decide whether to buy more time. */
  async leaseState(leaseId: string, token: string): Promise<LeaseState> {
    return (await this.authorized(`/v1/leases/${leaseId}`, token).then((r) =>
      r.json(),
    )) as LeaseState;
  }

  /** Free: ends the lease and stops the meter. Time already bought is not refunded. */
  async stopLease(leaseId: string, token: string): Promise<LeaseState> {
    const response = await fetch(`${this.baseUrl}/v1/leases/${leaseId}/stop`, {
      method: "POST",
      headers: { Authorization: `Bearer ${token}` },
    });
    if (!response.ok) {
      throw new Error(`stopping the lease failed: ${response.status} ${response.statusText}`);
    }
    return (await response.json()) as LeaseState;
  }

  /**
   * x402-gated: buys the first chunk of a metered session.
   *
   * Mechanically identical to createLease — same facilitator, same exact
   * scheme, same one-call-pays-and-provisions shape. What differs is what the
   * payment becomes: a lease's buys time that is gone whether it is used or
   * not, a session's buys credit the node burns down and refunds the remainder
   * of. The node settles only once the container is up and proven reachable, so
   * a failure here means nothing was charged.
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

  /**
   * x402-gated: buys another chunk of credit on a live session.
   *
   * Unlike extending a lease there is nothing to re-sign: the certificate was
   * minted for the session and a top-up does not move its expiry by buying a
   * new slice, it refills the credit behind it.
   */
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
        headers: { "Content-Type": "application/json", Authorization: `Bearer ${token}` },
      },
    );
    return { state: (await response.json()) as SessionState, settlement };
  }

  /**
   * Free: what the auto-top-up loop polls.
   *
   * `low_credits` is the field that matters. The node computes it against its
   * own threshold, which has to clear its sweep interval plus a payment round
   * trip — numbers a client would only be guessing at.
   */
  async sessionState(sessionId: string, token: string): Promise<SessionState> {
    return (await this.authorized(`/v1/sessions/${sessionId}`, token).then((r) =>
      r.json(),
    )) as SessionState;
  }

  /**
   * Free: ends the session. Unlike stopping a lease, this is what triggers the
   * refund — the node burns the final seconds, returns whatever credit is left,
   * and answers with the settled state.
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

  async state(jobId: string, token: string): Promise<JobState> {
    return (await this.authorized(`/v1/jobs/${jobId}`, token).then((r) => r.json())) as JobState;
  }

  async stop(jobId: string, token: string): Promise<JobState> {
    const response = await fetch(`${this.baseUrl}/v1/jobs/${jobId}/stop`, {
      method: "POST",
      headers: { Authorization: `Bearer ${token}` },
    });
    if (!response.ok) {
      throw new Error(`stop failed: ${response.status} ${response.statusText}`);
    }
    return (await response.json()) as JobState;
  }

  /** Retained output, without following. */
  async logs(jobId: string, token: string): Promise<{ lines: LogLine[] }> {
    return (await this.authorized(`/v1/jobs/${jobId}/logs`, token).then((r) => r.json())) as {
      lines: LogLine[];
    };
  }

  /**
   * Follows the job's log stream, yielding each line as it arrives.
   *
   * The node sends Server-Sent Events; this parses them off the raw body rather
   * than using EventSource so the job token can travel in a header.
   */
  async *followLogs(jobId: string, token: string): AsyncGenerator<LogEvent> {
    const response = await fetch(`${this.baseUrl}/v1/jobs/${jobId}/logs?follow=1`, {
      headers: { Authorization: `Bearer ${token}`, Accept: "text/event-stream" },
    });
    if (!response.ok || response.body === null) {
      throw new Error(`log stream failed: ${response.status} ${response.statusText}`);
    }

    const reader = response.body.getReader();
    const decoder = new TextDecoder();
    let buffer = "";

    while (true) {
      const { done, value } = await reader.read();
      if (done) break;

      buffer += decoder.decode(value, { stream: true });

      // SSE frames are separated by a blank line.
      let boundary = buffer.indexOf("\n\n");
      while (boundary !== -1) {
        const frame = buffer.slice(0, boundary);
        buffer = buffer.slice(boundary + 2);
        const event = parseSSEFrame(frame);
        if (event !== null) {
          yield event;
          if (event.event === "end") return;
        }
        boundary = buffer.indexOf("\n\n");
      }
    }
  }

  /** Downloads the job's output directory as a tar. */
  async artifact(jobId: string, token: string): Promise<ArrayBuffer> {
    const response = await this.authorized(`/v1/jobs/${jobId}/artifact`, token);
    return await response.arrayBuffer();
  }

  private async authorized(path: string, token: string): Promise<Response> {
    const response = await fetch(`${this.baseUrl}${path}`, {
      headers: { Authorization: `Bearer ${token}` },
    });
    if (!response.ok) {
      throw new Error(`GET ${path} failed: ${response.status} ${response.statusText}`);
    }
    return response;
  }
}

/**
 * Turns a node's error response into something a renter can act on.
 *
 * The node puts a human-readable reason in the body — which field of a deposit
 * disagreed with the quote, say — and losing that to a bare status code would
 * leave someone staring at "402" with no idea what to change.
 */
async function describeFailure(response: Response, what: string): Promise<string> {
  let detail = "";
  try {
    const body = (await response.json()) as { error?: string };
    detail = body.error ?? "";
  } catch {
    // A node that answered with something other than JSON: the status is all
    // there is, which is still better than nothing.
  }
  const suffix = detail ? `: ${detail}` : "";
  return `${what} failed (${response.status} ${response.statusText})${suffix}`;
}

export type LogLine = { stream: string; text: string };

export type LogEvent =
  | { event: "log"; data: LogLine }
  | { event: "end"; data: JobState };

function parseSSEFrame(frame: string): LogEvent | null {
  let event = "message";
  const dataLines: string[] = [];

  for (const line of frame.split("\n")) {
    if (line.startsWith("event:")) {
      event = line.slice("event:".length).trim();
    } else if (line.startsWith("data:")) {
      dataLines.push(line.slice("data:".length).trim());
    }
  }
  if (dataLines.length === 0) return null;

  try {
    const data: unknown = JSON.parse(dataLines.join("\n"));
    if (event === "end") return { event: "end", data: data as JobState };
    if (event === "log") return { event: "log", data: data as LogLine };
    return null;
  } catch {
    return null;
  }
}
