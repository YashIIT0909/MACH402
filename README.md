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

And the metered half of **M3**, **interactive leases** — the other way to buy compute here:

- **`POST /v1/leases`**: pay by the minute for an SSH shell and a Jupyter server in a container on
  the provider's GPU. Nothing of the renter's is uploaded; their code and data stay on their machine.
- **Certificate access, no credential exchange.** The renter generates a keypair locally and sends
  only the public half; the node's own CA signs it for one lease, expiring when the paid time does.
- **A tunnel out of NAT** (`cloudflared`, run by the node), so a provider needs no public IP and no
  port forwarding — and no Cloudflare account: the registry provisions it for them, or they use a
  quick tunnel and need no account either.
- **Freeze, then reap.** Missed extensions freeze the container rather than killing it, so a renter
  who is mid-run and slow to pay does not lose their work.

The rent-from-the-website flow and HCS receipts (M4–M5) are not built yet.

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

### Selling interactive access too

Separate from the GPU flag, and separate on purpose: handing a stranger a live shell is a bigger ask
than running their sandboxed batch job, so it never rides along with anything else.

```sh
LEASES=1 GPU=1 PAY_TO=0.0.1234 REGISTRY_URL=http://localhost:4400 \
  bash scripts/install.sh
```

That adds three things to the install: `cloudflared` (fetched for you), a check that `ssh-keygen`
exists, and a build of the lease images — which is slow, and only happens once.

By hand instead of through the installer:

```sh
make lease-image
./bin/cleargate-node setup --enable-leases --tunnel-mode quick --pay-to 0.0.1234 --force
```

`make lease-image` picks its base from what the machine has: the CUDA base where
the `nvidia` runtime is installed, a plain Python base otherwise. That is not a
convenience — passing a GPU into a container with no CUDA toolkit produces a lease
where `nvidia-smi` lists the card and every kernel launch fails, which is worse
than selling CPU honestly. The node checks the image and **refuses to sell
`require_gpu` leases on a CUDA-less one**, says so at `setup`, and advertises
`leases.gpu: false` so a renter sees it before paying. Override with
`LEASE_BASE_IMAGE=…` if you want a specific base.

The CUDA base makes this a **PyTorch** box, and that is the whole of it. Torch sees
the GPU immediately; **TensorFlow cannot be made to, on this base, by any means we
measured** — `tensorflow`, `tensorflow[and-cuda]`, and a separate virtualenv all end
at `Cannot dlopen some GPU libraries` and fall back to CPU, because TF's CUDA wheels
and the ones torch pins cannot coexist. It is a property of the image rather than
something a renter can debug their way out of, so the container's MOTD says so
plainly instead of letting them burn paid minutes on it.

A provider who expects TensorFlow renters should change the base rather than add
packages to this one:

```sh
make lease-image LEASE_BASE_IMAGE=tensorflow/tensorflow:2.17.0-gpu
```

`LEASE_EXTRA_PIP="…"` bakes additional packages into whichever base you pick.

`--tunnel-mode quick` needs no Cloudflare account at all and gives renters Jupyter over a random
hostname. `--tunnel-mode named` asks the registry to provision a stable tunnel for the node, which
is what adds an SSH terminal — and needs a registry that has Cloudflare credentials configured.

Setup generates this node's SSH certificate authority under `lease-ca`, beside `config.yaml`. The
private half never leaves the machine and is never copied anywhere, the same rule that keeps this
binary free of a Hedera key. Do not delete it while leases are live: every certificate already
issued would stop working.

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

### Rent a shell instead

`run` sends your code to the node. `rent` sends nothing — you get a shell and a Jupyter server on
the provider's GPU for the minutes you buy, and your code and data never leave your machine.

The provider has to have opted in (`LEASES=1` at install), and `cleargate quote` says whether they
did.

```sh
cleargate rent --node http://localhost:8402 --minutes 30 --budget 50000000
```

That prints an `ssh` command and a URL to paste into Colab's **Connect to a local runtime**, then
holds the lease open by buying another slice before each one lapses — stopping when your `--budget`
would be passed. Ctrl-c stops the lease and stops paying.

Two things are worth knowing before you rent:

- **You get a root shell in a container, and it has a network — but an allowlisted one.** `pip`,
  `conda`, `npm`, GitHub and Hugging Face work; arbitrary hosts do not. `cleargate quote` prints the
  node's list.
- **Check `leases.gpu`, not `gpu.available`.** The first says a lease container can compute on the
  card; the second only says the host has one. They differ when a provider built their lease image
  without CUDA, and `--gpu` refuses that node before you pay. PyTorch is preinstalled and ready;
  TensorFlow does not work on the GPU on a PyTorch-based lease image at all, by any route we
  measured — check what the node runs before renting for a TF workload.
- **SSH depends on how the provider's tunnel is set up.** A node on a *named* tunnel gives you both
  a terminal and Jupyter. A node on a *quick* tunnel — the zero-setup option, no Cloudflare account
  — gives you Jupyter only. The quote and the rent output both say which.

The certificate you get back is valid only for that lease, only until its paid time runs out. There
is no key to revoke and nothing to clean up: extending re-signs a new one, and letting a lease lapse
is how it ends.

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
| `POST` | `/v1/leases` | **x402** | buy interactive time, priced per minute. `404` on a node that did not opt into leasing |
| `POST` | `/v1/leases/:id/extend` | **x402** + lease token | buy another slice; re-signs the certificate with the later expiry |
| `GET` | `/v1/leases/:id` | lease token | status and seconds remaining — free, because it is what decides whether to pay again |
| `POST` | `/v1/leases/:id/stop` | lease token | end the lease and stop the meter |

Access tokens are 32 random bytes, minted at settlement, scoped to one job or one lease, and
compared in constant time. An unknown id and a wrong token both answer 404: whether a job or a lease
exists is not something an unauthorized caller gets to learn.

`POST /v1/leases` settles **after** a reachability check, not before: verify, start the container,
sign the certificate, point the tunnel at it, prove the tunnel actually answers, *then* settle. A
renter is never charged for a lease that never came up. That is also why the lease image has to be
built before the node sells anything (`make lease-image`) — unlike a job, there is no `staging`
phase to hide an image pull in.

A lease moves `provisioning → active → paused → active`, and out through `stopped`, `expired` or
`failed`. `paused` is a cgroup freeze, not a kill.

### Registry API

Discovery only. There is no payments table, no balance, and no route that moves money.

| Method | Path | Authorization | Notes |
|---|---|---|---|
| `GET` | `/health` | free | liveness; what `setup` preflights against |
| `POST` | `/v1/nodes/heartbeat` | node's listing token | upserts one node; the first beat claims the `node_id` |
| `POST` | `/v1/nodes/:id/offline` | node's listing token | the node is stopping; go offline now |
| `POST` | `/v1/nodes/:id/tunnel-token` | node's listing token | provisions this node's Cloudflare tunnel and DNS routes; `503` if this registry has no Cloudflare account |
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

The tunnel route is the only place Cloudflare credentials are ever used, and they live in the
registry's environment (`CLOUDFLARE_API_TOKEN`, `CLOUDFLARE_ACCOUNT_ID`, `CLOUDFLARE_ZONE_ID`,
`LEASE_DOMAIN` — see `registry/.env.example`). A provider never has a Cloudflare account and never
sees anything but a token good for running their own one tunnel. Leave those variables unset and the
registry still works: it answers 503 there, and nodes fall back to quick tunnels, which need no
account at all. This does not put the registry in the payment path — it hands out a network route,
and renters still pay nodes directly.

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

### The lease sandbox, and where it differs

A lease cannot be `NetworkMode: none` — a renter with a shell has to be able to install a package —
and it cannot have a read-only root for the same reason. Those two are the *only* relaxations, and
what replaces them is stricter rather than looser:

- **The container has no route off the machine.** It sits on a Docker network created with
  `Internal: true`. There is nothing to reach.
- **The one way out is a proxy that denies by default.** `cleargate-egress` is the single container
  dual-homed on that network and the outside, it is built from source in this repo
  (`agent/lease-image/egress`), and it allows package and model registries and nothing else. A
  renter can unset `HTTP_PROXY` and gain nothing, because there is no second path to find. The
  allowlist is matched on domain boundaries, so `notgithub.com` does not pass as `github.com`.
- **Every capability is dropped** except the handful `sshd` needs to accept a login, plus
  `no-new-privileges`, memory, CPU and PID caps.
- **Nothing survives the lease.** The workspace volume, both containers and the whole network are
  destroyed at reap.

Leases run as root inside the container, deliberately — a rented dev box where `apt-get` does not
work is not a usable one — which makes user-namespace remapping matter more here than it does for
jobs. The installer says so, and points at Docker's `userns-remap`.

Access is by certificate and nothing else. The container's `sshd` has no `authorized_keys` file: it
trusts one CA (the node's own, generated locally at setup, private half never copied anywhere) and
accepts one principal (the lease id). A certificate minted for a different lease on the same node is
signed by the same CA and still refused. Certificates expire when the paid time does, which is why
there is no revocation list — a force-stopped lease has its container killed, so there is nothing
left for a valid certificate to authenticate against.

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
make test         # go tests, including real-Docker sandbox tests and the egress allowlist
make lease-image  # the lease runtime and its egress proxy
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
