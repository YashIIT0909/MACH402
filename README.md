# ClearGate

A GPU rental marketplace settled with [x402](https://docs.hedera.com/solutions/ai/x402) payments on
Hedera. Providers run a daemon on an idle GPU; renters — people at a terminal or autonomous agents —
pay that node directly, by the second, for a container on it.

No API keys. No subscriptions. No custody. The renter pays the machine that does the work, and the
payment settles on Hedera in under a second, and whatever credit they do not use comes back.

Built for the *AI & Agentic Payments on Hedera* track. **Testnet only.**

---

## What works currently

A node sells one thing: **metered sessions**.

- **`POST /v1/sessions`**: pay for a Jupyter server in a container on the provider's GPU in small
  chunks of credit, burned by the second. Stop early and the **unburned remainder comes back**,
  paid automatically from the node's own operator account. Nothing of the renter's is uploaded;
  their code and data stay on their machine.
- **Paid only once it answers.** The node verifies the payment, starts the container, points a
  tunnel at it and proves it is reachable before it settles — a session that never came up costs
  nothing.
- **A capped exposure.** One payment buys at most `leases.session_chunk_seconds` (five minutes by
  default), so that is the most of a renter's money a provider ever holds ahead of the compute.
- **Certificate access, no credential exchange.** The renter generates a keypair locally and sends
  only the public half; the node's own CA signs it for one lease.
- **A tunnel out of NAT** (`cloudflared`, run by the node), so a node needs no public IP, no port
  forwarding and no Cloudflare account: every session is published through a quick tunnel.
- **Freeze, then reap.** Running out of credit freezes the container rather than killing it, so a
  renter who is mid-run and slow to top up does not lose their work.
- **GPU passthrough**, always requested and gated on the `nvidia` container runtime actually being
  present — a node in CPU-fallback mode says so, and refuses sessions that demand a GPU.
- **A provider dashboard** (`cleargate-node tui`): payments as they settle, the session running now
  and what is owed back, GPU utilisation, and a pause key that stops selling.

What makes prepaying a stranger checkable:

- **An HCS audit trail**: every settlement — and a burn checkpoint every fifteen seconds a session
  runs, carrying what the node would owe if it stopped right now — is published to a Hedera
  Consensus Service topic the provider owns. A provider who later refuses a refund is refusing a
  number they already signed, repeatedly, before there was anything to argue about.
- **ERC-8004 provider identity**: `cleargate-node register` gives a provider a persistent on-chain
  agent id, and the node serves its own agent card at `/.well-known/agent-card.json`.

Discovery:

- **A registry** (`registry/`): Fastify and Postgres, holding one row per node, updated by
  heartbeats. It is discovery only — it never receives, holds or forwards funds. `GET /v1/providers`
  serves the same listings under the names an autonomous renter looks for.
- **A website** (`web/`): a page that hands a provider their install command, a page that lists
  every node with its GPU and session price, and a rent flow that pays from the renter's own wallet.
- **An installer** (`scripts/install.sh`): builds the daemon, the lease image and the signing
  sidecar, configures the node, and starts it announcing.
- A smoke test that proves one real HBAR payment moves, kept green in CI.

Batch jobs (`POST /v1/jobs`) and prepaid, non-refundable leases shipped in earlier milestones and were
removed.

---

## Architecture

```
                    ┌──────────────────────────────┐
                    │  renter (client/, TypeScript)│
                    │  holds the only private key  │
                    └───────┬──────────────▲───────┘
                            │              │
          POST /v1/sessions │              │ 402 + PAYMENT-REQUIRED
        PAYMENT-SIGNATURE   │              │ 200 + PAYMENT-RESPONSE
                            ▼              │
                    ┌───────────────────────────────┐
                    │  provider node (agent/, Go)   │
                    │  x402 resource server         │
                    │  lease runner, no payout key  │
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
lives in the TypeScript client package the website uses, which is where the only mature implementation of the Hedera
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

1. **Renter asks for a session.** `POST /v1/sessions` with how long and an SSH public key, no payment.
2. **Node answers 402.** The challenge is base64 JSON in the `PAYMENT-REQUIRED` header, with
   `Cache-Control: no-store`. `extra.feePayer` is read from the facilitator's `GET /supported` on
   every challenge and never hardcoded — if it does not match, the client SDK throws before signing.
3. **Renter signs.** `@x402/hedera` builds a partially-signed `TransferTransaction` paying the node,
   base64-encoded into `PAYMENT-SIGNATURE`, and retries the request.
4. **Node verifies.** It POSTs `{ x402Version, paymentPayload, paymentRequirements }` to the
   facilitator's `/verify`, checking against *its own* requirements, not the client's copy of them.
5. **Node starts the container, publishes it and proves it answers**, then **settles** — all inside
   the payload's `maxTimeoutSeconds`. If anything before settlement fails, the container is torn
   down and the renter is not charged.
6. **Node returns 200** with the settlement receipt in `PAYMENT-RESPONSE`, the Jupyter link, and a
   session token in the body. The token — not the payment — authorizes top-ups, state and stop.

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
make install && make agent && make lease-image
./bin/cleargate-node setup --pay-to 0.0.YOUR_ACCOUNT
./bin/cleargate-node serve
```

`setup` preflights the facilitator, Docker, the GPU, the lease image, `cloudflared` and the operator
key before writing `config.yaml` — and waits while you fund that key with a few testnet HBAR — so
problems surface then rather than during someone's paid session. Without the NVIDIA Container Toolkit
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
  LEASE_PRICE_TINYBARS_PER_MINUTE=200000 \
  PUBLIC_URL=http://localhost:8402 \
  REGISTRY_URL=http://localhost:4400 \
  ./scripts/install.sh
```

That builds the daemon, preflights Docker and the GPU, writes `config.yaml` and starts serving. The
node announces itself immediately and every 30 seconds after, so it shows up at
<http://localhost:3000/nodes> straight away — with its GPU, session price, container and payout account.

Listing is opt-in. Leave `REGISTRY_URL` out and the node is simply unlisted: renters who know its
URL can still pay it. A registry that is down never interrupts a paid session.

### Renting the GPU out for real

The GPU path needs the NVIDIA Container Toolkit; the node checks for the `nvidia` runtime rather
than trusting `nvidia-smi` alone, because a card the daemon cannot pass through is a card you cannot
sell.

```sh
sudo pacman -S nvidia-container-toolkit          # Arch; use your distro's package elsewhere
sudo nvidia-ctk runtime configure --runtime=docker
sudo systemctl restart docker

docker run --rm --gpus all nvidia/cuda:12.4.1-base-ubuntu22.04 nvidia-smi
```

Then restart the node — `setup` already turned the GPU on; a config written before it did needs
`gpu_enabled: true`. `GET /v1/specs` will show the card instead of `CPU-fallback mode`.

### The lease image

Every node sells sessions, so the installer always fetches `cloudflared`, checks that `ssh-keygen`
and `pnpm` exist, builds the signing sidecar, and builds the lease images — which is slow, and only
happens once. By hand:

```sh
make lease-image
./bin/cleargate-node setup --pay-to 0.0.1234 --force
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

Leases are published through a Cloudflare quick tunnel: no Cloudflare account, a random hostname
per lease, and Jupyter only — a quick tunnel carries no SSH.

Setup generates this node's SSH certificate authority under `lease-ca`, beside `config.yaml`. The
private half never leaves the machine and is never copied anywhere, the same rule that keeps this
binary free of a Hedera key. Do not delete it while leases are live: every certificate already
issued would stop working.

### Rent from it

Browse `/nodes` on the website and open a node's listing. A node that sells interactive time has a
rent flow there: connect a wallet, pick the minutes, and the browser signs the payment — nothing
routes through the registry.

Discovery is free, and so is asking the price:

```sh
curl -s http://localhost:8402/v1/specs
curl -si -X POST http://localhost:8402/v1/sessions \
  -H 'Content-Type: application/json' \
  -d "{\"seconds\":300,\"public_key\":\"$(cat ~/.ssh/id_ed25519.pub)\"}"
```

The second answers `402` with the price of one chunk in a `PAYMENT-REQUIRED` header.

### What you get

A session sends nothing of yours to the node — you get a shell and a Jupyter server
on the provider's GPU, billed by the second, and your code and data never leave your machine.

The provider has to have opted in (`LEASES=1` at install); the `leases` block in the node's
`/v1/specs`, and its listing on the website, say whether they did.

Rent from the node's page on the website. It gives you the Jupyter link, tops the session up as
its credit runs low, and stopping it refunds what was not used.

Two things are worth knowing before you rent:

- **You get a root shell in a container, and it has a network — but an allowlisted one.** `pip`,
  `conda`, `npm`, GitHub and Hugging Face work; arbitrary hosts do not. `/v1/specs` lists them
  under `leases.egress_allowlist`.
- **Check `leases.gpu`, not `gpu.available`.** The first says a lease container can compute on the
  card; the second only says the host has one. They differ when a provider built their lease image
  without CUDA, and asking for a GPU refuses that node before you pay. PyTorch is preinstalled and ready;
  TensorFlow does not work on the GPU on a PyTorch-based lease image at all, by any route we
  measured — check what the node runs before renting for a TF workload.
- **There is no SSH.** Every lease is published through a Cloudflare quick tunnel, which carries
  HTTP only — so you get Jupyter, and Jupyter's own terminal.

The certificate you get back is valid only for that lease, only until its paid time runs out. There
is no key to revoke and nothing to clean up: it lapses with the session.

---

## Node API

Every endpoint states what authorizes it. New endpoints must do the same.

| Method | Path | Authorization | Notes |
|---|---|---|---|
| `GET` | `/health` | free | liveness |
| `GET` | `/v1/specs` | free | session terms, hardware, egress allowlist — discovery must not cost money |
| `POST` | `/v1/sessions` | **x402** | open a metered session: one chunk of credit, burned by the second. `503` while the operator has the node paused |
| `POST` | `/v1/sessions/:id/topup` | **x402** + session token | buy another chunk of credit |
| `GET` | `/v1/sessions/:id` | session token | credit, burn and refund state — free, because it is what decides whether to top up |
| `POST` | `/v1/sessions/:id/stop` | session token | end the session; the unburned credit is refunded |

Access tokens are 32 random bytes, minted at settlement, scoped to one session, and compared in
constant time. An unknown id and a wrong token both answer 404: whether a session exists is not something an unauthorized caller gets to learn.

`POST /v1/sessions` settles **after** a reachability check, not before: verify, start the container,
sign the certificate, point the tunnel at it, prove the tunnel actually answers, *then* settle. A
renter is never charged for a lease that never came up. That is also why the lease image has to be
built before the node sells anything (`make lease-image`) — there is no phase after settlement
to hide an image pull in.

A lease moves `provisioning → active → paused → active`, and out through `stopped`, `expired` or
`failed`. `paused` is a cgroup freeze, not a kill.

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



---

## The sandbox

A session hands a paying stranger a shell, so the container cannot be `NetworkMode: none` — a renter
has to be able to install a package — and it cannot have a read-only root for the same reason. What
replaces them is strict:

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
work is not a usable one — which makes user-namespace remapping matter. The installer says so, and points at Docker's `userns-remap`.

Access is by certificate and nothing else. The container's `sshd` has no `authorized_keys` file: it
trusts one CA (the node's own, generated locally at setup, private half never copied anywhere) and
accepts one principal (the lease id). A certificate minted for a different lease on the same node is
signed by the same CA and still refused. Certificates expire when the paid time does, which is why
there is no revocation list — a force-stopped lease has its container killed, so there is nothing
left for a valid certificate to authenticate against.

### When payment happens, relative to the slow parts

```
validate the request         →  400, and costs the renter nothing
no payment header            →  402 challenge, priced per chunk
header present               →  /verify
verified                     →  start the container, sign the certificate
container up                 →  point the tunnel at it, prove it answers
reachable                    →  /settle          ← inside the payload's 300s
settled                      →  credit banked, burned by the second
stopped or reaped            →  unburned credit refunded
```

---

## Deploying

[`deploy/README.md`](deploy/README.md): the registry and Postgres on one server, the website on
another, each started with a single `docker compose up -d --build` behind automatic HTTPS.

---

## Development

[`TESTING.md`](TESTING.md) is the step-by-step runbook for verifying a build, from static
checks through a real paid session. Every command in it runs from the repo root.


```sh
make typecheck    # every TS package
make test         # go tests, including the meter and the egress allowlist
make lease-image  # the lease runtime and its egress proxy
make smoke        # live payment against testnet — run before every PR
```

A golden-fixture test (`agent/internal/x402/challenge_test.go`) pins the Go challenge to bytes
captured from the reference `@x402/express` server. There is no automated session smoke test yet;
paying a real node from the website (TESTING.md step 12f) is that check.

If a change makes `make smoke` fail, the change is wrong until proven otherwise.

---

## License

MIT — see [LICENSE](LICENSE).
