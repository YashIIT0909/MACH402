#!/usr/bin/env bash
#
# The provider one-liner: fetch ClearGate, build the node daemon, configure
# it, and start selling GPU time.
#
# The website hands out this exact form — it works with no prior checkout:
#
#   PAY_TO=0.0.1234 \
#   PRICE_TINYBARS=100000 \
#   PUBLIC_URL=http://1.2.3.4:8402 \
#   REGISTRY_URL=http://localhost:4400 \
#   GPU=1 \
#   LEASES=1 \
#   SESSIONS=1 \
#   SELF_SETTLE=1 \
#   bash -c "$(curl -fsSL https://raw.githubusercontent.com/YashIIT0909/ClearGate/main/scripts/install.sh)"
#
# It also works run in place as ./scripts/install.sh from an existing
# checkout (what a developer does), in which case it reuses that checkout
# instead of cloning one.
#
# With SESSIONS=1 (or HCS=1 alone), the operator key this script generates may
# come back unfunded on a first run — that is a Hedera account that does not
# exist until someone sends it HBAR, not a failure. `cleargate-node setup`
# prints the address and waits for funding on its own, so the whole install
# — build, configure, fund, start — happens in one sitting rather than two
# invocations of this script. See --self-settle in CLAUDE.md's session
# lifecycle section for what SELF_SETTLE actually changes about the operator
# key's role.
#
# No Hedera private key is involved on the earnings side, here or anywhere
# else on the provider side: the node receives payment into PAY_TO, and PAY_TO
# never signs (CLAUDE.md invariant 1). HCS=1 and SESSIONS=1 add a *separate*,
# node-local operator key that pays its own gas and — only with
# SELF_SETTLE=1 — pays session refunds; it never touches PAY_TO or its
# earnings.
set -euo pipefail

PAY_TO="${PAY_TO:-}"
PRICE_TINYBARS="${PRICE_TINYBARS:-100000}"
PUBLIC_URL="${PUBLIC_URL:-http://localhost:8402}"
REGISTRY_URL="${REGISTRY_URL:-}"
GPU="${GPU:-0}"

# Timed interactive access: an SSH shell and a Jupyter server, in a container,
# on this machine. Separate from GPU on purpose — letting a stranger open a
# shell on your box is a bigger ask than running their sandboxed batch job, and
# it must be its own decision rather than a side effect of turning on the card.
LEASES="${LEASES:-0}"
LEASE_PRICE_TINYBARS_PER_MINUTE="${LEASE_PRICE_TINYBARS_PER_MINUTE:-200000}"

# "quick" needs no Cloudflare account and gives renters Jupyter over a random
# hostname. "named" asks the registry to provision a stable tunnel for this
# node, which is what adds an SSH terminal.
TUNNEL_MODE="${TUNNEL_MODE:-quick}"

# Which base the lease image is built from. A machine with a card wants the
# CUDA base, or a renter's torch will not see the GPU they paid for.
LEASE_BASE_IMAGE="${LEASE_BASE_IMAGE:-}"

# The audit trail: every settlement, and — with SESSIONS=1 — the running
# refund-owed figure a metered session publishes every 15 seconds, on a Hedera
# Consensus Service topic this provider owns. Off by default because it is the
# first thing that puts key material on this machine (CLAUDE.md's "the agent
# never holds a private key" is about the merchant side; this is the sidecar's
# own node-local operator key, described in the README's HCS section).
HCS="${HCS:-0}"

# Metered, refundable interactive time instead of forward-paid leases. Implies
# HCS=1 whether or not it was set: the published burn trail is what makes
# prepaying a stranger checkable at all, not an optional extra on top of it.
SESSIONS="${SESSIONS:-0}"

# Whether this node pays a session's unburned credit back automatically, from
# its own operator account, instead of leaving that for the provider to do by
# hand. Real money moves through the operator key with this on — see the note
# printed after setup.
SELF_SETTLE="${SELF_SETTLE:-0}"

CONFIG="${CONFIG:-config.yaml}"
CLEARGATE_REPO="${CLEARGATE_REPO:-https://github.com/YashIIT0909/ClearGate.git}"
CLEARGATE_DIR="${CLEARGATE_DIR:-$HOME/.cleargate/ClearGate}"

leases_on()      { [[ "$LEASES" == "1" || "$LEASES" == "true" ]]; }
gpu_on()         { [[ "$GPU" == "1" || "$GPU" == "true" ]]; }
sessions_on()    { [[ "$SESSIONS" == "1" || "$SESSIONS" == "true" ]]; }
hcs_on()         { [[ "$HCS" == "1" || "$HCS" == "true" ]] || sessions_on; }
self_settle_on() { [[ "$SELF_SETTLE" == "1" || "$SELF_SETTLE" == "true" ]]; }

if [[ -z "$PAY_TO" ]]; then
  echo "PAY_TO is required — the Hedera testnet account your earnings are paid into." >&2
  echo "Get one at https://portal.hedera.com, then re-run with PAY_TO=0.0.1234" >&2
  exit 1
fi

# Sessions are a way of paying for leases, not a feature on their own — the
# same rule `cleargate-node setup` enforces, checked here too so this fails in
# a second rather than after building lease images that will not be used.
if sessions_on && ! leases_on; then
  echo "SESSIONS=1 only applies to interactive time; re-run with LEASES=1 as well." >&2
  exit 1
fi

for tool in git go docker; do
  if ! command -v "$tool" >/dev/null 2>&1; then
    echo "$tool is not installed, and the node needs it." >&2
    exit 1
  fi
done

# The Hedera sidecar (`cleargate-hedera`) is a TS workspace package, not part
# of the Go binary `make agent` builds — HCS and metered sessions need pnpm to
# get it onto this machine at all.
if hcs_on; then
  if ! command -v pnpm >/dev/null 2>&1; then
    echo "pnpm is required when HCS=1 or SESSIONS=1 — it builds the Hedera signing sidecar." >&2
    echo "Install it (https://pnpm.io/installation) and re-run." >&2
    exit 1
  fi
fi

# ssh-keygen becomes a hard runtime dependency the moment leasing is on: it is
# what generates this node's certificate authority and signs each renter's
# certificate. Checking here means a missing binary fails now, with a sentence
# that explains it, rather than in the middle of a renter's paid request as an
# opaque "exec: ssh-keygen: not found".
if leases_on; then
  if ! command -v ssh-keygen >/dev/null 2>&1; then
    echo "ssh-keygen is required when LEASES=1 — it signs the certificates that let renters in." >&2
    echo "Install your platform's openssh client package and re-run." >&2
    exit 1
  fi
fi

# cloudflared is what lets a renter reach this machine at all, and unlike
# git/go/docker almost no provider already has it. Install it rather than
# erroring out: "you need one more binary, go find it" is where a one-line
# installer stops being one.
install_cloudflared() {
  if command -v cloudflared >/dev/null 2>&1; then
    return 0
  fi

  echo "==> installing cloudflared"
  case "$(uname -s)" in
    Darwin)
      if command -v brew >/dev/null 2>&1; then
        brew install cloudflared
      else
        echo "cloudflared is missing and Homebrew is not installed." >&2
        echo "Install it from https://developers.cloudflare.com/cloudflare-one/connections/connect-networks/downloads/" >&2
        return 1
      fi
      ;;
    Linux)
      local arch
      case "$(uname -m)" in
        x86_64)         arch=amd64 ;;
        aarch64|arm64)  arch=arm64 ;;
        armv7l)         arch=arm ;;
        *)
          echo "no cloudflared build for $(uname -m); install it by hand and re-run" >&2
          return 1
          ;;
      esac

      # The distro-agnostic binary rather than the apt repository: it is one
      # file, it works the same on every Linux, and it does not need this
      # script to start editing a provider's package sources.
      local url="https://github.com/cloudflare/cloudflared/releases/latest/download/cloudflared-linux-${arch}"
      local target="/usr/local/bin/cloudflared"
      local tmp
      tmp="$(mktemp)"
      curl -fsSL "$url" -o "$tmp"
      chmod +x "$tmp"
      if [[ -w "$(dirname "$target")" ]]; then
        mv "$tmp" "$target"
      else
        echo "    (needs sudo to write $target)"
        sudo mv "$tmp" "$target"
      fi
      ;;
    *)
      echo "cloudflared must be installed by hand on $(uname -s)" >&2
      return 1
      ;;
  esac
}

if leases_on && [[ "$TUNNEL_MODE" != "off" ]]; then
  install_cloudflared
fi

# Piped in via curl | bash, this script has no file of its own to find a repo
# root from — $BASH_SOURCE points at a fd, not a path — so clone (or reuse a
# prior clone) under $CLEARGATE_DIR instead. Run in place from an existing
# checkout, BASH_SOURCE does resolve to a real file, so that checkout is used
# as-is rather than cloning a second copy.
script_path="${BASH_SOURCE[0]:-}"
if [[ -n "$script_path" && -f "$script_path" ]]; then
  repo_root="$(cd "$(dirname "$script_path")/.." && pwd)"
elif [[ -d "$CLEARGATE_DIR/.git" ]]; then
  echo "==> updating existing checkout at $CLEARGATE_DIR"
  git -C "$CLEARGATE_DIR" pull --ff-only
  repo_root="$CLEARGATE_DIR"
else
  echo "==> cloning ClearGate into $CLEARGATE_DIR"
  git clone --depth 1 "$CLEARGATE_REPO" "$CLEARGATE_DIR"
  repo_root="$CLEARGATE_DIR"
fi
cd "$repo_root"

echo "==> building cleargate-node"
make agent

# The sidecar (`cleargate-hedera`) lives in the TS workspace, not the Go
# module `make agent` just built. `make install` (a `pnpm install` at the repo
# root) puts it at node_modules/.bin/cleargate-hedera. That absolute path, not
# a bare command name, is what gets handed to `setup` below — a bare name
# would be resolved off whatever PATH happens to be in scope, which works in
# the terminal running this script and silently stops working the next time
# the node is started from a fresh shell, cron, or a systemd unit.
hedera_sidecar=""
if hcs_on; then
  echo "==> installing the Hedera signing sidecar"
  make install
  hedera_sidecar="$repo_root/node_modules/.bin/cleargate-hedera"
  if [[ ! -x "$hedera_sidecar" ]]; then
    echo "expected the sidecar at $hedera_sidecar after \`make install\` but did not find it" >&2
    exit 1
  fi
fi

# A lease settles only once its container is up and reachable, so there is no
# staging phase to hide an image pull in — the image has to be on the box
# before the first renter arrives. Built locally rather than pulled from a
# registry: standing one up is not a prerequisite for renting out a GPU, and
# the egress proxy every byte of a lease passes through should be built from
# source rather than trusted as somebody's published tag.
if leases_on; then
  echo "==> building the lease images (this is the slow part of a first install)"
  if [[ -z "$LEASE_BASE_IMAGE" ]] && gpu_on; then
    LEASE_BASE_IMAGE="pytorch/pytorch:2.4.1-cuda12.1-cudnn9-runtime"
    echo "    using the CUDA base so renters' torch sees your card"
  fi
  if [[ -n "$LEASE_BASE_IMAGE" ]]; then
    make lease-image LEASE_BASE_IMAGE="$LEASE_BASE_IMAGE"
  else
    make lease-image
  fi
fi

setup_args=(
  --config "$CONFIG"
  --pay-to "$PAY_TO"
  --price-tinybars "$PRICE_TINYBARS"
  --public-url "$PUBLIC_URL"
  --force
)

if [[ -n "$REGISTRY_URL" ]]; then
  setup_args+=(--registry-url "$REGISTRY_URL")
fi

# The node re-checks this against the nvidia container runtime and quietly falls
# back to CPU if the card cannot actually be passed through, so asking for a GPU
# you do not have is safe — it just will not be advertised as one.
if gpu_on; then
  setup_args+=(--gpu)
fi

if leases_on; then
  setup_args+=(
    --enable-leases
    --lease-price-tinybars-per-minute "$LEASE_PRICE_TINYBARS_PER_MINUTE"
    --tunnel-mode "$TUNNEL_MODE"
  )
fi

if hcs_on; then
  setup_args+=(--enable-hcs --hedera-sidecar "$hedera_sidecar")
fi
if sessions_on; then
  setup_args+=(--enable-sessions)
fi
if self_settle_on; then
  setup_args+=(--self-settle)
fi

echo "==> configuring"
# If this node's operator key has no HBAR yet, setup prints the address and
# waits for it to be funded rather than exiting — so if HCS or SESSIONS is on
# and the key was just generated, this is the point at which the script pauses
# for you to send a few testnet HBAR from https://portal.hedera.com. It
# continues on its own the moment that lands; ctrl-c aborts and re-running the
# whole script picks up from a config that already has everything but Hedera.
./bin/cleargate-node setup "${setup_args[@]}"

# setup exits 0 without writing $CONFIG when Hedera features were requested
# and the operator key never got funded within its wait window — that is a
# provider choosing not to fund it yet, not a script failure, so it is not
# treated as one. But starting the dashboard against a config that does not
# exist would be a confusing way to find that out.
if [[ ! -f "$CONFIG" ]]; then
  echo
  echo "==> not started: $CONFIG was not written."
  echo "    This means the operator key is still unfunded. Send it a few testnet HBAR"
  echo "    (https://portal.hedera.com), then re-run this exact command — everything"
  echo "    already built (the binary, the lease images) will not be rebuilt."
  exit 0
fi

if leases_on; then
  cat <<'NOTE'

==> a note on leasing

Renters get a root shell inside a container on this machine. That container has
every Linux capability dropped but the handful sshd needs, cannot gain
privileges, and reaches the network only through a proxy that denies everything
except package and model registries.

Root in a container is still not nothing. If your Docker daemon does not have
user-namespace remapping on, enable it — it is what makes in-container root map
to an unprivileged user on your host:

  https://docs.docker.com/engine/security/userns-remap/

NOTE
fi

if sessions_on; then
  if self_settle_on; then
    cat <<'NOTE'

==> a note on metered sessions

A renter's unburned credit is refunded from this node's own operator account
(SELF_SETTLE=1) — that account now needs to hold more than a fee float, and
`setup` just checked whether it does. Earnings are untouched: they still land
in PAY_TO, which signs nothing, so a compromise of the operator key can only
ever cost what is sitting in it for refunds — never your earnings.

NOTE
  else
    cat <<'NOTE'

==> a note on metered sessions

This node meters sessions and publishes what it owes to its own HCS topic, but
SELF_SETTLE is off — refunds are not paid automatically. When a session ends
owing a renter money, the node logs it and the amount stays on the audit
topic; pay it by hand, or re-run this script with SELF_SETTLE=1.

NOTE
  fi
fi

echo
echo "==> starting the node — ctrl-c to stop selling"
exec ./bin/cleargate-node tui --config "$CONFIG"
