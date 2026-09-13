import { z } from "zod";
import type { McpServer } from "@modelcontextprotocol/sdk/server/mcp.js";
import type { McpConfig } from "../config.js";
import { listOnlineNodes } from "../registryClient.js";

export function registerSearchComputeNodes(server: McpServer, config: McpConfig): void {
  server.registerTool(
    "search_compute_nodes",
    {
      title: "Search compute nodes",
      description:
        "List online mach402 provider nodes that sell metered GPU sessions, optionally filtered " +
        "by GPU requirement and max price per second. Free — no payment, no key needed.",
      inputSchema: {
        require_gpu: z
          .boolean()
          .optional()
          .describe("Only return nodes that can give a session an actually working GPU"),
        max_price_tinybars_per_second: z
          .string()
          .optional()
          .describe("Only return nodes at or below this price, in tinybars per second"),
      },
    },
    async ({ require_gpu, max_price_tinybars_per_second }) => {
      const nodes = await listOnlineNodes(config.registryUrl);
      const maxPrice =
        max_price_tinybars_per_second !== undefined ? BigInt(max_price_tinybars_per_second) : null;

      const results = nodes
        .filter((node) => node.leases?.payment_mode === "session")
        .filter((node) => !require_gpu || node.leases?.gpu === true)
        .filter((node) => {
          if (maxPrice === null) return true;
          const price = node.leases?.price_tinybars_per_second;
          return price !== undefined && BigInt(price) <= maxPrice;
        })
        .map((node) => ({
          node_id: node.node_id,
          node_url: node.public_url,
          gpu: node.leases?.gpu ?? false,
          gpu_model: node.gpu.model,
          price_tinybars_per_second: node.leases?.price_tinybars_per_second,
          chunk_seconds: node.leases?.chunk_seconds,
          min_seconds: node.leases?.min_minutes !== undefined ? node.leases.min_minutes * 60 : undefined,
          max_seconds: node.leases?.max_minutes !== undefined ? node.leases.max_minutes * 60 : undefined,
          ssh: node.leases?.ssh ?? false,
          jupyter: node.leases?.jupyter ?? false,
        }));

      return { content: [{ type: "text", text: JSON.stringify(results, null, 2) }] };
    },
  );
}
