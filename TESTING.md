# Testing ClearGate

Every step below has a command, the output that means it passed, and what it actually proves. The
free steps cost nothing at all; each paid one says what it costs on
testnet.

Two terminals: **A** runs the provider node, **B** is the renter. **Every command below runs from the repo root** (`ClearGate/`) — none of them want you inside `client/` or `agent/`. `make: *** No rule to make target 'smoke'` means only that you are in the wrong directory.

Prerequisites: Node 22+, pnpm 10, Go 1.25+, Docker running, and a funded `.env`.

```sh
pnpm install
cat .env            # HEDERA_ACCOUNT_ID, HEDERA_PRIVATE_KEY, HEDERA_KEY_TYPE, PAY_TO_ACCOUNT_ID
docker info | grep -i "server version"
```

The provider binary is `./bin/cleargate-node`, built by `make agent`.

---

## 1. Static checks — free

```sh
make typecheck && make vet && make test && gofmt -l agent/
```

**Pass:** typecheck silent, `go vet` silent, all Go packages `ok`, `gofmt -l` prints nothing.

The meter, the burn trail and the refund accounting are pinned by
`agent/internal/httpapi/session_meter_test.go`; none of the Go tests need Docker.

## 2. The facilitator is alive — free

```sh
make supported
```

**Pass:** a `hedera:testnet` kind is listed and a fee payer is printed (`0.0.7162784` today).

This is the early-warning signal. If it fails, nothing downstream can settle and the problem is not
in ClearGate. The fee payer is read at runtime and never hardcoded, so a change here is absorbed
automatically — but it should be *seen*.

## 2b. Turn the GPU on — operator steps, free

Skip this if you only want to test the CPU path; every other step works without it.

```sh
sudo pacman -S nvidia-container-toolkit          # Arch; use your distro's package elsewhere
sudo nvidia-ctk runtime configure --runtime=docker
sudo systemctl restart docker
```

**Pass:** all three of these.

```sh
docker info --format '{{json .Runtimes}}' | grep -o nvidia     # prints: nvidia
docker run --rm --gpus all nvidia/cuda:12.4.1-base-ubuntu22.04 nvidia-smi
docker run --rm --gpus all pytorch/pytorch:2.4.1-cuda12.1-cudnn9-runtime \
  python -c "import torch; print(torch.cuda.is_available(), torch.cuda.get_device_name(0))"
```

The last one is the one that counts: `True NVIDIA GeForce RTX 3050 Laptop GPU`. A CUDA 13.2 driver
runs CUDA 12.1 containers under backward compatibility, so a driver newer than the image is fine and
expected — do not go hunting for a matching pair.

`setup` already writes `gpu_enabled: true`, and `DetectGPU` looks for exactly that `nvidia` runtime
entry, so nothing else needs changing. A config written before setup did that needs
`gpu_enabled: true` set by hand.

Then build the lease image on this machine, so it picks the CUDA base now that the runtime exists —
it is large, and a renter's paid session should not be the thing that waits for it:

```sh
make lease-image
```

If step 2 fails with `could not select device driver "" with capabilities: [[gpu]]`, the runtime was
registered but Docker was not restarted. A driver/library mismatch means the kernel module and
userspace are out of step after the upgrade — reboot.

## 3. Start the node — terminal A, free

```sh
make install && make agent && make lease-image
./bin/cleargate-node setup --pay-to 0.0.YOUR_ACCOUNT    # waits while you fund the operator key
./bin/cleargate-node serve
```

**Pass:** the banner prints the node id, the session price, the pay-to account and the GPU, then
`listening on 0.0.0.0:8402`. Without step 2b it prints a loud CPU-fallback warning instead, naming
what is missing — for example `docker has no "nvidia" runtime` — and the node advertises no GPU. A
config with `gpu_enabled: false` falls back the same way, on purpose:

```
gpu: unavailable — gpu_enabled is false in config; running in CPU-fallback mode
listening on 0.0.0.0:8402
```

If a previous run was killed mid-session, the startup sweep removes the orphaned containers and volumes
and says so. That line appearing is a pass, not a problem.

## 4. Free endpoints — terminal B

```sh
curl -s localhost:8402/health
curl -s localhost:8402/v1/specs | python3 -m json.tool
```

**Pass:** `/v1/specs` shows `pay_to`, `gpu` (`available: true` after step 2b, or
`available: false` with the CPU-fallback `reason` without it), and a `leases` block with
`payment_mode: "session"`, `price_tinybars_per_second` and `chunk_seconds`.

`gpu.available` must be `false` on a box without the NVIDIA container runtime. A node claiming a GPU
it cannot pass through is the worst failure mode this project has, so this is a real assertion.

## 4b. Getting listed on the website — free

Discovery only: no payment happens anywhere in this section, and the registry never holds funds.

```sh
make registry-db        # Postgres on :5433, in docker
make dev-registry       # terminal C — the registry on :4400
make dev-web            # terminal D — the website on :3000
```

Open <http://localhost:3000/provide>, enter the account you want paid, and run the command it gives
you from the repo root. Stop the node from step 3 first — the install script starts its own:

```sh
PAY_TO=0.0.YOUR_ACCOUNT \
  LEASE_PRICE_TINYBARS_PER_MINUTE=200000 \
  PUBLIC_URL=http://localhost:8402 \
  REGISTRY_URL=http://localhost:4400 \
  ./scripts/install.sh
```

**Pass:** setup prints `registry  http://localhost:4400`, the node starts, and
<http://localhost:3000/nodes> shows it as **available** with its GPU and session price within a
couple of seconds. `curl -s localhost:4400/v1/nodes` shows the same row with `"online": true`.

Three things to assert, all of which have already been wrong once:

```sh
# A node's listing cannot be stolen: a second beat with a different token is refused.
curl -s -X POST localhost:4400/v1/nodes/heartbeat \
  -H 'Content-Type: application/json' -H 'Authorization: Bearer attacker' \
  -d "$(curl -s localhost:8402/v1/specs | python3 -c 'import json,sys; s=json.load(sys.stdin); s.update(public_url="http://evil.example", paused=False); print(json.dumps(s))')"
# -> 403 this node_id is registered to a different token

# The registry is not a rendering hazard: a non-http URL never reaches the page.
curl -s -X POST localhost:4400/v1/nodes/heartbeat \
  -H 'Content-Type: application/json' -H 'Authorization: Bearer whatever' \
  -d '{"node_id":"node_x","public_url":"javascript:alert(1)"}'
# -> 400 expected an absolute http(s) URL

# Nobody else can delist you either: withdrawal needs the same token.
curl -s -o /dev/null -w '%{http_code}\n' -X POST localhost:4400/v1/nodes/NODE_ID/offline \
  -H 'Authorization: Bearer attacker'
# -> 403
```

Now press ctrl-c on the node. **Pass:** its log ends with `withdrawn from the registry`, and
`/nodes` shows it **offline** on the very next refresh — not ninety seconds later. Start it again
and it returns to `available`. The row itself stays either way: a renter looking for a node they
used yesterday should find it listed as offline rather than silently gone.

`kill -9` on the node skips the goodbye, which is the case the freshness window exists for: it
reads `available` until 90 seconds after its last heartbeat, then flips to `offline`.

Then stop the registry and confirm the node keeps working: `/v1/specs` still answers and a paid
session still runs. A registry outage must never interrupt selling compute.

## 5. The 402 challenge — terminal B, free

```sh
KEY="$(cat ~/.ssh/id_ed25519.pub)"
curl -si -X POST localhost:8402/v1/sessions \
  -H 'Content-Type: application/json' -d "{\"seconds\":300,\"public_key\":\"$KEY\"}"
```

**Pass:** status `402`, `Cache-Control: no-store`, and a `Payment-Required:` header. Decode it:

```sh
curl -sD - -o /dev/null -X POST localhost:8402/v1/sessions \
  -H 'Content-Type: application/json' -d "{\"seconds\":300,\"public_key\":\"$KEY\"}" \
  | grep -i '^payment-required:' | cut -d' ' -f2- | tr -d '\r' | base64 -d | python3 -m json.tool
```

It must be x402 **v2**: `x402Version: 2`, a `resource` object, and `accepts[0]` carrying
`amount` (not `maxAmountRequired`) for one chunk, `asset: "0.0.0"`, your `payTo`, and
`extra.feePayer`. The header is authoritative; the JSON body is a readable echo for humans.

Server-side refusals, also free — the node answers `400`, not `402`, so no challenge is issued and
nothing reaches the chain:

```sh
# longer than this node sells
curl -s -X POST localhost:8402/v1/sessions -H 'Content-Type: application/json' \
  -d "{\"seconds\":99999999,\"public_key\":\"$KEY\"}"

# a GPU this node's lease image cannot use (only refused on such a node)
curl -s -X POST localhost:8402/v1/sessions -H 'Content-Type: application/json' \
  -d "{\"seconds\":300,\"public_key\":\"$KEY\",\"require_gpu\":true}"

# not a public key
curl -s -X POST localhost:8402/v1/sessions -H 'Content-Type: application/json' \
  -d '{"seconds":300,"public_key":"hello"}'
```

**Pass:** a `400` for each, explaining itself. Anything knowable in advance is rejected while the
session is still free.

## 6. M0 gate — one real payment (**costs 0.001 HBAR**)

```sh
make smoke
```

**Pass:** ends with `SMOKE PASS <transaction id>`. Open the HashScan link it prints; the transfer
list must show your renter account **−100000**, the pay-to account **+100000**, and the *facilitator*
paying the network fee. The renter spends zero gas — that property is the whole reason x402 works
for agents, so look at it rather than trusting the word PASS.

## 7. A real paid session

There is no command-line client for sessions: pay for one from the website, as in step 12f.
`make smoke` (step 6) is what proves a payment moves, and the challenge bytes the node emits are
pinned by `agent/internal/x402/challenge_test.go`.

## 7c. The provider dashboard — free

```sh
./bin/cleargate-node tui
```

**Pass:** four screens — Overview, Leasing, Activity, Node. The header names the node and shows
`ACCEPTING`, the machine card states plainly whether the GPU is usable, and earnings match
`cleargate-node earnings`. With a session open from the website, Leasing shows its credit, what is
owed back and the Jupyter URL, and Activity shows a `session burn` line every 15 seconds.

The settlement line must show the **full** transaction id — that string is what a provider pastes
into HashScan to check the payment themselves.

Press `p` and, from terminal B, repeat the first request from step 5, then:

```sh
curl -s localhost:8402/health
```

**Pass:** `503 Service Unavailable` with a `Retry-After`, while `/health` still answers. Press `p`
again and the same POST returns `402`.

`e` ends the session running now, after asking a second time; the renter is charged for the seconds
used and refunded the rest.

The dashboard writes the daemon's logs to `cleargate-node.log`, since it owns the terminal.

## 8. The ledger agrees with the chain — free

```sh
./bin/cleargate-node earnings
```

**Pass:** the transaction id from step 7 appears with 100000 tinybars, matching the challenge
exactly. `earnings` reads the node's own append-only receipt file.

Confirm against the chain itself, which is the only source that can't be faked locally:

```sh
curl -s "https://testnet.mirrornode.hedera.com/api/v1/accounts/$HEDERA_ACCOUNT_ID" \
  | python3 -c 'import json,sys; print(json.load(sys.stdin)["balance"]["balance"])'
```

## 12. Escrow sessions and the audit trail — **costs a few tinybars per run**

Everything here is testnet money and the point of the section is that most of it
comes back. Run the steps in order; each one builds on the last.

### 12a. The contract math, free and offline

```
make contracts-test
```

**Pass:** 33 passing. No network, no HBAR, no credentials — Hardhat's in-process
EVM. If this fails, nothing below is worth running.

### 12b. Deploy, free of judgement but **costs about 2 HBAR of gas**

```
make contracts-deploy
```

**Pass:** two addresses and two HashScan links, written to
`contracts/deployments/hederaTestnet.json`.

### 12c. The refund, proven without a node — **costs about 0.004 HBAR**, most refunded

```
make contracts-demo
```

Opens a session, tops it up, waits 20 seconds, and settles early.

**Pass:** a `PASS` line saying the provider was paid for the seconds actually
elapsed and the rest went back. If the provider received the full deposit, the
refund did not happen and the run fails loudly rather than quietly passing.

This is the step that proves the mechanism. Everything after it is plumbing.

### 12d. A provider's operator key and audit topic — **costs about 1 HBAR to fund**

```
cleargate-node setup --enable-leases --enable-hcs \
  --enable-escrow --escrow-contract 0x<SessionEscrow> \
  --identity-contract 0x<IdentityRegistry>
```

**Pass on the first run:** setup prints the operator key's EVM address and says
it has no account yet. That is not a failure — an ECDSA key has an address from
birth, but no Hedera account exists until someone funds it. Send it a few HBAR
and run setup again:

```
FUND_ADDRESS=0x<the address setup printed> FUND_HBAR=20 \
  pnpm --filter @cleargate/contracts exec hardhat run scripts/fund.ts --network hederaTestnet
```

(A real provider would use the Hedera portal or a faucet; this just saves a
manual step while testing.)

**Pass on the second run:** an account id, a balance, the escrow contract, the
address `pay_to` resolves to, and a topic id with a HashScan link.

Two things setup refuses, and both are worth checking deliberately: a `pay_to`
with `receiverSigRequired` set, and one with no EVM address. Neither can be paid
by a contract, and discovering that at payout time would strand a session.

### 12e. Identity, and that it is safely re-runnable — **costs about 0.5 HBAR once**

```
make node-register
make node-register
```

**Pass:** the first run prints `registered  agent N`; the second prints
`confirmed   agent N (already registered)` with the **same** N and sends no new
registration. Registering twice would orphan the first id, so this is the
property that matters, not the first run.

Then:

```
curl -s http://localhost:8402/.well-known/agent-card.json | jq .registrations
```

**Pass:** the agent id, the address it is bound to, and the registry that issued
it. This is what the on-chain record resolves to.

### 12f. A real session, stopped early — **costs about 0.004 HBAR, most refunded**

Terminal A runs a node set up with `--enable-leases`. On the website, open
the node from `/nodes`, connect a wallet, and start a 5-minute session.

**Pass:** a payment, then the Jupyter link, then top-ups on their
own as the credit runs low. Stop the session after a minute or two.

**Pass on stop:** the session page shows what was used and what was refunded —
and the refund should be most of it. That difference is the entire feature.

**Check it on-chain**, not just in our own output:

```
curl -s "https://testnet.mirrornode.hedera.com/api/v1/topics/<topic>/messages?order=desc&limit=4" \
  | jq -r '.messages[] | .message | @base64d'
```

**Pass:** a `session_open` and a `session_settle` for the same `session_id`,
with `elapsed_seconds` and `refund_tinybars` matching what the session page showed. The
open record is what makes the settle record checkable by a stranger: together
they say what was promised and what was paid, and neither party wrote them
anywhere they could later edit.

### 12g. The renter vanishes — **costs about 0.002 HBAR, none refunded, and that is correct**

Start a short session from the website, then close the browser tab without
stopping it — no more top-ups will come. Wait out the paid time plus the node's
grace period.

**Pass:** the node's log shows the container frozen, then reaped, then
`session settled on-chain`. The provider is paid the full deposit, because
`elapsed` is capped at the duration that was actually bought — there is nothing
left to refund, and the renter got the minute they paid for.

### 12h. The payment path still works — free, and non-negotiable

```
make smoke
```

**Pass:** green. If it fails, the change is wrong.

---

## 11. Nothing leaked — free

Stop the node in terminal A (Ctrl-C), then:

```sh
docker ps -a --filter label=cleargate.lease
docker volume ls --filter label=cleargate.lease
docker network ls --filter label=cleargate.lease
```

**Pass:** all empty once no session is running. A session's two containers, its network and its
workspace volume are destroyed at reap. Kill the node mid-session and restart it: the startup sweep
clears the orphans and logs each one it removed.

---

## When something fails

| Symptom | Look here first |
|---|---|
| Payment "does nothing", no network call | Spend controls. `x402Client` permits only `findDefaultAsset` assets — on Hedera that is USDC `0.0.429274`, not HBAR. `client/src/payment.ts` must call `setSpendControls(false)` with its own policy. |
| `INVALID_SIGNATURE` or key parse error | Wrong `HEDERA_KEY_TYPE`. Flip between `ecdsa` and `ed25519` in `.env`. |
| `INSUFFICIENT_PAYER_BALANCE` | Renter account unfunded — top up at portal.hedera.com. |
| 402 loops forever, client never pays | The challenge bytes. Re-run step 5 and diff against `agent/internal/x402/testdata/golden_challenge.json`. |
| Settle fails after the container started | The node tears the session down and returns 402. Nothing was charged, and no free compute was given. |
| `No rule to make target` | You are not in the repo root. `make` reads only the current directory's Makefile; it never searches upwards. |
| `require_gpu` refused on a node with a GPU | Docker has no `nvidia` runtime, `gpu_enabled` is false, or the lease image has no CUDA runtime (`make lease-image` again). `/v1/specs` prints the reason. |
| Dashboard shows nothing | Its panes fill from live events. Open a session; a node with no history opens empty by design. |
| Port 8402 in use | An earlier `serve` is still running: `pgrep -af cleargate-node`. |
