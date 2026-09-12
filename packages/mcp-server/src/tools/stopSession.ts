import { z } from "zod";
import type { McpServer } from "@modelcontextprotocol/sdk/server/mcp.js";
import { SessionClient } from "../vendor/sessionClient.js";
import { logAudit } from "../safety/audit.js";

export function registerStopSession(server: McpServer): void {
  server.registerTool(
    "stop_session",
    {
      title: "Stop a session",
      description:
        "Free: ends the session now. This is what triggers the node's refund of unburned credit " +
        "back to the payer.",
      inputSchema: {
        node_url: z.string().url(),
        session_id: z.string(),
        token: z.string(),
      },
    },
    async ({ node_url, session_id, token }) => {
      const client = new SessionClient(node_url);
      const state = await client.stopSession(session_id, token);
      logAudit({ tool: "stop_session", node_url, session_id, outcome: state.status });
      return { content: [{ type: "text", text: JSON.stringify(state, null, 2) }] };
    },
  );
}
