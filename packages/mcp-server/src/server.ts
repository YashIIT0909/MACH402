/**
 * `cleargate-mcp` — an MCP server exposing ClearGate's metered GPU sessions as
 * tools, so an AI agent can shop for, pay for, and use compute without a
 * human running the `cleargate` CLI by hand.
 *
 * Sessions only (CLAUDE.md: no flat-fee job tools here), no escrow contract
 * (uses the live session-metering + HCS-published refund trail), and no
 * dependency on any other package in this repo — see package.json.
 */
import { McpServer } from "@modelcontextprotocol/sdk/server/mcp.js";
import { StdioServerTransport } from "@modelcontextprotocol/sdk/server/stdio.js";
import { loadConfig } from "./config.js";
import { registerSearchComputeNodes } from "./tools/searchComputeNodes.js";
import { registerGetNodeQuote } from "./tools/getNodeQuote.js";
import { registerOpenSession } from "./tools/openSession.js";
import { registerGetSessionAccess } from "./tools/getSessionAccess.js";
import { registerTopUpSession } from "./tools/topUpSession.js";
import { registerStopSession } from "./tools/stopSession.js";
import { registerConfirmPayment } from "./tools/confirmPayment.js";

const config = loadConfig();

const server = new McpServer({ name: "cleargate-mcp", version: "0.1.0" });

registerSearchComputeNodes(server, config);
registerGetNodeQuote(server);
registerOpenSession(server, config);
registerGetSessionAccess(server);
registerTopUpSession(server, config);
registerStopSession(server);
registerConfirmPayment(server, config);

const transport = new StdioServerTransport();
await server.connect(transport);
