import { z } from "zod";
import type { McpServer } from "@modelcontextprotocol/sdk/server/mcp.js";
import type { McpConfig } from "../config.js";
import { SessionClient } from "../vendor/sessionClient.js";
import { createPayer } from "../vendor/payer.js";
import {
  createSshIdentity,
  discardSshIdentity,
  saveCertificate,
  sshCommand,
  colabUrl,
  localColabUrl,
  type SshIdentity,
} from "../vendor/sshIdentity.js";
import { recordSpend } from "../vendor/receipt.js";
import { logAudit } from "../safety/audit.js";
import {
  checkDailyCap,
  needsConfirmation,
  holdForConfirmation,
  confirmationPayload,
  type OpenSessionAction,
} from "../safety/limits.js";
import type { SessionCreated } from "../vendor/types.js";

async function quoteOpenSession(
  nodeUrl: string,
  seconds: number,
  requireGpu?: boolean,
): Promise<bigint> {
  const client = new SessionClient(nodeUrl);
  const identity = createSshIdentity();
  try {
    const quote = await client.quoteSession({
      seconds,
      public_key: identity.publicKey,
      require_gpu: requireGpu,
    });
    return BigInt(quote.accepts[0]?.amount ?? "0");
  } finally {
    discardSshIdentity(identity);
  }
}

function accessPayload(session: SessionCreated, identity: SshIdentity, localPort: number) {
  return {
    session_id: session.session_id,
    token: session.token,
    status: session.status,
    expires_at: session.expires_at,
    ssh_user: session.ssh_user,
    ssh_host: session.ssh_host,
    ssh_principal: session.ssh_principal,
    tunnel_mode: session.tunnel_mode,
    ssh_command: sshCommand(session, identity, localPort) || undefined,
    jupyter_url: colabUrl(session),
    jupyter_url_local: session.ssh_host ? localColabUrl(session, localPort) : undefined,
    credit_tinybars: session.credit_tinybars,
    price_tinybars_per_second: session.price_tinybars_per_second,
    seconds: session.seconds,
    identity_key_path: identity.keyPath,
  };
}

/** Actually pays for and opens the session. Shared by the direct path and confirm_payment. */
export async function performOpenSession(
  config: McpConfig,
  params: OpenSessionAction,
): Promise<Record<string, unknown>> {
  const { nodeUrl, seconds, requireGpu, localPort } = params;
  const client = new SessionClient(nodeUrl);
  const identity = createSshIdentity();

  const payer = createPayer({ network: config.network, maxTinybarsPerPayment: config.maxTinybarsPerPayment });
  const { session, settlement } = await client.createSession(payer, {
    seconds,
    public_key: identity.publicKey,
    require_gpu: requireGpu,
  });
  saveCertificate(identity, session.certificate);
  recordSpend(settlement, nodeUrl, session.session_id, session.amount_tinybars);
  logAudit({
    tool: "open_session",
    node_url: nodeUrl,
    session_id: session.session_id,
    amount_tinybars: session.amount_tinybars,
    outcome: "paid",
  });

  return accessPayload(session, identity, localPort ?? 8888);
}

export function registerOpenSession(server: McpServer, config: McpConfig): void {
  server.registerTool(
    "open_session",
    {
      title: "Open a metered GPU session",
      description:
        "Pay for and open a metered interactive session: an SSH + Jupyter box on a provider's GPU, " +
        "billed second by second, refunding unused credit on stop_session. If the cost is above the " +
        "configured confirmation threshold, this pauses and returns a confirmation_id instead of " +
        "paying — call confirm_payment with it to proceed.",
      inputSchema: {
        node_url: z.string().url(),
        seconds: z.number().int().positive(),
        require_gpu: z.boolean().optional(),
        local_port: z.number().int().positive().optional(),
      },
    },
    async ({ node_url, seconds, require_gpu, local_port }) => {
      const amount = await quoteOpenSession(node_url, seconds, require_gpu);
      checkDailyCap(config, amount);

      if (needsConfirmation(config, amount)) {
        const hold = holdForConfirmation(amount, {
          kind: "open_session",
          nodeUrl: node_url,
          seconds,
          requireGpu: require_gpu,
          localPort: local_port,
        });
        return { content: [{ type: "text", text: JSON.stringify(confirmationPayload(hold), null, 2) }] };
      }

      const payload = await performOpenSession(config, {
        kind: "open_session",
        nodeUrl: node_url,
        seconds,
        requireGpu: require_gpu,
        localPort: local_port,
      });
      return { content: [{ type: "text", text: JSON.stringify(payload, null, 2) }] };
    },
  );
}
