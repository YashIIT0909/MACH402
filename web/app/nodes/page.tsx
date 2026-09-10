import Link from "next/link";
import type { NodeListing } from "@cleargate/types";

import { fetchNodes, REGISTRY_URL } from "@/lib/registry";

// Listings go stale the moment a node stops beating, so never cache this page.
export const dynamic = "force-dynamic";

export default async function NodesPage() {
  const result = await fetchNodes();

  return (
    <>
      <h1>Nodes</h1>
      <p>
        Everything a node has announced about itself, straight from the registry at{" "}
        <code>{REGISTRY_URL}</code>. <Link href="/nodes">Refresh</Link>
      </p>

      {!result.ok ? (
        <p>
          Could not reach the registry: <code>{result.error}</code>
        </p>
      ) : result.nodes.length === 0 ? (
        <p>
          No node has ever checked in. <Link href="/provide">List your GPU</Link> to be the first.
        </p>
      ) : (
        <table>
          <thead>
            <tr>
              <th>Node</th>
              <th>Status</th>
              <th>GPU</th>
              <th>Price</th>
              <th>Limits</th>
              <th>Paid to</th>
            </tr>
          </thead>
          <tbody>
            {result.nodes.map((node) => (
              <NodeRow key={node.node_id} node={node} />
            ))}
          </tbody>
        </table>
      )}
    </>
  );
}

function NodeRow({ node }: { node: NodeListing }) {
  return (
    <tr>
      <td>
        <code>{node.node_id}</code>
        <br />
        <span className="offline">{node.public_url}</span>
        <br />
        <span className="offline">agent {node.agent_version}</span>
      </td>
      <td>
        <Status node={node} />
        <br />
        <span className="offline">seen {new Date(node.last_seen_at).toLocaleTimeString()}</span>
      </td>
      <td>
        {node.gpu.available ? (
          <>
            {node.gpu.model ?? "GPU"}
            {node.gpu.vram_mb !== null && node.gpu.vram_mb !== undefined ? (
              <>
                <br />
                <span className="offline">{Math.round(node.gpu.vram_mb / 1024)} GB VRAM</span>
              </>
            ) : null}
          </>
        ) : (
          <span className="offline">CPU only</span>
        )}
      </td>
      <td>
        {hbar(node.price_tinybars)} HBAR
        <br />
        <span className="offline">per job</span>
      </td>
      <td className="offline">
        {node.limits.cpu_cores} cores · {Math.round(node.limits.memory_mb / 1024)} GB
        <br />
        {node.limits.max_seconds}s max
      </td>
      <td>
        <code>{node.pay_to}</code>
      </td>
    </tr>
  );
}

function Status({ node }: { node: NodeListing }) {
  if (!node.online) return <span className="offline">offline</span>;
  if (node.paused) return <span className="paused">paused</span>;
  return <span className="online">available</span>;
}

/**
 * Tinybars to HBAR for display only.
 *
 * The string is split rather than divided: amounts are never parsed into floats
 * anywhere in ClearGate, and this is no exception just because it is a label.
 */
function hbar(tinybars: string): string {
  const padded = tinybars.padStart(9, "0");
  const whole = padded.slice(0, -8);
  const fraction = padded.slice(-8).replace(/0+$/, "");
  return fraction === "" ? whole : `${whole}.${fraction}`;
}
