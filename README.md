# ClearGate

A GPU rental marketplace settled with [x402](https://docs.hedera.com/solutions/ai/x402) payments on
Hedera. Providers run a daemon on an idle GPU; renters — people at a terminal or autonomous agents —
pay that node directly, per job, and get a container run on it.

No API keys. No subscriptions. No escrow. The renter pays the machine that does the work, and the
payment settles on Hedera in under a second.

Built for the *AI & Agentic Payments on Hedera* track. **Testnet only.**

---

## What works today

Milestones **M0** (payment spike) and **M1** (one node, one paid job):

- A Go agent that serves an x402-gated API, verifies payment with the facilitator, runs a sandboxed
  container, and settles immediately.
- **Dataset staging**: the renter sends a *URL*, the node downloads it and mounts it at `/data`. The
  container itself never gets a network.
- **GPU passthrough**, behind a config flag and gated on the `nvidia` container runtime actually
  being present — a node in CPU-fallback mode says so, and refuses jobs that demand a GPU.
- **A provider dashboard** (`cleargate-node tui`): payments as they settle, jobs as they run, GPU
  utilisation, and a pause key that stops selling without stopping running work.
- A TypeScript renter CLI that signs Hedera payments, streams job output, and downloads results.
- A smoke test that proves one real HBAR payment moves, kept green in CI.

Plus the discovery half of **M2**:

- **A registry** (`registry/`): Fastify and Postgres, holding one row per node, updated by
  heartbeats. It is discovery only — it never receives, holds or forwards funds.
- **A website** (`web/`): a page that hands a provider their install command, and a page that lists
  every node with its GPU, price and limits.
- **An installer** (`scripts/install.sh`): builds the daemon, configures it, and starts it announcing.

The rent-from-the-website flow, metered leases and HCS receipts (M3–M5) are not built yet.

---

## Architecture

```
                    ┌──────────────────────────────┐
                    │  renter (client/, TypeScript)│
                    │  holds the only private key  │
                    └───────┬──────────────▲───────┘
                            │              │
              POST /v1/jobs │              │ 402 + PAYMENT-REQUIRED
        PAYMENT-SIGNATURE   │              │ 200 + PAYMENT-RESPONSE
                            ▼              │
                    ┌───────────────────────────────┐
                    │  provider node (agent/, Go)   │
                    │  x402 resource server         │
                    │  docker runner, no keys ever  │
                    └───────┬───────────────────────┘
                            │ /verify  /settle  /supported
                            ▼
                    ┌───────────────────────────────┐
                    │  Blocky402 facilitator        │
                    │  co-signs as fee payer        │
                    └───────┬───────────────────────┘
                            ▼
                       Hedera testnet
```

**The load-bearing rule:** the agent never holds a private key and never links a Hedera SDK. The
merchant side of x402 needs only JSON construction and HTTP calls to the facilitator. All signing
lives in the TypeScript renter client, which is where the only mature implementation of the Hedera
exact scheme exists.

| Component | Language | Why |
|---|---|---|
| `agent/` | Go | One static binary, trivial to install on a stranger's machine; the merchant side needs no SDK |
| `client/` | TypeScript | `@x402/hedera` is the reference implementation of the Hedera exact scheme; signing must use it |
| `packages/types/` | TypeScript | The frozen cross-component contract, re-exporting the SDK's wire types so drift becomes a compile error |
| `smoke/` | TypeScript | The known-good reference server and client, kept forever as a CI regression test |

---

## The payment flow

x402 v2. Note the header names — v1's `X-PAYMENT` pair is accepted on input but never emitted.

1. **Renter asks for work.** `POST /v1/jobs` with a job spec, no payment.
2. **Node answers 402.** The challenge is base64 JSON in the `PAYMENT-REQUIRED` header, with
   `Cache-Control: no-store`. `extra.feePayer` is read from the facilitator's `GET /supported` on
   every challenge and never hardcoded — if it does not match, the client SDK throws before signing.
3. **Renter signs.** `@x402/hedera` builds a partially-signed `TransferTransaction` paying the node,
   base64-encoded into `PAYMENT-SIGNATURE`, and retries the request.
4. **Node verifies.** It POSTs `{ x402Version, paymentPayload, paymentRequirements }` to the
   facilitator's `/verify`, checking against *its own* requirements, not the client's copy of them.
5. **Node starts the container**, then **settles immediately**. Never the other way round, and never
   after the job finishes: the signed payload expires at `maxTimeoutSeconds`, and a training run
   outlives that window many times over. If settlement fails, the container is killed — the node
   does not do unpaid work, and the renter is not charged.
6. **Node returns 200** with the settlement receipt in `PAYMENT-RESPONSE` and a job token in the
   body. The token — not the payment — authorizes logs and artifacts from then on.

The settlement is written to the node's append-only `receipts.jsonl` the moment it lands, so a
provider can audit earnings without trusting any website.

---

## Setup

Requires Node 22+, pnpm 10, Go 1.25+, and Docker.

```sh
git clone https://github.com/YashIIT0909/ClearGate
cd ClearGate
pnpm install
cp .env.example .env      # then fill it in
```

Fund a testnet account at [portal.hedera.com](https://portal.hedera.com) and put its id and private
key in `.env`. That key is the renter's; it is read from the environment, never written to disk,
never logged, and never seen by a provider node.

### Prove a payment works

```sh
make supported    # the facilitator still advertises hedera:testnet
make smoke        # boots a reference server, pays it, prints a transaction id
```

`make smoke` ends with a real Hedera transaction id you can open on HashScan. If it fails, check in
this order: spend controls rejecting HBAR, wrong `HEDERA_KEY_TYPE`, an unfunded account, a
facilitator fee-payer mismatch.

### Run a provider node

```sh
make agent
./bin/cleargate-node setup --pay-to 0.0.YOUR_ACCOUNT
./bin/cleargate-node serve
```

`setup` preflights the facilitator, the Docker daemon and the GPU before writing `config.yaml`, so
problems surface then rather than during someone's paid job. Without the NVIDIA Container Toolkit
the node runs in **CPU-fallback mode** — it says so loudly at startup and reports
`gpu.available: false` in its specs, so nobody rents a GPU that is not there.

`cleargate-node tui` runs the same server with a live dashboard instead of log lines. `serve` stays
the right command for a box running under systemd.

### Get listed on the website

The registry is discovery only: it records where nodes are, never a payment. Start it and the site:

```sh
make registry-db      # Postgres on :5433, in docker
make dev-registry     # the registry on :4400
make dev-web          # the website on :3000
```

Then open <http://localhost:3000/provide>, fill in the Hedera account you want to be paid into, and
run the command it gives you on the machine with the GPU:

```sh
PAY_TO=0.0.1234 \
  PRICE_TINYBARS=100000 \
  PUBLIC_URL=http://localhost:8402 \
  REGISTRY_URL=http://localhost:4400 \
  ./scripts/install.sh
```

That builds the daemon, preflights Docker and the GPU, writes `config.yaml` and starts serving. The
node announces itself immediately and every 30 seconds after, so it shows up at
<http://localhost:3000/nodes> straight away — with its GPU, price, limits and payout account.

Listing is opt-in. Leave `REGISTRY_URL` out and the node is simply unlisted: renters who know its
URL can still pay it. A registry that is down never interrupts a paid job.

### Renting the GPU out for real

The GPU path needs the NVIDIA Container Toolkit; the node checks for the `nvidia` runtime rather
than trusting `nvidia-smi` alone, because a card the daemon cannot pass through is a card you cannot
sell.

```sh
sudo pacman -S nvidia-container-toolkit          # Arch; use your distro's package elsewhere
sudo nvidia-ctk runtime configure --runtime=docker
sudo systemctl restart docker

docker run --rm --gpus all nvidia/cuda:12.4.1-base-ubuntu22.04 nvidia-smi
docker pull pytorch/pytorch:2.4.1-cuda12.1-cudnn9-runtime
```

Then set `gpu_enabled: true` and restart. `cleargate quote` will show the card instead of
`CPU-fallback mode`.

### Rent from it

Everything runs from the repo root:

```sh
export PATH="$PWD/node_modules/.bin:$PATH"   # or prefix each command with `pnpm exec`

cleargate quote --node http://localhost:8402
cleargate run \
  --node http://localhost:8402 \
  --image python:3.11-slim \
  --script examples/train.py \
  --output result.tar
```

With a dataset, on a GPU:

```sh
cleargate run \
  --node http://localhost:8402 \
  --image pytorch/pytorch:2.4.1-cuda12.1-cudnn9-runtime \
  --gpu \
  --script examples/train_mnist.py \
  --dataset https://storage.googleapis.com/tensorflow/tf-keras-datasets/mnist.npz \
  --dataset-sha256 731c5ac602752760c8e48fbffcf8c3b850d9dc2a2aedcf2cc48468fc17b673d1 \
  --output model.tar
```

`quote` is free. `run` pays, streams the container's output live, and downloads the artifact.

---

## Node API

Every endpoint states what authorizes it. New endpoints must do the same.

| Method | Path | Authorization | Notes |
|---|---|---|---|
| `GET` | `/health` | free | liveness |
| `GET` | `/v1/specs` | free | price, hardware, limits, allowlist — discovery must not cost money |
| `POST` | `/v1/jobs` | **x402** | run one job; this is the call that moves HBAR. `503` while the operator has the node paused |
| `GET` | `/v1/jobs/:id` | job token | status |
| `GET` | `/v1/jobs/:id/logs` | job token | `?follow=1` for an SSE stream |
| `GET` | `/v1/jobs/:id/artifact` | job token | the output directory as a tar |
| `POST` | `/v1/jobs/:id/stop` | job token | kill early |

Job tokens are 32 random bytes, minted at settlement, scoped to one job, and compared in constant
time. An unknown job and a wrong token both answer 404: whether a job exists is not something an
unauthorized caller gets to learn.

### Registry API

Discovery only. There is no payments table, no balance, and no route that moves money.

| Method | Path | Authorization | Notes |
|---|---|---|---|
| `GET` | `/health` | free | liveness; what `setup` preflights against |
| `POST` | `/v1/nodes/heartbeat` | node's listing token | upserts one node; the first beat claims the `node_id` |
| `POST` | `/v1/nodes/:id/offline` | node's listing token | the node is stopping; go offline now |
| `GET` | `/v1/nodes` | free | every node, online first; `?online=true` to filter |
| `GET` | `/v1/nodes/:id` | free | one node |

A node is `online` when it has not withdrawn and a heartbeat has landed within 90 seconds. Stopping
a node with ctrl-c withdraws it immediately; the 90-second timeout is the backstop for a node that
lost power without saying goodbye. Either way the row stays, listed as offline, and the node
reclaims it on its next heartbeat. The listing token is minted by
`cleargate-node setup`, and the registry stores only its SHA-256 — it exists so nobody can repoint
an established listing at their own machine, and it can never authorize a payment.

A job moves `pending → staging → running → succeeded | failed | timeout | killed`. `staging` is
after payment and before the container runs — pulling the image, downloading the dataset — and it is
reported in `JobState.stage` and streamed over the same log feed, so a paid job is never silently
stalled.

---

## The sandbox

Untrusted code runs only in an allowlisted image, and:

- `NetworkMode: none` — the job computes, it does not phone home. There is a test that fails if
  network access ever leaks.
- Read-only root filesystem, with three writable volumes: `/work` for the uploaded script, `/data`
  for the staged dataset, `/out` for results.
- Memory, CPU and PID caps; all capabilities dropped; `no-new-privileges`.
- A wall-clock timeout, enforced by killing the container.
- Container and volumes reaped after the artifact retention window, so one renter's data does not
  linger on a provider's disk.

Arbitrary user-supplied images are deliberately out of scope. Jobs do run as root *inside* the
container — there is no user-namespace remapping yet, which is a known residual risk rather than
something the tests cover.

### Datasets, and why the node downloads them

A job with no network cannot fetch its own training data, so the renter supplies a URL and the
**node** fetches it. That puts a provider's daemon in the position of making arbitrary requests on
behalf of a paying stranger, so `agent/internal/fetch` treats the URL as hostile:

- https only by default; loopback, link-local (`169.254.169.254` — cloud metadata), private and
  CGNAT addresses refused, **re-checked on every redirect hop** and again at dial time against the
  address actually being connected to.
- A size cap enforced both against the advertised `Content-Length` and by counting bytes as they
  arrive, because a chunked response declares no length at all.
- Optional `sha256`, verified while streaming.
- Archives unpacked host-side, refusing path escapes (`../`), absolute paths and symlinks.

An operator can opt into a LAN mirror with `dataset.allow_private`, or pin an allowlist of hosts.

### When payment happens, relative to the slow parts

```
validate + preflight the dataset   →  400, and costs the renter nothing
no payment header                  →  402 challenge
header present                     →  /verify
verified                           →  accept the job
accepted                           →  /settle          ← immediately, well inside 300s
settled                            →  receipt + job token returned
background                         →  pull image, download dataset, run    ← job is "staging"
```

The signed payment payload expires at `maxTimeoutSeconds` (300s), and a dataset download plus a
training run outlive that many times over — so settlement happens when the job is *accepted*, never
when the work finishes.

Everything after settlement is unrefundable, which is why the checks that can be made in advance are
made *before* the 402: the image allowlist, `require_gpu` against real GPU availability, and a `HEAD`
preflight of the dataset URL. A job killed while staging genuinely stops, so a failed settlement
never leaves the node finishing a download for a payment that did not land.

---

## Development

[`TESTING.md`](TESTING.md) is the step-by-step runbook for verifying a build, from static
checks through a real paid job to the sandbox tests. Every command in it runs from the repo root.


```sh
make typecheck    # every TS package
make test         # go tests, including real-Docker sandbox tests
make smoke        # live payment against testnet — run before every PR
make smoke-agent  # pay the running Go agent with the official TS client
```

`make smoke-agent` is the test that matters most: it points the *official* `@x402/hedera` client at
the Go agent. If the official client can pay our agent unmodified, the challenge is correct. There
is also a golden-fixture test (`agent/internal/x402/challenge_test.go`) pinning the Go challenge to
bytes captured from the reference `@x402/express` server.

If a change makes `make smoke` fail, the change is wrong until proven otherwise.

---

## License

MIT — see [LICENSE](LICENSE).
