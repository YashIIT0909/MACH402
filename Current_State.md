# ClearGate — Current State

A snapshot of what is built, what is wired together, and what is still open. For onboarding and
setup, see `README.md`; for the invariants that must never be violated, see `CLAUDE.md`. This file
is the "where are we" view — read it before planning the next piece of work.

Last updated: 2026-09-12, against `main` at `5bc7398` plus the sessions-only change.

---

## Milestone status

| Milestone | Scope | Status |
|---|---|---|
| **M0** | Payment spike — prove one x402 payment settles on Hedera testnet | ✅ Done. `make smoke` is green and kept as a CI regression test. |
| **M1** | One provider node, one paid flat-fee job | ✅ Done, then removed. Batch jobs, dataset staging and the job sandbox are gone; GPU passthrough with CPU fallback and the provider TUI remain. |
| **M2 (discovery half)** | Registry + website, opt-in listing | ✅ Done. Heartbeat-based discovery, trust-on-first-use listing ownership, provider signup page, node browsing page. |
| **M2 (rent-from-website half)** | Paying for a session from the browser | ✅ Done. Browser wallet signing and a rent flow on the site. |
| **M3** | Interactive leases (a Jupyter server in a container) | ✅ Done. Certificate-based access, cloudflared quick tunnels, freeze-then-reap. Sold only as M4's metered sessions now — the prepaid, non-refundable mode was removed. |
| **M4** | Refundable sessions, HCS audit trail, ERC-8004 identity | ✅ Done, then reworked. Sessions are now **metered x402 chunks with a published refund-owed trail**, not contract escrow; `SessionEscrow` is parked. `IdentityRegistry`, HCS publishing and `register` are unchanged. |

Nothing is mainnet. Everything above is testnet-only by design (see CLAUDE.md's "Deliberately out
of scope").

---

## What exists, end to end

### `agent/` (Go) — the provider node binary, `cleargate-node`

Commands: `setup`, `serve`, `tui`, `register`, `hedera` (sidecar diagnostics), plus `earnings`.

Routes actually registered (`agent/internal/httpapi/server.go`):

```
GET  /health
GET  /v1/specs
POST /v1/sessions                  x402-gated, priced per chunk
POST /v1/sessions/{id}/topup       x402-gated + session-token-gated
GET  /v1/sessions/{id}             token-gated
POST /v1/sessions/{id}/stop        token-gated
GET  /.well-known/agent-card.json  free, unauthenticated
```

Sessions answer `404` on a node whose leasing is off or whose sidecar did not start. The batch job
routes (`/v1/jobs`) and the prepaid `/v1/leases` routes were removed. There is no session *quote* endpoint: the price of a given payment is the ordinary x402
challenge on the 402, and the shopping-ahead terms are in `leases` on the free `/v1/specs`.

Internal packages, and what each owns:

- `internal/x402` — challenge building, facilitator client (`/supported`, `/verify`, `/settle`), v1/v2 header compatibility.
- `internal/runner` — Docker-over-HTTP lease containers (internal Docker network + `cleargate-egress` proxy), freeze and reap.
- `internal/nodespec` — single source of truth for `/v1/specs` and the heartbeat body, so they cannot drift.
- `internal/sshca` — the node's own SSH CA; signs one certificate per lease, `principal = lease_id`.
- `internal/tunnel` — supervises `cloudflared` quick tunnels (Jupyter only, no SSH).
- `internal/mirror` — read-only Hedera mirror-node client. No key. Resolves accounts and EVM addresses, retries through ingestion lag. Used by setup and by the parked escrow path.
- `internal/escrow` — **parked** with `contracts/SessionEscrow.sol`. Only `PricePerSecond` is still live; the session path uses it.
- `internal/hedera` — runs `cleargate-hedera` (from `hederakit/`) as a child process for the operations that need a signature: HCS submission, ERC-8004 registration, and the HBAR transfer that refunds a session. Links no Hedera SDK.
- `internal/hcs` — builds and publishes settlement receipts *and the running session burn checkpoints* to the provider's own audit topic.
- `internal/registry` — the heartbeat client that pushes this node's listing every 30s.

### `client/` (TypeScript) — payment signing for the website

- `payment.ts` — the payer: `@x402/hedera` exact-scheme signing wrapped around `fetch`, with spend controls explicitly reconfigured to allow HBAR (the SDK's stock defaults only recognize testnet USDC) and a per-payment cap. Isomorphic, and the only copy of that policy.
- `browser/` — the entry the website imports (`@cleargate/client/browser`): a wallet-backed signer and in-browser SSH key generation for leases.
- `escrow.ts` — **parked** alongside the contract, with `env.ts` for its key. Nothing on the default path imports it.

### `registry/` (TypeScript, Fastify + Postgres)

- Heartbeat ingestion (`/v1/nodes/heartbeat`), trust-on-first-use `registry_token` ownership, online/offline logic (last-beat-under-90s AND not explicitly withdrawn).
- `providers.ts` — records each node's ERC-8004 `agentId` alongside its listing.
- Never touches money in any code path — enforced by construction, since nothing here talks to Docker, the facilitator, or a Hedera key.

### `web/` (Next.js)

- `/` — landing page.
- `/provide` — hands a provider their install command.
- `/nodes` — lists every node from the registry: GPU, session price, container, payout account.
- `/rent/[id]` — rent a session, paid from the renter's own wallet.

### `contracts/` (Solidity, Hardhat) — deployed to Hedera testnet

- `SessionEscrow.sol` — `openSession` / `topUp` / `settle` / `quoteSettlement` / `withdraw`. Immutable, no owner, no pause, no upgrade path. `settle` is permissionless and one-shot; a failed payout becomes a pull (`pendingWithdrawal`) rather than reverting, so one misconfigured address can never brick the contract.
- `IdentityRegistry.sol` — ERC-8004 `newAgent`, bound to `msg.sender`; idempotent at the contract level (`agentIdOf` checked before minting).
- Unit tests run in-process (no live network, no HBAR spent): `make contracts-test`.
- Deployment addresses live in `contracts/deployments/hederaTestnet.json`.

### `hederakit/` (TypeScript) — the signing sidecar, `cleargate-hedera`

The only package other than `client/` that holds a Hedera key, and the only thing the agent ever
shells out to for a signature. Its own binary (`hederakit/bin/cleargate-hedera.mjs`) is invoked as a
child process by `agent/internal/hedera`; the Go binary reads JSON back over stdout and never
touches the key file itself.

### `packages/types/` (TypeScript)

Shared wire types for node specs, lease offers, sessions, and x402 payment shapes — imported by both the agent's
TS-facing tooling and the client/registry, so the 402 challenge shape, lease spec, and session spec
cannot drift between components silently.

---

## How interactive time is paid for

Only as a metered session (`payment_mode: session`). A prepaid mode that bought minutes of wall clock
with no refund (`payment_mode: direct`, `/v1/leases`) was removed; a config still naming it is
refused at load.

| | Metered sessions |
|---|---|
| Who signs | Renter signs a `TransferTransaction` |
| Who submits | The Blocky402 facilitator (co-signs as fee payer) |
| Verified by | Facilitator's `/verify` |
| What a payment buys | A credit in tinybars, burned by the second |
| Settled by | Facilitator's `/settle`, immediately after the container is proven reachable |
| Money sits | Provider's `pay_to`, immediately — partly owed back |
| Refundable | Yes — the unburned credit, transferred back automatically at stop or reap |
| Max exposure | One chunk (`session_chunk_seconds`, default 300s) |
| What backs the refund | The provider's own transfer, against a figure they publish to HCS every 15s |

The honest framing: this is **forward payment with a provider-issued refund**,
not escrow. Between a chunk settling and the refund going out, the node holds money that is partly
the renter's. What makes that checkable is `KindSessionBurn` — the node publishes `refund_tinybars`,
what it owes if the session stopped right now, on every 15-second sweep, to a consensus-ordered
topic it cannot edit. A provider who later refuses to refund is refusing a number they signed
repeatedly, in advance, with nothing to gain by lying about it at the time.

The stronger guarantee — where the provider never holds the renter's money at all — is what
`contracts/SessionEscrow.sol` gave. It is parked rather than deleted, behind a future
`payment_mode: escrow-vault`.

## Two identities that are easy to confuse

- **`pay_to`** — an arbitrary Hedera account the provider names in `config.yaml`. Holds earnings.
  Signs nothing. The node never has its key. It is where every settlement lands, sessions included —
  which is why a session refund comes out of the operator account instead: the node cannot move money
  out of `pay_to`.
- **The operator key** (`hedera.operator_key_path`) — a separate, node-local account generated at
  setup, read only by the `cleargate-hedera` sidecar. Pays gas for HCS submissions and ERC-8004
  registration, and on any node selling sessions **pays refunds out of its own balance**.
  That last one is a real change to what this key is for: it is no longer a gas-only float, and a
  session node has to keep enough there to cover an outstanding chunk. A
  compromise still costs only that balance, never the earnings in `pay_to`.

---

## Known gaps / explicitly deferred

- **A session refund depends on the provider.** The node pays it automatically, but the transfer
  can still fail — an unfunded operator account, a sidecar that will not start — and the sweep
  retries while the debt stays `pending` until it succeeds. This
  is the cost of dropping the contract, and the published burn trail is the mitigation, not a fix.
- **No end-to-end session smoke test.** `make smoke` covers the payment path against a reference server; a
  real open → tick → top-up → stop cycle against testnet needs a lease-capable node (built lease
  image, cloudflared, funded operator key, HCS topic) and has not been automated. The meter, the
  burn trail and the accounting identity are covered by Go unit tests in
  `agent/internal/httpapi/session_meter_test.go`.
- **No registry keeper.** Nothing external closes a session; the node's own 15-second sweep meters,
  freezes, reaps and refunds. The registry still holds no key and runs no background work.
- **Single active lease/session per node.** No multi-tenant GPU partitioning in V1.
- **Arbitrary user images are out of scope.** Leases run only the one image the node itself built (`leases.image`).
- **Windows providers, mainnet, reputation/slashing, fiat on-ramp** — all explicitly out of scope per
  `CLAUDE.md`.

---

## Where to look first for common questions

- "How does a payment actually move?" → `README.md`'s "The payment flow" section, or `CLAUDE.md`'s
  "Payment shapes."
- "Why can't the agent hold a key?" → `CLAUDE.md`, invariant 1, and `internal/hedera`'s doc comment.
- "How is a session's refund decided?" → `Lease.Burn` in `agent/internal/runner/lease.go`, and
  `tickMeter`/`settleSession` in `agent/internal/httpapi/sessions.go`.
- "What stops a provider just keeping the money?" → nothing absolutely; see the burn trail in
  `tickMeter` and the honest framing in the payment-paths table above.
- "What's actually running right now vs. planned?" → this file.
