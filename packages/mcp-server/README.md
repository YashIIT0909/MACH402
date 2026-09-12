# @cleargate/mcp-server

An MCP server that lets an AI agent shop for, pay for, and use a ClearGate
metered GPU session on its own — the agent equivalent of a human running
`cleargate session` from the CLI. It only ever opens **metered sessions**
(pay → meter → refund unburned credit); it never runs a flat-fee job or a
`direct`-mode lease, and it never touches the escrow contract.

This package is standalone: it has no dependency on any other package in this
repository. Everything it needs is either an ordinary npm dependency or a
small vendored copy, so it can be run with nothing but Node — no cloning this
repo, no `pnpm install` in a monorepo.

## Setup

The only secret this server ever holds is **your own** Hedera key — it never
leaves your machine, and ClearGate never sees it. Get a funded testnet account
at https://portal.hedera.com, then set:

- `HEDERA_ACCOUNT_ID`, `HEDERA_PRIVATE_KEY`, `HEDERA_KEY_TYPE`
- `CLEARGATE_REGISTRY_URL` — the ClearGate registry to search for nodes

See `.env.example` for the full list, including optional spend caps
(`CLEARGATE_MAX_TINYBARS_PER_PAYMENT`, `CLEARGATE_MAX_TINYBARS_PER_DAY`,
`CLEARGATE_CONFIRM_ABOVE_TINYBARS`).

## Running it

```
npx -y @cleargate/mcp-server
```

(Once published — see the repo's `feat/mcp-server` branch notes until then.
From a checkout of this repo, `pnpm --filter @cleargate/mcp-server run start`
runs the same thing.)

It speaks MCP over stdio, so it's meant to be launched by an MCP host, not run
standalone in a terminal.

## Wiring it into an agent

**An MCP-speaking host** (Claude Code, Claude Desktop, or any framework with
built-in MCP client support) just needs one config entry:

- **Claude Code**:
  ```
  claude mcp add cleargate -- npx -y @cleargate/mcp-server
  ```
  with env vars set in your shell or in a `.mcp.json` `env` block.
- **Claude Desktop** — add to `claude_desktop_config.json`:
  ```json
  {
    "mcpServers": {
      "cleargate": {
        "command": "npx",
        "args": ["-y", "@cleargate/mcp-server"],
        "env": {
          "HEDERA_ACCOUNT_ID": "0.0.xxxxxxx",
          "HEDERA_PRIVATE_KEY": "...",
          "CLEARGATE_REGISTRY_URL": "https://your-registry.example"
        }
      }
    }
  }
  ```

Once connected, the host's normal tool discovery picks up every tool below
and the agent's own model decides when to call them — no ClearGate-specific
glue code needed.

**An agent framework that doesn't speak MCP yet** needs a generic MCP client
adapter: use `@modelcontextprotocol/sdk`'s `Client` + `StdioClientTransport`
to spawn this server, call `client.listTools()`, and feed the returned tool
schemas into whatever tool-calling loop that framework already has.

## Tools

| Tool | Payment? | What it does |
|---|---|---|
| `search_compute_nodes` | no | List online nodes that sell metered sessions |
| `get_node_quote` | no | Price a session with no side effects |
| `open_session` | yes | Pay for and open a session; may pause for `confirm_payment` |
| `get_session_access` | no | Poll status, remaining credit, connection info |
| `top_up_session` | yes | Buy another chunk of credit; may pause for `confirm_payment` |
| `stop_session` | no | End the session and trigger the refund of unburned credit |
| `confirm_payment` | yes | Executes a payment `open_session`/`top_up_session` paused |

## Safety

Every paying call goes through: a daily-cap check, then a confirmation gate
for anything above `CLEARGATE_CONFIRM_ABOVE_TINYBARS`, then the per-call cap
enforced inside the payment itself. Every call — paying or not — is appended
to an audit log (`CLEARGATE_MCP_AUDIT_LOG`), and every payment is mirrored to
a local spend ledger (`CLEARGATE_MCP_RECEIPTS`) separate from anything the
`cleargate` CLI writes.

Be honest about the trust shape you're accepting: like the CLI's `session`
command, this is forward payment into a node-held credit with a
provider-published refund trail on HCS, not an escrow. See the main repo's
`CLAUDE.md` ("The session lifecycle") for the full explanation.
