import type { NodeListing } from "@cleargate/types";

/**
 * The registry this site reads from. Server-side only, so the browser never
 * talks to the registry directly and there is no CORS to configure.
 */
export const REGISTRY_URL = process.env.REGISTRY_URL ?? "http://localhost:4400";

export type NodesResult =
  | { ok: true; nodes: NodeListing[] }
  | { ok: false; error: string };

export async function fetchNodes(): Promise<NodesResult> {
  try {
    const response = await fetch(`${REGISTRY_URL}/v1/nodes`, { cache: "no-store" });
    if (!response.ok) {
      return { ok: false, error: `registry answered ${response.status} ${response.statusText}` };
    }
    const body = (await response.json()) as { nodes: NodeListing[] };
    return { ok: true, nodes: body.nodes };
  } catch (error) {
    return { ok: false, error: error instanceof Error ? error.message : String(error) };
  }
}
