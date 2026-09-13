/**
 * Read-only client for the mach402 discovery registry.
 *
 * The registry never touches money (CLAUDE.md invariant 3), so this is the
 * only free, no-key call in this package's shopping flow.
 */
import type { NodeListing } from "./vendor/types.js";

export async function listOnlineNodes(registryUrl: string): Promise<NodeListing[]> {
  const base = registryUrl.replace(/\/$/, "");
  const response = await fetch(`${base}/v1/nodes?online=true`);
  if (!response.ok) {
    throw new Error(`GET ${base}/v1/nodes failed: ${response.status} ${response.statusText}`);
  }
  const body = (await response.json()) as { nodes: NodeListing[] };
  return body.nodes;
}
