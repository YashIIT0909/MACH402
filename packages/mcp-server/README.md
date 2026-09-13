# @mach402/mcp-server

An MCP (Model Context Protocol) server that lets an AI agent shop for, pay for, and use a metered
GPU session on the ClearGate marketplace entirely on its own — no human running a CLI command, no
human clicking through a website. It only ever opens **metered sessions** (pay a chunk → burn
credit second by second → refund whatever's unused): every node on this network sells that and
nothing else — there are no flat-fee jobs and no unrefundable "direct" leases to worry about.

> **A note on naming.** This package ships under the `@mach402` npm scope and its own env vars use
> a `mach402_` prefix, from an in-progress rename of the wider project. The provider daemon, its
> binary, and the registry are still `cleargate-node` / `@cleargate/*` at the time of writing. Both
> names refer to the same marketplace — use `@mach402/mcp-server` for *this* package and
> `cleargate-node` for the provider commands below; that split will go away once the rename finishes.

## Why this package is standalone

It has **zero dependency on any other package in this repository** — not `client/`, not
`packages/types`, nothing. Everything it needs (the x402 payment signing, the session HTTP client,
the SSH keypair helper) is a small vendored copy under `src/vendor/`, plus ordinary npm packages
(`@x402/hedera`, `@modelcontextprotocol/sdk`, etc.). That means it can be published to npm and run
with nothing but Node — no cloning this monorepo, no `pnpm install` inside it.

## What it can do

| Tool | Costs money? | What it does |
|---|:---:|---|
| `search_compute_nodes` | no | List online nodes that sell metered sessions, filtered by GPU / max price |
| `get_node_quote` | no | Price a session of a given length — no session is created, nothing is charged |
| `open_session` | **yes** | Pay for and open a session; pauses for `confirm_payment` above your confirmation threshold |
| `get_session_access` | no | Poll status, remaining credit, and the connection URL |
| `top_up_session` | **yes** | Buy another chunk of credit before the current one runs out; may also pause for confirmation |
| `stop_session` | no | End the session now — this is what triggers the refund of unburned credit |
| `confirm_payment` | **yes** | Executes a payment `open_session` / `top_up_session` held for confirmation |

Every node currently publishes over a Cloudflare **quick tunnel** — you get back a real, public
Jupyter URL automatically, no local networking to fight with. Quick tunnels carry HTTP only, so
there is **no SSH** on the current build; connection details are a Jupyter link and nothing else.

## Requirements

- Node.js 18+
- Your own funded Hedera **testnet** account — get one free at https://portal.hedera.com. This is
  the only key this server ever holds, and it never leaves your machine.
- A ClearGate/mach402 registry to search (your own local one, or one a provider gave you).

## Configuration

All configuration is environment variables — no config file, ever (the private key especially is
never written to disk beyond a `.env` you control, and never logged). Copy `.env.example` to `.env`
next to wherever you run this from, or set these directly in your MCP host's server config:

| Variable | Required | Default | What it's for |
|---|:---:|---|---|
| `HEDERA_ACCOUNT_ID` | ✅ | — | The renter account this server pays from |
| `HEDERA_PRIVATE_KEY` | ✅ | — | That account's private key. Never logged, never sent anywhere but Hedera. |
| `HEDERA_KEY_TYPE` | | `ecdsa` | `ecdsa` or `ed25519` — match what the portal gave you |
| `mach402_REGISTRY_URL` | ✅ | — | The registry to search with `search_compute_nodes`. No default — never guessed. |
| `mach402_MAX_TINYBARS_PER_PAYMENT` | | `10000000` (0.1 HBAR) | Hard ceiling — a single payment above this is refused outright, confirmed or not |
| `mach402_MAX_TINYBARS_PER_DAY` | | unset (no cap) | Refuses a payment that would push today's total spend past this |
| `mach402_CONFIRM_ABOVE_TINYBARS` | | unset (never pauses) | A payment above this pauses for `confirm_payment` instead of running immediately. Must be ≤ `mach402_MAX_TINYBARS_PER_PAYMENT`, or startup fails with a clear error explaining why |
| `mach402_MCP_RECEIPTS` | | `~/.mach402/mcp-receipts.jsonl` | This server's own append-only spend ledger |
| `mach402_MCP_AUDIT_LOG` | | `~/.mach402/mcp-audit.jsonl` | Append-only log of every tool call, paying or not |

`mach402_CONFIRM_ABOVE_TINYBARS` and `mach402_MAX_TINYBARS_PER_PAYMENT` work together: the first is
a "check with me before spending this much" gate, the second is an absolute ceiling nothing ever
crosses. Keep the confirm threshold at or below the hard cap — setting it higher creates a range of
prices that can never be paid at all, confirmed or not, and the server refuses to start in that
state rather than fail mysteriously later.

## Running it

Once published:
```bash
npx -y @mach402/mcp-server
```
It speaks MCP over stdio and is meant to be launched by an MCP host — it isn't something you run
and watch in your own terminal.

**From a checkout of this repo instead**, run the same thing with:
```bash
pnpm --filter @mach402/mcp-server run start
# or, from the repo root:
make mcp-server
```

### Testing a local build before publishing (Verdaccio)

If you're iterating on this package and want to test the real `npx` install path without pushing to
the public npm registry:
```bash
npm install -g verdaccio
verdaccio &                                     # serves on http://localhost:4873
npm adduser --registry http://localhost:4873    # any local username/password

cd packages/mcp-server
npm publish --registry http://localhost:4873    # requires "private": false in package.json
cd -

npx --registry http://localhost:4873 -y @mach402/mcp-server   # should print a clear config error and exit — that's success
```
Bump the version (`npm version patch --no-git-tag-version`) before republishing — Verdaccio refuses
to overwrite an existing version.

## Setting up a provider node to test against

You need at least one node selling sessions to search for. From the repo root:
```bash
make agent                    # builds bin/cleargate-node
make lease-image              # builds the session runtime + egress proxy (needs Docker)

./bin/cleargate-node setup \
  --pay-to 0.0.<PROVIDER_ACCOUNT_ID> \
  --registry-url http://localhost:4400 \
  --public-url http://localhost:8402
# prints an operator EVM address — fund it with testnet HBAR from the portal faucet,
# setup polls until it sees the balance land

./bin/cleargate-node serve
```
Every node sells metered sessions unconditionally now — there's no `--enable-leases`,
`--enable-hcs`, `--enable-sessions`, or `--tunnel-mode` to set; those flags still exist for old
scripts but do nothing. `--registry-url`/`--public-url` are optional — omit both to run an unlisted
node reachable only if you already know its URL.

For `search_compute_nodes` to find that node, bring up the registry too:
```bash
make registry-db     # Postgres, via docker compose
make dev-registry     # registry on :4400
# or both in one command:
make registry-up
```

## Wiring it into an agent

**An MCP-speaking host** (Claude Code, Claude Desktop, or any framework with built-in MCP client
support) just needs one config entry.

**Claude Code**, adding it project-local (default `--scope local`, so it never ends up in a
committed `.mcp.json` — keep `HEDERA_PRIVATE_KEY` out of anything checked into git):
```bash
claude mcp add \
  -e HEDERA_ACCOUNT_ID=0.0.<your account> \
  -e HEDERA_PRIVATE_KEY=<your private key> \
  -e HEDERA_KEY_TYPE=ecdsa \
  -e mach402_REGISTRY_URL=http://localhost:4400 \
  -e mach402_CONFIRM_ABOVE_TINYBARS=20000000 \
  --transport stdio \
  --scope local \
  mach402 -- npx -y @mach402/mcp-server
```
Verify with `claude mcp list` / `claude mcp get mach402`; remove with `claude mcp remove mach402`.
(Testing against a Verdaccio-published build instead of the real registry? Add
`-e NPM_CONFIG_REGISTRY=http://localhost:4873` to that same command — it scopes the registry
override to just this one subprocess.)

**Claude Desktop** — add to `claude_desktop_config.json`:
```json
{
  "mcpServers": {
    "mach402": {
      "command": "npx",
      "args": ["-y", "@mach402/mcp-server"],
      "env": {
        "HEDERA_ACCOUNT_ID": "0.0.xxxxxxx",
        "HEDERA_PRIVATE_KEY": "...",
        "HEDERA_KEY_TYPE": "ecdsa",
        "mach402_REGISTRY_URL": "https://your-registry.example"
      }
    }
  }
}
```

**An agent framework that doesn't speak MCP yet** needs a generic MCP client adapter: use
`@modelcontextprotocol/sdk`'s `Client` + `StdioClientTransport` to spawn this server, call
`client.listTools()`, and feed the returned schemas into whatever tool-calling loop that framework
already runs.

Once connected, the host's normal tool discovery picks up every tool in the table above and the
agent's own model decides when to call them — there's no marketplace-specific glue code to write.

## Safety

Every paying call goes through, in order: a daily-cap check, a confirmation gate for anything above
`mach402_CONFIRM_ABOVE_TINYBARS`, then the hard per-call cap enforced inside the payment itself.
Every call — paying or not — lands in the audit log; every successful payment is separately mirrored
to the spend ledger. Both are kept apart from anything a human-facing tool writes, so an agent's
activity is never invisibly mixed into someone else's records.

Be honest with yourself about the trust shape you're accepting: this is forward payment into a
node-held credit with a provider-published refund trail on Hedera Consensus Service, **not an
escrow**. The provider genuinely holds your unburned credit between chunks; what makes that
checkable is that the node publishes what it owes you, continuously, to its own audit topic — not
that a contract holds the money instead. See the main repo's `CLAUDE.md` ("The session lifecycle")
for the full explanation.

## Troubleshooting

**`open_session` fails with a bare `402 Payment Required`, no confirmation pause.** This means a
real, signed payment was attempted and rejected — not a config-side refusal. In order of likelihood:
1. The renter account (`HEDERA_ACCOUNT_ID`) has no testnet HBAR. Check:
   ```bash
   curl -s "https://testnet.mirrornode.hedera.com/api/v1/accounts/0.0.<ACCOUNT_ID>"
   ```
2. `HEDERA_KEY_TYPE` doesn't match the actual key (`ecdsa` vs `ed25519`).
3. `mach402_CONFIRM_ABOVE_TINYBARS` was set below `mach402_MAX_TINYBARS_PER_PAYMENT` incorrectly —
   startup now refuses to boot in that state, so if the server started at all, this specific
   combination isn't your problem.

The node's actual rejection reason (e.g. `settlement failed: INSUFFICIENT_PAYER_BALANCE`) is
included in the thrown error's message — read the full tool error, not just its first line.

**No SSH in the connection details.** Expected on the current build — every session publishes over
a Cloudflare quick tunnel, which is Jupyter-only by design (it carries HTTP, not raw TCP). Use the
Jupyter URL.

**`search_compute_nodes` returns nothing.** Confirm the registry is reachable
(`curl $mach402_REGISTRY_URL/v1/nodes?online=true`) and that at least one node's heartbeat has
landed — heartbeats are pushed every 30s, so a freshly started node can take up to that long to
appear.
