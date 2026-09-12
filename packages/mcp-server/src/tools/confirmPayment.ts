import { z } from "zod";
import type { McpServer } from "@modelcontextprotocol/sdk/server/mcp.js";
import type { McpConfig } from "../config.js";
import { takeConfirmedAction } from "../safety/limits.js";
import { performOpenSession } from "./openSession.js";
import { performTopUp } from "./topUpSession.js";

export function registerConfirmPayment(server: McpServer, config: McpConfig): void {
  server.registerTool(
    "confirm_payment",
    {
      title: "Confirm a held payment",
      description:
        "Executes a payment that open_session or top_up_session paused because its cost was above " +
        "the configured confirmation threshold. Each confirmation_id is single-use and expires a few " +
        "minutes after it was issued.",
      inputSchema: {
        confirmation_id: z.string(),
      },
    },
    async ({ confirmation_id }) => {
      const hold = takeConfirmedAction(confirmation_id);
      const payload =
        hold.action.kind === "open_session"
          ? await performOpenSession(config, hold.action)
          : await performTopUp(config, hold.action);
      return { content: [{ type: "text", text: JSON.stringify(payload, null, 2) }] };
    },
  );
}
