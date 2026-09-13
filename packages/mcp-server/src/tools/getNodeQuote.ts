import { z } from "zod";
import type { McpServer } from "@modelcontextprotocol/sdk/server/mcp.js";
import { SessionClient } from "../vendor/sessionClient.js";
import { createSshIdentity, discardSshIdentity } from "../vendor/sshIdentity.js";

export function registerGetNodeQuote(server: McpServer): void {
  server.registerTool(
    "get_node_quote",
    {
      title: "Get a session price quote",
      description:
        "Ask a node what a metered session of a given length would cost. No payment happens and no " +
        "session is created — safe to call as many times as needed while shopping around.",
      inputSchema: {
        node_url: z.string().url(),
        seconds: z.number().int().positive(),
        require_gpu: z.boolean().optional(),
      },
    },
    async ({ node_url, seconds, require_gpu }) => {
      const client = new SessionClient(node_url);
      // A throwaway key just to satisfy the node's spec validation before it
      // checks for payment — nothing is ever signed with it.
      const identity = createSshIdentity();
      try {
        const quote = await client.quoteSession({
          seconds,
          public_key: identity.publicKey,
          require_gpu,
        });
        return { content: [{ type: "text", text: JSON.stringify(quote, null, 2) }] };
      } finally {
        discardSshIdentity(identity);
      }
    },
  );
}
