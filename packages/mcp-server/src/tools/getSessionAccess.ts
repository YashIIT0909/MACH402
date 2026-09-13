import { z } from "zod";
import type { McpServer } from "@modelcontextprotocol/sdk/server/mcp.js";
import { SessionClient } from "../vendor/sessionClient.js";

export function registerGetSessionAccess(server: McpServer): void {
  server.registerTool(
    "get_session_access",
    {
      title: "Get session status and access info",
      description:
        "Free: polls a session's current state — status, remaining credit, and whether it's running " +
        "low (low_credits) and due a top_up_session call.",
      inputSchema: {
        node_url: z.string().url(),
        session_id: z.string(),
        token: z.string(),
      },
    },
    async ({ node_url, session_id, token }) => {
      const client = new SessionClient(node_url);
      const state = await client.sessionState(session_id, token);
      return { content: [{ type: "text", text: JSON.stringify(state, null, 2) }] };
    },
  );
}
