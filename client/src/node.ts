/**
 * Typed client for a ClearGate provider node's /v1 API.
 *
 * Only `createJob` costs money; everything else is either free (specs) or
 * authorized by the job token minted at payment.
 */
import type { JobSpec, JobState, NodeSpec, SettleResponse } from "@cleargate/types";
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
