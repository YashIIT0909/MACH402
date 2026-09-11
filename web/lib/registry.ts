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

/**
 * What the homepage counters and price cards read.
 *
 * Derived on the server and handed to the client sections as plain data, so
 * neither of them repeats the arithmetic and the two can never disagree about
 * how many nodes are online.
 */
export type RegistrySnapshot = {
  listed: number;
  online: number;
  /** Online nodes advertising a GPU the runtime can actually pass through. */
  gpus: number;
  /** Cheapest online node's flat job price, in tinybars. Null when none are up. */
  cheapestTinybars: string | null;
};

export function summarize(nodes: NodeListing[]): RegistrySnapshot {
  const online = nodes.filter((node) => node.online && !node.paused);

  const byPrice = sortByPrice(online);

  return {
    listed: nodes.length,
    online: online.length,
    gpus: online.filter((node) => node.gpu.available).length,
    cheapestTinybars: byPrice[0]?.price_tinybars ?? null,
  };
}

/**
 * Nodes cheapest first.
 *
 * Prices are compared by digit count and then lexicographically, which is
 * exact for the non-negative integer strings the registry stores. Amounts are
 * never parsed into a number anywhere in ClearGate, and ordering them is no
 * exception — a tinybar figure can exceed what a double represents exactly.
 */
export function sortByPrice(nodes: NodeListing[]): NodeListing[] {
  const normalize = (value: string) => value.replace(/^0+(?=\d)/, "");

  return [...nodes].sort((a, b) => {
    const left = normalize(a.price_tinybars);
    const right = normalize(b.price_tinybars);
    return left.length === right.length ? left.localeCompare(right) : left.length - right.length;
  });
}

/**
 * Tinybars to HBAR for display only.
 *
 * The string is split rather than divided: amounts are never parsed into floats
 * anywhere in ClearGate, and this is no exception just because it is a label.
 */
export function hbar(tinybars: string): string {
  const padded = tinybars.padStart(9, "0");
  const whole = padded.slice(0, -8);
  const fraction = padded.slice(-8).replace(/0+$/, "");
  return fraction === "" ? whole : `${whole}.${fraction}`;
}
