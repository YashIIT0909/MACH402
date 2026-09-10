# CLAUDE.md

Context for AI assistants working in this repo. Read this before writing code.

## What this is

**ClearGate** — a GPU rental marketplace settled with x402 payments on Hedera. Providers run a daemon on an idle GPU and get listed; renters (people or agents) pay that node directly, per job or per minute, and get a container run on it.

Built for the "AI & Agentic Payments on Hedera" hackathon track. Testnet only.

## The four parties

- **Renter** — wants compute. Holds a Hedera key. Pays.
- **Provider node** (`agent/`) — the x402 resource server. Receives. Runs containers.
- **Registry + web** (`registry/`, `web/`) — discovery only. Never touches money.
- **Facilitator** — Blocky402 at `https://api.testnet.blocky402.com`. Co-signs as fee-payer, submits to Hedera. Not ours; do not reimplement.

## Hard invariants — violating any of these is a bug, not a style choice

1. **The agent never holds a private key** and never imports a Hedera SDK. The merchant side of x402 needs only JSON construction and HTTP calls to the facilitator. If you find yourself adding a Hedera SDK to `agent/`, stop.
2. **All payment signing lives in `client/`** using `@x402/hedera`. TypeScript only. There is no reference implementation of the Hedera exact scheme in Go; hand-rolling frozen `TransferTransaction` bytes is out of scope.
3. **The registry never receives, holds, or forwards funds.** Payments are renter → node, direct. Marketplace commission, if any, is an HTS fractional custom fee applied by the network — never a code path.
4. **Verify before work, settle immediately after accepting work.** Never start a container on an unverified payment; never wait for job completion to settle (the signed payload expires at `maxTimeoutSeconds`, 300s).
   - Anything slow — pulling an image, downloading a dataset — happens **after** settlement, with the job in `staging`. Waiting for it before settling would forfeit the payment.
   - Because staging is unrefundable, anything knowable in advance is checked **before the 402**: image allowlist, `require_gpu` against real GPU availability, and a `HEAD` preflight of the dataset URL. A rejection there costs the renter nothing.
   - A job killed while staging must actually stop. `Runner.Kill` cancels the staging context; without that, a settlement failure would leave the node finishing a download and running the job for free.
5. **`extra.feePayer` is read from the facilitator's `GET /supported`, never hardcoded.** It must match, or the client SDK throws before signing.
6. **Untrusted code runs only in the allowlisted-image sandbox**: `--network=none`, resource caps, wall-clock timeout, volume wiped after download. Never widen this to accept arbitrary user images without an explicit decision recorded here.
   - **The container never gets network access, not even for datasets.** A renter supplies a dataset *URL*; the node downloads it and mounts it at `/data`. If you find yourself giving a job container a network so it can fetch something, stop — that is the invariant, not an inconvenience.
   - **Dataset URLs are fetched from inside the provider's network on behalf of a stranger**, so `internal/fetch` blocks loopback, link-local (cloud metadata), private and CGNAT addresses, and re-checks **every redirect hop**. Archives are extracted host-side with path-escape and symlink refusal. Weakening any of this is a vulnerability, not a simplification.
7. **The agent API is versioned (`/v1/...`) and old versions keep working.** Nodes update on their own schedule; the registry records `agent_version`.
8. **Flat-fee job mode stays functional** behind a config flag even after metered leasing lands. It is the demo fallback.

## Layout

```
agent/       Go — cleargate-node binary: x402 resource server, docker runner, dataset staging, TUI
client/      TS — payFor() 402 wrapper, cleargate CLI, budgeting agent (M4)
registry/    TS — Fastify + Postgres, node heartbeats and discovery (M2)
web/         Next.js — provider signup and node browsing (M2); rent flow still to come
packages/    shared TS types (payment requirements, node specs, job specs)
scripts/     install.sh — the provider one-liner
smoke/       the minimal end-to-end payment test; keep it green
examples/    job scripts: hello, train, train_mnist (GPU), and the sandbox negative tests
docs/
```

Built so far: **M0** (payment spike), **M1** (one node, one paid job) and the discovery half of
**M2** (registry, provider signup, node listing).

## Discovery

A node announces itself; the registry never goes looking. `cleargate-node serve` POSTs a heartbeat
to `{registry_url}/v1/nodes/heartbeat` every 30s. Push, not poll, because a provider's box is
usually behind NAT and a registry could not dial it.

**A node is `online` when it has not withdrawn *and* its last beat is under 90s old.** The two
halves cover different failures and both are needed: on shutdown the node POSTs
`/v1/nodes/{id}/offline` so the site stops offering it immediately, and the 90s timeout catches the
node that lost power and never got to say anything. Withdrawal is cleared by the next heartbeat.
Without the explicit withdrawal a provider presses ctrl-c and watches themselves advertised as
available for another minute and a half, which reads as the site ignoring them.

A withdrawn node keeps its row. A renter looking for a node they used yesterday should find it
listed as offline rather than silently vanished.

- **Listing is opt-in and failure is silent.** No `registry_url` in `config.yaml` means the node is
  simply unlisted; renters who know its URL still pay it normally. A registry that is down logs a
  warning and nothing else — it must never interrupt a paid job.
- **A node owns its listing by trust-on-first-use.** `setup` mints a `registry_token`; the first
  heartbeat for a `node_id` records its hash, and later beats must match. Without this, anyone could
  repoint an established listing at their own machine and collect jobs meant for someone else's GPU.
  It is a listing credential only — it cannot move funds, and the node still holds no Hedera key.
- **`public_url` is validated as an absolute http(s) URL at the registry boundary**, because the
  website renders it as a link and a hostile node would otherwise have script injection against
  every visitor.
- `nodespec.Build` is the single source of both `GET /v1/specs` and the heartbeat body, so the two
  cannot drift.

## Payment shapes

**x402 v2 renamed the HTTP headers.** `X-PAYMENT` and `X-PAYMENT-RESPONSE` are v1 names; we
accept them on input for compatibility but never emit them. In v2:

```
client  --PAYMENT-SIGNATURE-->  agent     base64 PaymentPayload
agent   --PAYMENT-REQUIRED-->   client    base64 PaymentRequired, on a 402
agent   --PAYMENT-RESPONSE-->   client    base64 SettleResponse, on a 200
```

The 402 challenge is authoritative **in the header**, not the body. The body is a readable echo
for browsers and for debugging by hand.

402 challenge the agent emits (note `resource`, which is required in v2 and absent from the
Hedera docs' examples):

```json
{
  "x402Version": 2,
  "error": "Payment required",
  "resource": {
    "url": "https://node.example/v1/jobs",
    "description": "One GPU job on ClearGate node node_xxxx",
    "mimeType": "application/json",
    "serviceName": "ClearGate"
  },
  "accepts": [{
    "scheme": "exact",
    "network": "hedera:testnet",
    "amount": "100000",
    "asset": "0.0.0",
    "payTo": "0.0.xxxxxxx",
    "maxTimeoutSeconds": 300,
    "extra": { "feePayer": "<from /supported>" }
  }]
}
```

- `amount` is a **string**, in tinybars for HBAR. 100000 = 0.001 HBAR. (`maxAmountRequired` is
  the v1 field name; v2 uses `amount`.)
- `asset: "0.0.0"` is HBAR; an HTS token id goes there for token settlement.
- The client's payload is `{ x402Version, resource?, accepted, payload: { transaction } }`, where
  `payload.transaction` is a base64 partially-signed `TransferTransaction`.
- The agent POSTs `{ x402Version, paymentPayload, paymentRequirements }` to `/verify` then `/settle`.
- `/verify` returns `{ isValid, invalidReason?, invalidMessage?, payer? }`; `/settle` returns
  `{ success, errorReason?, errorMessage?, payer?, transaction, network }`. `transaction` is
  `0.0.<feePayer>@<seconds>.<nanos>` on Hedera.

**Client spend controls reject HBAR by default.** `x402Client` permits only assets its
`findDefaultAsset` recognizes — on Hedera that is testnet USDC (`0.0.429274`) and nothing else —
capped at `DEFAULT_MAX_AMOUNT_PER_PAYMENT` of `"$1"`. Native HBAR (`"0.0.0"`) is not recognized, so
a stock client silently refuses to pay a ClearGate challenge with no network call and no obvious
error. `client/src/pay.ts` calls `setSpendControls(false)` and then registers its own explicit
policy (HBAR, expected network, under the renter's cap). If a payment appears to do nothing at all,
check this first.

Known settle error worth handling explicitly: `TOKEN_NOT_ASSOCIATED_TO_ACCOUNT` (provider hasn't associated the HTS token).

## Commands

```
make smoke          # end-to-end payment test against testnet — run before every PR
make smoke-agent    # pay the local Go agent with the official TS client
make supported      # check the facilitator still advertises hedera:testnet
make agent          # build the cleargate-node binary
make dev-node       # run an agent locally, headless (what systemd runs)
make dev-tui        # run an agent locally with the provider dashboard
make registry-db    # start the registry's Postgres in docker
make dev-registry   # run the discovery registry on :4400
make dev-web        # run the website on :3000
make cli ARGS="…"   # the renter CLI from the repo root
make test           # go tests, including real-Docker sandbox tests
make typecheck      # typecheck every TS package
```

The dashboard (`cleargate-node tui`) is a **view** over the same runner and server `serve` uses. It
owns no state of its own, so a provider never has to wonder whether it and the daemon disagree.
Because it owns the terminal, the daemon's logs go to `cleargate-node.log` instead of stdout.

## The job lifecycle

```
pending -> staging -> running -> succeeded | failed | timeout | killed
```

`staging` is after payment and before the container runs: pulling the image, downloading the
dataset, uploading it into `/data`. It is reported in `JobState.stage` and streamed to the renter
over the same SSE log feed, so a paid job is never silently paused.

## Conventions

- Go: standard layout, `internal/` for everything non-exported, errors wrapped with context, no panics in request paths.
- The agent talks to Docker over its HTTP API on the unix socket rather than linking the Docker SDK; the binary stays small and dependency-light.
- TS: strict mode, no `any` in payment code paths, shared types imported from `packages/types` rather than redeclared.
- Payment amounts are strings end to end. Never parse them into floats. Never do arithmetic on HBAR in floating point.
- Log every settlement to the local append-only receipt file **and** to HCS. The provider must be able to audit earnings without trusting our website.
- Secrets: only the renter side has a key, and it comes from env (`HEDERA_ACCOUNT_ID`, `HEDERA_PRIVATE_KEY`). Never write a key to disk, never log one, never put one in a config example with a real value.

## When making changes

- Changing the 402 challenge shape is a cross-team break — update `packages/types` and both sides in one PR.
- New agent endpoints go under `/v1/` and must state whether they are free, x402-gated, or job-token-gated.
- If a change makes `make smoke` fail, the change is wrong until proven otherwise.
- Prefer adding behind a config flag over replacing working behaviour; every milestone has a tagged release that must stay demoable.

## Deliberately out of scope

Mainnet. Escrow and refunds (streamed forward-payment removes the need). Multi-GPU partitioning. Arbitrary user-supplied images. Reputation and slashing. Fiat on-ramp. Windows providers.

## Reference

- Blocky402 docs: https://blocky402.com/docs/
- Hedera x402: https://docs.hedera.com/solutions/ai/x402
- Scheme spec: `scheme_exact_hedera.md` in the x402 repo
