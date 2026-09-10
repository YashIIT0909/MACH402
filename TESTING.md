# Testing M1

Every step below has a command, the output that means it passed, and what it actually proves. The
free steps cost nothing at all; the paid ones spend **0.001 HBAR each on testnet**, and running the
whole runbook costs about 0.008 HBAR.

Two terminals: **A** runs the provider node, **B** is the renter. **Every command below runs from the repo root** (`ClearGate/`) — none of them want you inside `client/` or `agent/`. `make: *** No rule to make target 'smoke'` means only that you are in the wrong directory.

Prerequisites: Node 22+, pnpm 10, Go 1.25+, Docker running, and a funded `.env`.

```sh
pnpm install
export PATH="$PWD/node_modules/.bin:$PATH"   # puts `cleargate` on your PATH for this shell
cat .env            # HEDERA_ACCOUNT_ID, HEDERA_PRIVATE_KEY, HEDERA_KEY_TYPE, PAY_TO_ACCOUNT_ID
docker info | grep -i "server version"
```

Without that `export`, `cleargate` is not a command — use `pnpm exec cleargate ...` instead, which
needs no PATH change. Either way the working directory stays the repo root, so `-s examples/train.py`
resolves where you expect. The provider binary is separate: `./bin/cleargate-node`, built by `make agent`.

---

## 1. Static checks — free

```sh
make typecheck && make vet && make test && gofmt -l agent/
```

**Pass:** typecheck silent, `go vet` silent, all Go packages `ok`, `gofmt -l` prints nothing.

The runner tests talk to the real Docker daemon: they create containers, upload a script into a
read-only-rootfs container, and assert the artifact comes back. If Docker is down they skip rather
than fail — check the output says `ok`, not `[no test files]`, for `internal/runner`.

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

Then set `gpu_enabled: true` in `config.yaml`. `DetectGPU` looks for exactly that `nvidia` runtime
entry, so nothing else needs changing.

Pre-pull the training image first — it is about 7 GB, and a renter's paid job should not be the
thing that waits for it:

```sh
docker pull pytorch/pytorch:2.4.1-cuda12.1-cudnn9-runtime
```

If step 2 fails with `could not select device driver "" with capabilities: [[gpu]]`, the runtime was
registered but Docker was not restarted. A driver/library mismatch means the kernel module and
userspace are out of step after the upgrade — reboot.

## 3. Start the node — terminal A, free

```sh
make agent
./bin/cleargate-node serve
```

**Pass:** the banner prints the node id, the price, the pay-to account, and a loud CPU-fallback
warning:

```
gpu: unavailable — gpu_enabled is false in config; running in CPU-fallback mode
listening on 0.0.0.0:8402
```

If a previous run was killed mid-job, the startup sweep removes the orphaned containers and volumes
and says so. That line appearing is a pass, not a problem.

## 4. Free endpoints — terminal B

```sh
curl -s localhost:8402/health
curl -s localhost:8402/v1/specs | python3 -m json.tool
cleargate quote --node http://localhost:8402
```

**Pass:** `quote` prints price, pay-to, `gpu none — CPU-fallback mode`, limits and the allowlist.

`gpu.available` must be `false` on a box without the NVIDIA container runtime. A node claiming a GPU
it cannot pass through is the worst failure mode this project has, so this is a real assertion.

## 5. The 402 challenge — terminal B, free

```sh
curl -si -X POST localhost:8402/v1/jobs \
  -H 'Content-Type: application/json' -d '{"image":"python:3.11-slim"}'
```

**Pass:** status `402`, `Cache-Control: no-store`, and a `Payment-Required:` header. Decode it:

```sh
curl -sD - -o /dev/null -X POST localhost:8402/v1/jobs \
  -H 'Content-Type: application/json' -d '{"image":"python:3.11-slim"}' \
  | grep -i '^payment-required:' | cut -d' ' -f2- | tr -d '\r' | base64 -d | python3 -m json.tool
```

It must be x402 **v2**: `x402Version: 2`, a `resource` object, and `accepts[0]` carrying
`amount` (not `maxAmountRequired`), `asset: "0.0.0"`, your `payTo`, and `extra.feePayer`. The header
is authoritative; the JSON body is a readable echo for humans. Getting these bytes wrong is the one
failure the official client cannot work around, which is why step 9 exists.

Client-side refusals, also free — no 402 is even requested:

```sh
cleargate run -n http://localhost:8402 -i alpine:latest -c echo hi
cleargate run -n http://localhost:8402 -i python:3.11-slim -c echo hi --budget 1
```

**Pass:** `node does not allow image "alpine:latest"` and `node asks 100000 tinybars, above your
--budget of 1`. The renter is stopped by their own budget, not by a failed transaction.

Server-side refusals, also free — the node answers `400`, not `402`, so no challenge is issued and
nothing reaches the chain:

```sh
# a dataset URL that does not resolve to a usable file
curl -s -X POST localhost:8402/v1/jobs -H 'Content-Type: application/json' \
  -d '{"image":"python:3.11-slim","dataset":{"url":"https://example.org/nope.tar.gz"}}'

# a job demanding a GPU this node does not have
curl -s -X POST localhost:8402/v1/jobs -H 'Content-Type: application/json' \
  -d '{"image":"python:3.11-slim","require_gpu":true}'

# a dataset URL aimed at the provider's own network
curl -s -X POST localhost:8402/v1/jobs -H 'Content-Type: application/json' \
  -d '{"image":"python:3.11-slim","dataset":{"url":"https://169.254.169.254/latest/meta-data/"}}'
```

**Pass:** three `400`s, each explaining itself — an unreachable dataset, a GPU this node lacks, and
`refusing to fetch from a link-local address`. That last one matters most: the node downloads
dataset URLs *from inside the provider's network*, so a renter must never be able to point it at
cloud metadata or a LAN host. `go test ./internal/fetch/` covers the rest of that policy.

This is also the step that protects the renter. Everything after settlement is unrefundable, so
anything knowable in advance is rejected while the job is still free.

## 6. M0 gate — one real payment (**costs 0.001 HBAR**)

```sh
make smoke
```

**Pass:** ends with `SMOKE PASS <transaction id>`. Open the HashScan link it prints; the transfer
list must show your renter account **−100000**, the pay-to account **+100000**, and the *facilitator*
paying the network fee. The renter spends zero gas — that property is the whole reason x402 works
for agents, so look at it rather than trusting the word PASS.

## 7. M1 gate — a real paid job (**costs 0.001 HBAR**)

Terminal B:

```sh
cleargate run \
  --node http://localhost:8402 \
  --image python:3.11-slim \
  --script examples/train.py \
  --output /tmp/result.tar
```

`pnpm exec cleargate run ...` is the same command without the PATH export.

**Pass:** all four of these, in order:

1. a job id, a token, and a transaction id are printed;
2. the log lines stream in **live** (`epoch   0  w=…`, one every second or so) — not dumped at the end;
3. `--- succeeded ---`;
4. `artifact /tmp/result.tar` is written.

Then check the artifact really came from the container:

```sh
tar -tvf /tmp/result.tar
tar -xOf /tmp/result.tar out/weights.json
```

**Pass:** `w` near 3.0 and `b` near 0.4 — the job actually ran gradient descent, it did not echo
something back. This is the track's "at least one real paid request end to end".

Sanity-check the ordering invariant in terminal A's log: `verify` → container started → `settle`.
Settlement must happen when the job *starts*, never when it finishes; the signed payload expires at
`maxTimeoutSeconds` (300s) and a 900s job would settle into an expired payload.

## 7b. A paid job with a dataset (**costs 0.001 HBAR**)

The thing the marketplace is actually for: the renter sends a script and a *URL*, and the node
fetches the data on their behalf.

To test without a public dataset, serve one locally and let the node reach it. Loopback is blocked
by default, so this needs the operator opt-in — which is itself the thing being tested:

```sh
mkdir -p /tmp/ds/sample && printf 'id,label\n1,cat\n2,dog\n' > /tmp/ds/sample/labels.csv
tar -czf /tmp/ds/dataset.tar.gz -C /tmp/ds sample
sha256sum /tmp/ds/dataset.tar.gz
(cd /tmp/ds && python3 -m http.server 8099 --bind 127.0.0.1 &)
```

Add to `config.yaml` and restart the node:

```yaml
dataset:
    allow_http: true      # only because this test server is plaintext
    allow_private: true   # only because it is on loopback
```

Then:

```sh
cleargate run -n http://localhost:8402 -i python:3.11-slim \
  -s examples/hello.py \
  -d http://127.0.0.1:8099/dataset.tar.gz \
  --dataset-sha256 <the sum you just printed>
```

**Pass:** the renter's stream shows staging *before* the container runs, and the checksum echoed
back matches:

```
downloading dataset dataset.tar.gz
downloading dataset 232 B / 232 B
downloaded 232 B (sha256 0b2389f1…)
extracting dataset.tar.gz
dataset ready at /data
```

Two properties worth checking deliberately:

1. **Settlement happened before staging finished.** Terminal A's log shows `job started` and the
   transaction id. A dataset can take minutes and the signed payload expires in 300s, so the node
   settles when it *accepts* the job, not when the data lands.
2. **The container still has no network.** Run `examples/escape.py` with a `--dataset` attached: the
   dataset is in `/data` and outbound TCP is still `Network is unreachable`. The node fetched it;
   the container could not have.

Pass a deliberately wrong `--dataset-sha256` and the job fails with `checksum mismatch` rather than
training on the wrong data. That failure costs the renter their fee — which is why step 5's
preflight exists, and why a mismatch is reported loudly rather than swallowed.

## 7c. The provider dashboard — free (uses whatever jobs you have already run)

```sh
./bin/cleargate-node tui
```

**Pass:** the header names the node and shows `accepting`, the GPU line states plainly whether the
GPU is usable, and earnings match `cleargate-node earnings`. Then, with a job running from terminal
B, all three panes update live:

```
  ClearGate  node_db8cc286021b15fe  accepting              agent 0f30398   up 39s
  GPU  none — CPU-fallback mode
  0.001 HBAR per job → 0.0.10446279     earned 0.01 HBAR over 10 job(s)
  JOB       STATUS     IMAGE             ELAPSED   DETAIL
▸ 7b334425  running    python:3.11-slim  00:00:15
  logs · 7b334425
  step  9 — the provider dashboard is watching this
  04:43:12 staging 7b334425  downloading dataset 232 B / 232 B
  04:43:12 started 7b334425  python:3.11-slim
  04:43:14 settled 0.001 HBAR from 0.0.10446559  0.0.7162784@1788995587.423051323
  ↑↓ select   x kill job   p pause/resume   ? help   q quit
```

The settlement line must show the **full** transaction id — that string is what a provider pastes
into HashScan to check the payment themselves.

Press `p` and, from terminal B:

```sh
curl -si -X POST localhost:8402/v1/jobs -H 'Content-Type: application/json' \
  -d '{"image":"python:3.11-slim"}' | head -3
curl -s localhost:8402/health
```

**Pass:** `503 Service Unavailable` with a `Retry-After`, while `/health` still answers. A paused
node sells nothing and keeps running what it already sold — that is how an operator reclaims their
laptop without killing paid work. Press `p` again and the same POST returns `402`.

`x` kills the selected job. It works during `staging` too, which is the case that matters: a
settlement can fail after staging has begun, and the node must stop downloading rather than finish
the job for free. `go test ./internal/runner/ -run TestKillDuringStaging` pins that.

The dashboard writes the daemon's logs to `cleargate-node.log`, since it owns the terminal.

## 8. Both ledgers agree — free

```sh
./bin/cleargate-node earnings                          # provider side
cleargate spend                                        # renter side
```

**Pass:** the transaction id from step 7 appears on **both** sides with the same amount, 100000
tinybars, matching the challenge exactly.

The two logs are deliberately not mirror images. `earnings` records every settlement the node made,
including the smoke-client payments from steps 6 and 9; `spend` records only what the renter CLI
itself paid, so a `make smoke-agent` job shows up on the provider side and not the renter side. What
must match is every id that appears on both.

Confirm against the chain itself, which is the only source that can't be faked locally:

```sh
curl -s "https://testnet.mirrornode.hedera.com/api/v1/accounts/$HEDERA_ACCOUNT_ID" \
  | python3 -c 'import json,sys; print(json.load(sys.stdin)["balance"]["balance"])'
```

## 9. Cross-verification gate (**costs 0.001 HBAR**)

The step that actually proves the Go merchant implementation is correct. With the node still running
in terminal A:

```sh
make smoke-agent
```

This is the **official `@x402/hedera` TypeScript client** — the reference implementation, with no
ClearGate-specific code — paying the Go agent and getting a job back.

**Pass:** `SMOKE PASS <transaction id>`, and terminal A shows a new job running.

If step 7 passes but this fails, our client and our server share a bug that cancels out. That is
exactly the failure a single end-to-end test cannot see, so do not skip this one.

## 10. The sandbox holds — **costs 0.001 HBAR**, optional but worth it

```sh
cleargate run -n http://localhost:8402 -i python:3.11-slim -s examples/escape.py
```

**Pass:** every line is `BLOCKED` except the last two, which write to `/work` and `/out`:

```
BLOCKED  outbound tcp: OSError: [Errno 101] Network is unreachable
BLOCKED  dns: gaierror: ...
BLOCKED  write /etc: OSError: [Errno 30] Read-only file system
BLOCKED  mount: ...
BLOCKED  raw socket: PermissionError
BLOCKED  docker socket: FileNotFoundError
ALLOWED  write /work: ...
ALLOWED  write /out: ...
```

Any `ALLOWED` on the first six is a security bug, not a test failure — stop and fix it.

The script also prints `uid=0`: jobs run as root *inside* the container. With `CapDrop: ALL`,
`no-new-privileges`, a read-only rootfs and no network that is a much smaller surface than it
sounds, but there is no user-namespace remapping yet, so this is a known residual risk rather than
something the test suite covers.

Wall-clock enforcement, same price:

```sh
cleargate run -n http://localhost:8402 -i python:3.11-slim -s examples/slow.py --timeout 20
```

**Pass:** the ticks stop at roughly 20 and the job ends `timeout`, not `succeeded`. A flat-fee job is
paid up front, so a timeout does not refund — that is the honest limitation metered leases (M3) fix.

## 10b. Train on the GPU (**costs 0.001 HBAR**) — needs step 2b

The payoff. Requires `gpu_enabled: true` and a working `nvidia` runtime.

```sh
cleargate quote -n http://localhost:8402       # must show a GPU, not CPU-fallback

cleargate run -n http://localhost:8402 \
  -i pytorch/pytorch:2.4.1-cuda12.1-cudnn9-runtime \
  --gpu \
  -s examples/train_mnist.py \
  -d https://storage.googleapis.com/tensorflow/tf-keras-datasets/mnist.npz \
  --dataset-sha256 731c5ac602752760c8e48fbffcf8c3b850d9dc2a2aedcf2cc48468fc17b673d1 \
  -o model.tar
```

That URL is real MNIST — 11.5 MB, public https, with exactly the `x_train`/`y_train`/`x_test`/
`y_test` keys the script expects. No `allow_private` or `allow_http` needed, unlike step 7b.

**Pass:** the script names the actual card, and the loss falls:

```
device    cuda — NVIDIA GeForce RTX 3050 Laptop GPU (4.0 GiB)
torch     2.4.1+cu121  cuda 12.1
dataset   1 file(s) staged at /data
          mnist.npz  11490434 bytes
shapes    train (60000, 1, 28, 28)  test (10000, 1, 28, 28)  classes 10
epoch 1/3  batch    0  loss 2.3016
...
epoch 3/3  mean loss 0.0776  accuracy 0.9784
wrote /out/model.pt (830376 bytes) and /out/metrics.json
done in 5.8s
```

Three epochs on 60,000 images takes **under six seconds** on a 3050. If it takes minutes, it is
running on the CPU — check `metrics.json` below rather than guessing.

Then confirm the model is real:

```sh
tar -xOf model.tar out/metrics.json
```

`"gpu"` must name the card and `"device": "cuda"`. If it says `cpu`, the job ran on the CPU despite
`--gpu` — which would be a bug worth stopping for, since the renter paid for a GPU.

The model is a real `state_dict`, not a placeholder. Load it to be sure:

```sh
docker run --rm -v $PWD/model.tar:/m.tar:ro \
  pytorch/pytorch:2.4.1-cuda12.1-cudnn9-runtime python -c "
import tarfile, torch, io
with tarfile.open('/m.tar') as t:
    sd = torch.load(io.BytesIO(t.extractfile('out/model.pt').read()), map_location='cpu')
print(list(sd.keys()))"
```

**Pass:** eight tensors — `conv1.weight`, `conv1.bias`, `conv2.*`, `fc1.*`, `fc2.*`.

With no `--dataset`, the script trains on synthetic data instead and still exercises payment,
staging and the artifact path. That keeps the demo runnable when no dataset URL is to hand.

**On 4 GB of VRAM**, the default batch of 128 fits comfortably. A CUDA OOM is caught and reported
with the batch size to retry at, rather than as a stack trace.

## 11. Nothing leaked — free

Stop the node in terminal A (Ctrl-C), then:

```sh
docker ps -a --filter label=cleargate.job
docker volume ls --filter label=cleargate.job
```

**Pass:** both empty. Artifacts are retained for 15 minutes after a job ends, so checking
immediately after a job you may still see one container and its **three** volumes — `work`, `out`
and `data`. That is correct. Kill the node mid-job and restart it: the startup sweep clears the
orphans and logs each one it removed.

---

## When something fails

| Symptom | Look here first |
|---|---|
| Payment "does nothing", no network call | Spend controls. `x402Client` permits only `findDefaultAsset` assets — on Hedera that is USDC `0.0.429274`, not HBAR. `client/src/pay.ts` must call `setSpendControls(false)` with its own policy. |
| `INVALID_SIGNATURE` or key parse error | Wrong `HEDERA_KEY_TYPE`. Flip between `ecdsa` and `ed25519` in `.env`. |
| `INSUFFICIENT_PAYER_BALANCE` | Renter account unfunded — top up at portal.hedera.com. |
| 402 loops forever, client never pays | The challenge bytes. Re-run step 5 and diff against `agent/internal/x402/testdata/golden_challenge.json`. |
| Settle fails after the container started | The node kills the job and returns 402. Check terminal A for `abandoning unpaid job`. Correct behaviour — no free compute. |
| `container rootfs is marked read-only` | A tmpfs does not exist until the container starts, but the script uploads before start. `/work` must be a named volume. |
| `No rule to make target` | You are not in the repo root. `make` reads only the current directory's Makefile; it never searches upwards. |
| `cleargate: command not found` | The PATH export in the prerequisites was not run in this shell. Use `pnpm exec cleargate ...`, or re-export. |
| `refusing to fetch from a private address` | Working as intended. A dataset on your LAN or on loopback needs `dataset.allow_private: true`, which an operator sets deliberately. |
| Dataset job fails right after payment | Look for `staging failed` in the node log. The HEAD preflight catches most bad URLs before payment; a server that answers HEAD but fails the GET will get through it. |
| `--gpu` refused on a node with a GPU | The card is present but Docker has no `nvidia` runtime, or `gpu_enabled` is still false. `/v1/specs` prints the exact reason. |
| Dashboard shows nothing | Its panes fill from live events. Run a job; a node with no history opens empty by design. |
| Port 8402 in use | An earlier `serve` is still running: `pgrep -af cleargate-node`. |
