import { z } from "zod";
import type { McpServer } from "@modelcontextprotocol/sdk/server/mcp.js";
import type { McpConfig } from "../config.js";
import { SessionClient } from "../vendor/sessionClient.js";
import { createPayer } from "../vendor/payer.js";
import { recordSpend } from "../vendor/receipt.js";
import { logAudit } from "../safety/audit.js";
import {
  checkDailyCap,
  needsConfirmation,
  holdForConfirmation,
  confirmationPayload,
  type TopUpAction,
} from "../safety/limits.js";

async function quoteTopUp(client: SessionClient, sessionId: string, token: string): Promise<bigint> {
  const quote = await client.quoteTopUp(sessionId, token);
  return BigInt(quote.accepts[0]?.amount ?? "0");
}

/** Actually pays for the top-up. Shared by the direct path and confirm_payment. */
export async function performTopUp(
  config: McpConfig,
  params: TopUpAction,
): Promise<Record<string, unknown>> {
  const { nodeUrl, sessionId, token } = params;
  const client = new SessionClient(nodeUrl);
  const amount = await quoteTopUp(client, sessionId, token);

  const payer = createPayer({ network: config.network, maxTinybarsPerPayment: config.maxTinybarsPerPayment });
  const { state, settlement } = await client.topUpSession(payer, sessionId, token);
  recordSpend(settlement, nodeUrl, sessionId, amount.toString());
  logAudit({
    tool: "top_up_session",
    node_url: nodeUrl,
    session_id: sessionId,
    amount_tinybars: amount.toString(),
    outcome: "paid",
  });

  return {
    session_id: state.session_id,
    status: state.status,
    expires_at: state.expires_at,
    seconds_remaining: state.seconds_remaining,
    credit_tinybars: state.credit_tinybars,
    low_credits: state.low_credits,
  };
}

export function registerTopUpSession(server: McpServer, config: McpConfig): void {
  server.registerTool(
    "top_up_session",
    {
      title: "Top up a session's credit",
      description:
        "Pay for another chunk of credit on a live session. If the cost is above the configured " +
        "confirmation threshold, this pauses and returns a confirmation_id instead of paying — call " +
        "confirm_payment with it to proceed.",
      inputSchema: {
        node_url: z.string().url(),
        session_id: z.string(),
        token: z.string(),
      },
    },
    async ({ node_url, session_id, token }) => {
      const client = new SessionClient(node_url);
      const amount = await quoteTopUp(client, session_id, token);
      checkDailyCap(config, amount);

      if (needsConfirmation(config, amount)) {
        const hold = holdForConfirmation(amount, {
          kind: "top_up_session",
          nodeUrl: node_url,
          sessionId: session_id,
          token,
        });
        return { content: [{ type: "text", text: JSON.stringify(confirmationPayload(hold), null, 2) }] };
      }

      const payload = await performTopUp(config, {
        kind: "top_up_session",
        nodeUrl: node_url,
        sessionId: session_id,
        token,
      });
      return { content: [{ type: "text", text: JSON.stringify(payload, null, 2) }] };
    },
  );
}
