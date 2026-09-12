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
   - **The sidecar is the one sanctioned way around this, and it is not an exception to the rule — it is how the rule is kept.** Publishing an HCS receipt and registering an ERC-8004 identity both need a signed Hedera transaction, and the Go binary still links no SDK and reads no key: it runs `cleargate-hedera` (the `hederakit/` package) as a child process and reads JSON back. This is the same move `internal/sshca` makes with `ssh-keygen` and `internal/tunnel` makes with `cloudflared`. `agent/internal/hedera` is that wrapper; adding a Hedera SDK to `go.mod` is still the thing not to do.
   - The key that sidecar reads is **node-local, opt-in, and is not `pay_to`.** `pay_to` holds a provider's earnings and signs nothing, ever. The operator key signs topic submissions and ERC-8004 registration, and — only with `leases.self_settle` — the HBAR transfer that refunds a metered session's unburned credit. Do not describe it as a zero-value or gas-only key: with `self_settle` on it has to hold enough to cover an outstanding session chunk, so it can genuinely spend. The honest claim is that a compromise costs that float and cannot touch earnings, which sit in `pay_to`.
   - **Reading the chain needs no key at all.** `agent/internal/mirror` reads Hedera's public mirror node over plain HTTP, which is what let the (now parked) escrow path verify a deposit in Go without breaking this invariant. Metered sessions do not read the chain to take payment — they use the ordinary facilitator cycle — but they do use the sidecar to *pay a refund*, and that is money out of the operator account rather than only gas. See the session lifecycle.
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
   - **Lease containers are the one recorded exception, and only in the ways an interactive session forces.** This is that explicit decision. A lease (`POST /v1/leases`) hands a paying stranger a shell, so `--network=none` and a read-only root are both impossible — a renter has to be able to `pip install`. What replaces them is stricter, not looser, and all four parts are load-bearing:
     1. the container sits on a Docker network created with `Internal: true`, so it has **no route off the host at all**;
     2. the only thing dual-homed on that network is `cleargate-egress`, a proxy built from source in this repo (`agent/lease-image/egress`) that **denies by default** and allows only package and model registries. A renter can unset `HTTP_PROXY` and gain nothing: there is no other path out to find;
     3. every capability is dropped except the handful `sshd` needs to accept a login, plus `no-new-privileges`. Providers are directed to user-namespace remapping so in-container root is not host root;
     4. the workspace volume and the whole network are destroyed at reap.

     The lease image itself is still not arbitrary — it is one image the node built, named in `leases.image`.
7. **A lease's access is a certificate, never a key.** The renter generates a keypair on their machine and sends only the public half; the node's own CA (`internal/sshca`, private key node-local, generated at `setup --enable-leases`) signs it with `principal = lease_id` and `ValidBefore = expires_at`. Extending re-signs a fresh certificate rather than mutating the old one, so **expiry is the revocation mechanism** and nothing needs actively revoking in the normal case. Neither side ever holds the other's secret — the same rule as the node never holding a Hedera key.
8. **The agent API is versioned (`/v1/...`) and old versions keep working.** Nodes update on their own schedule; the registry records `agent_version`.
9. **Flat-fee job mode stays functional** behind a config flag even after metered leasing lands. It is the demo fallback. Equally, **leasing is opt-in per provider** (`setup --enable-leases`, `LEASES=1`) and must never be switched on as a side effect of anything else — a node that did not opt in answers 404 on `/v1/leases` and behaves exactly as it did before leases existed.

## Layout

```
agent/       Go — cleargate-node binary: x402 resource server, docker runner, dataset staging, TUI
  internal/sshca/       the node's own SSH CA: per-lease certificates, private key never leaves
  internal/tunnel/      cloudflared supervision — how a renter reaches a lease through NAT
  lease-image/          the lease runtime (sshd + Jupyter) and its deny-by-default egress proxy
  internal/mirror/      Hedera's public mirror node, read-only — how a deposit is verified without a key
  internal/escrow/      parked: the escrow-vault fallback's on-chain reader (PricePerSecond still live)
  internal/hedera/      runs the signing sidecar as a child process; links no SDK
  internal/hcs/         publishes settlements to the provider's own audit topic
client/      TS — payFor() 402 wrapper, cleargate CLI (escrow.ts parked)
contracts/   Solidity — IdentityRegistry (ERC-8004) and SessionEscrow (parked; see below), Hardhat
hederakit/   TS — cleargate-hedera, the sidecar the node shells out to; the only thing holding a provider key
registry/    TS — Fastify + Postgres, node heartbeats, discovery (M2), tunnel provisioning
web/         Next.js — provider signup and node browsing (M2); rent flow still to come
packages/    shared TS types (payment requirements, node specs, job specs, lease specs)
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
make lease-image    # build the lease runtime + egress proxy (required before a node sells leases)
make dev-node       # run an agent locally, headless (what systemd runs)
make dev-tui        # run an agent locally with the provider dashboard
make registry-db    # start the registry's Postgres in docker
make dev-registry   # run the discovery registry on :4400
make dev-web        # run the website on :3000
make cli ARGS="…"   # the renter CLI from the repo root
make test           # go tests, including real-Docker sandbox tests
make typecheck      # typecheck every TS package
make contracts-test # unit-test SessionEscrow and IdentityRegistry — in-process EVM, no network, no HBAR
make contracts-deploy   # deploy both contracts to Hedera testnet
make contracts-demo     # prove the refund end to end against the live contract, with no node involved
make escrow-selectors   # regenerate the function selectors agent/internal/escrow carries as constants
make node-register      # register this node's ERC-8004 provider identity
```

The dashboard (`cleargate-node tui`) is a **view** over the same runner and server `serve` uses. It
owns no state of its own, so a provider never has to wonder whether it and the daemon disagree — the
one exception is a ledger seeded from `receipts.jsonl` at startup, which is not a second opinion but
the same authoritative file `cleargate-node earnings` reads, so a restart does not appear to zero a
provider's earnings. Because it owns the terminal, the daemon's logs go to `cleargate-node.log`
instead of stdout.

It is five screens rather than one stack: **Overview** (earnings by window, GPU load, what the node
sells, and whether anyone can find it), **Jobs** (table, detail and a live log pane), **Leasing**
(the session running now, its credit and what is owed back, how the renter is connected, terms,
history), **Activity** (the whole event feed, filterable and scrollable) and **Node** (the
configuration and trust posture actually in force). Two things it can do that no other local surface
can: report that the registry is refusing this node's heartbeats — the daemon logs that at warn level
and keeps selling, which is correct and silent — and evict the lease running right now, via
`Server.EndLease`, which reuses the sweep's reap-and-settle order so a metered session is charged for
the seconds it used and refunded the rest. `x` and `e` both ask a second time before acting.

## The job lifecycle

```
pending -> staging -> running -> succeeded | failed | timeout | killed
```

`staging` is after payment and before the container runs: pulling the image, downloading the
dataset, uploading it into `/data`. It is reported in `JobState.stage` and streamed to the renter
over the same SSE log feed, so a paid job is never silently paused.

## The lease lifecycle

```
provisioning -> active -> paused -> active
                     \         \-> expired
                      \-> stopped | failed
```

A lease is the inverse trade of a job: nothing of the renter's is uploaded, and instead they get a
shell and a Jupyter server on the provider's GPU for the minutes they bought. One active lease per
node in V1.

- **`POST /v1/leases` settles only after a reachability check.** The order is verify → start the
  container → sign the certificate → point the tunnel at it → *prove it answers* → settle. A renter
  is never charged for a lease that never came up. This is why the lease image must already be on
  the box (checked before the 402): there is no `staging` phase to hide an image pull in, and the
  signed payload expires at `maxTimeoutSeconds`.
- **Two missed extensions freeze, they do not kill.** `docker pause` — the cgroup freezer — so a
  renter who is mid-run and slow to pay gets their session back when they extend. A grace period
  after that reaps: container killed, tunnel ingress withdrawn, workspace wiped.
- **Extending buys a slice and re-signs.** `POST /v1/leases/:id/extend` is 402-gated the same way
  and issues a new certificate with the later expiry; the old one lapses on its own.
- **Stopping does not refund — in `direct` mode.** Forward payment is what removes the need for
  escrow there. What stopping does is end the meter, which is what the renter actually wants and what
  frees the node for the next one. A node selling metered `session`s behaves differently; see below.
- **The renter's `--budget` is enforced client-side**, in `holdLease`, and is a different mechanism
  from the node's freeze/reap timers: one protects the renter from overspending, the other protects
  the provider from an unpaid container.

- **A GPU on the host is not a GPU in a lease.** `runner.LeaseGPU()` is `gpu.Available` *and* the
  lease image carrying a CUDA runtime, checked from the image's declared env at startup. A node with
  a working card and a `python:3.11-slim`-based lease image gives a renter a container where
  `nvidia-smi` lists the device and every kernel launch fails. So `require_gpu` is refused before the
  402, `LeaseOffer.GPU` advertises the narrow answer, and `make lease-image` picks its base from
  whether the `nvidia` runtime is present rather than from the provider remembering a flag. Never
  widen the advertised value back to the host-level one.

Reaching the container is `internal/tunnel`, and it has two modes with a real difference a renter
must be told about before paying: `named` (registry-provisioned Cloudflare tunnel, stable hostnames,
**SSH and Jupyter**) and `quick` (`cloudflared tunnel --url`, no Cloudflare account at all, random
hostname, **Jupyter only — no SSH**, because a quick tunnel carries no TCP). `nodespec.LeaseOffer.SSH`
is what advertises which.

Cloudflare credentials live **only** in the registry's environment
(`CLOUDFLARE_API_TOKEN`, `CLOUDFLARE_ACCOUNT_ID`, `CLOUDFLARE_ZONE_ID`, `LEASE_DOMAIN`). Providers
never have a Cloudflare account; `POST /v1/nodes/:id/tunnel-token` provisions one on their behalf and
returns a token good for running that one tunnel. A registry without those variables is still a fine
registry — it just answers 503 there and nodes fall back to quick tunnels.

## The session lifecycle — metered, refundable interactive time

A session is a lease whose payment buys **credit** rather than time. Same container, same
certificate, same freeze-and-reap; the difference is that the node burns the credit second by second
and owes back whatever is unburned. `leases.payment_mode` selects it (`session`; `escrow` is the old
spelling and normalizes to it), `direct` stays the default, and a node that never opted in answers
404 on `/v1/sessions`.

- **Be honest about the trust shape.** This is forward payment with a provider-issued refund, not an
  escrow. Between a chunk settling and the refund going out, the node is holding money that is partly
  the renter's, and nothing but the node's own bookkeeping says how much. Anyone describing this as
  trustless or as escrow is describing the parked `contracts/SessionEscrow.sol`, not this path.
- **The burn checkpoint is the mitigation, and it is load-bearing.** `tickMeter` publishes
  `KindSessionBurn` carrying `refund_tinybars` — what the node owes if the session stopped right now
  — on **every** 15-second sweep, not only at open and close. That converts the provider's private
  balance into a consensus-ordered, running-hash-bound public fact, recorded continuously and before
  any dispute exists. A provider who later refuses to refund is refusing a number they signed
  repeatedly at a time when they had no motive to shade it. Because this is what makes prepaying a
  stranger checkable at all, `hcs.enabled` is **required** for session mode and refused at config
  load, not warned about at runtime.
- **The chunk cap bounds the exposure.** `leases.session_chunk_seconds` (default 300) is the most
  time one payment ever buys, whatever the renter asked for; they get a chunk and top up. Widening
  it widens the only real gap in this design, so it is not a performance knob.
- **Credit comes from the settlement, never from the price table.** `MarkMetered` is handed
  `requirements.Amount` — what the facilitator actually confirmed moved. A credit recomputed from
  config could disagree with the money, and the trail's whole claim is that the node owes back what
  it received.
- **Expiry is derived from credit, and pinned once it runs out.** `syncExpiryLocked` keeps
  `expiresAt` equal to what the credit buys, which is what lets the existing freeze-and-reap sweep
  work unchanged — a session is overdue exactly when its credit is empty. When it hits zero the
  expiry is pinned at that instant: a zero-credit session that kept moving its own expiry forward on
  every tick would sit at "zero seconds overdue" forever and never freeze.
- **A frozen session does not burn.** The sweep only ticks a `LeaseActive` lease, and `ResumeLease`
  restarts the meter's clock. Charging for a pause would bill a renter for time they got nothing in.
- **Expiry reuses the existing 15-second sweep**, not new timers. There are no `time.AfterFunc`
  timers in this codebase and adding some would mean two schedulers for one event.
- **Settle with the lease you already hold, never by looking it up again.** `StopLease` releases the
  node's single lease slot, so after a reap `runner.ActiveLease()` returns nothing — a re-lookup
  finds no session and silently refunds nobody.
- **A failed refund stays `SettlePending` on purpose.** The sweep calls `settleSession` again for
  every terminal lease, so a transient transfer failure retries itself rather than quietly writing
  the renter's money off.
- **Refunds are paid from the operator key, and that changes what that key is for.** With
  `leases.self_settle` on, the operator account needs more than a fee float — enough to cover an
  outstanding chunk. Earnings are untouched: they land in `pay_to`, which still signs nothing and
  whose key this machine still does not have. Do not describe the operator key as holding only gas.
  With `self_settle` off the node meters and publishes the debt but a human pays it.
- **There is no session quote endpoint.** The authoritative price is the ordinary x402 challenge in
  the `PAYMENT-REQUIRED` header; the shopping-ahead terms are `nodespec.LeaseOffer` on the free
  `/v1/specs`. Adding a second source for the same numbers would be a second thing to keep in
  agreement with the first.

**Parked, not deleted:** `contracts/SessionEscrow.sol`, `agent/internal/escrow` (except
`PricePerSecond`, which both modes use) and `client/src/escrow.ts` are the harder-guarantee
fallback — there the provider never holds the renter's money at all — that a future
`payment_mode: escrow-vault` could reactivate. Nothing on the default path calls them. Their
hard-won facts are still true and still worth keeping: `msg.value` inside the Hedera EVM is
**tinybars, not weibars**; `pay_to`'s EVM address must be **resolved from the mirror node, never
computed** as the long-zero form; a failed payout must credit `pendingWithdrawal` rather than
revert; and the mirror node's REST path wants `0.0.x-sss-nnnnnnnnn`, not `0.0.x@sss.nnn`.

## Provider identity — ERC-8004

A provider registers once with `cleargate-node register` and gets a persistent `agentId`. That id
answers "who is this provider", and deliberately nothing else: GPU, price and availability change
every thirty seconds and live in the registry, where they cost nothing to update.

- **Registration is explicit, and `serve` never does it.** An identity that appeared as a side
  effect of starting a daemon would be a claim nobody chose to make, and it is the only command
  that spends a provider's HBAR.
- **It is idempotent at two levels — config and chain.** `register` checks `identity.agent_id`, and
  the contract's `agentIdOf` is checked before minting. A provider who lost their `config.yaml`
  recovers their id instead of silently acquiring a second one and orphaning the first.
- **`newAgent` binds to `msg.sender`.** ClearGate cannot mint an identity for a provider, move one,
  or revoke one. That is the only reason it is worth being on-chain rather than another column in
  our Postgres — and it is why the registry records an agent id without pretending to verify it.
- **`agentDomain` is the host of `public_url`, and the node serves the card there** at
  `GET /.well-known/agent-card.json`, free and unauthenticated. The resolution path is id → domain →
  a card the provider serves themselves; our registry appears nowhere in it. The card is rendered
  from `nodespec.Spec`, so it cannot disagree with `/v1/specs` or the heartbeat.
- **Capabilities are derived, never stored.** `cuda`/`ssh`/`jupyter`/`refundable` come from what the node
  already advertises. A stored copy would be a second thing to keep in step with the first.

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

Mainnet. Multi-GPU partitioning. Arbitrary user-supplied images. Reputation and slashing. Fiat
on-ramp. Windows providers.

**No registry keeper.** The registry holds no Hedera key and runs no background work, and nothing
about a metered session needs one: the node's own 15-second sweep meters, freezes, reaps and refunds,
and a renter who force-quits is refunded by that sweep rather than by anyone chasing them. Adding a
keeper would give the registry its first key and its first worker for no problem that exists.

**Contract-held escrow is parked, not gone.** It shipped, and was replaced on the session path by
metering with a published refund-owed trail: the renter's experience is the same (stop early, get
the rest back) and the payment path is one mechanism instead of two. What was given up is real — in
the escrow flow the provider never held the renter's money — which is why the contract stays in the
repo behind a future `payment_mode: escrow-vault` rather than being deleted. The flat-fee job path
keeps its direct-transfer x402 flow unchanged.

## Reference

- Blocky402 docs: https://blocky402.com/docs/
- Hedera x402: https://docs.hedera.com/solutions/ai/x402
- Scheme spec: `scheme_exact_hedera.md` in the x402 repo
