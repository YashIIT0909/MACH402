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
 * Tinybars to HBAR for display only.
 *
 * The string is split rather than divided: amounts are never parsed into floats
 * anywhere in MACH402, and this is no exception just because it is a label.
 */
export function hbar(tinybars: string): string {
  const padded = tinybars.padStart(9, "0");
  const whole = padded.slice(0, -8);
  const fraction = padded.slice(-8).replace(/0+$/, "");
  return fraction === "" ? whole : `${whole}.${fraction}`;
}

/**
 * The same amount, short enough to set at display size.
 *
 * A tinybar amount carries eight decimal places and most of them are noise to a
 * renter watching a balance — 0.01000200 at 66px does not fit half a panel, and
 * padding it out of the way by shrinking the type makes the one number the page
 * is about the smallest thing on it.
 *
 * Truncated, never rounded, and by slicing the string: these are exact integer
 * amounts and arithmetic on them in a float is how a balance ends up off by a
 * tinybar. Truncation also keeps the displayed figure from ever overstating
 * what is owed. A non-zero amount that would truncate to nothing is shown as a
 * bound instead, because "0" would be a lie about money.
 */
export function hbarShort(tinybars: string, decimals = 4): string {
  const padded = tinybars.padStart(9, "0");
  const whole = padded.slice(0, -8).replace(/^0+(?=\d)/, "");
  const fraction = padded.slice(-8).slice(0, decimals).replace(/0+$/, "");

  if (fraction !== "") return `${whole}.${fraction}`;
  // Whole part aside, everything shown is zero: say so honestly.
  if (whole !== "0") return whole;
  return /[1-9]/.test(tinybars) ? `<0.${"0".repeat(decimals - 1)}1` : "0";
}

export type NodeResult =
  | { ok: true; node: NodeListing }
  | { ok: false; error: string };

/**
 * One node, for the rent page.
 *
 * Read from the registry rather than from the node itself so the page renders
 * before any cross-origin call happens. The browser does talk to the node
 * directly — that is the whole point, payments are renter -> node — but only
 * once the renter clicks Rent, and the node's own `/v1/specs` is the
 * authority at that moment.
 */
export async function fetchNode(nodeId: string): Promise<NodeResult> {
  try {
    const response = await fetch(`${REGISTRY_URL}/v1/nodes/${encodeURIComponent(nodeId)}`, {
      cache: "no-store",
    });
    if (response.status === 404) {
      return { ok: false, error: "no node with that id has ever checked in" };
    }
    if (!response.ok) {
      return { ok: false, error: `registry answered ${response.status} ${response.statusText}` };
    }
    return { ok: true, node: (await response.json()) as NodeListing };
  } catch (error) {
    return { ok: false, error: error instanceof Error ? error.message : String(error) };
  }
}
