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
#   bash -c "$(curl -fsSL https://raw.githubusercontent.com/YashIIT0909/ClearGate/main/scripts/install.sh)"
#
# It also works run in place as ./scripts/install.sh from an existing
# checkout (what a developer does), in which case it reuses that checkout
# instead of cloning one.
#
# No Hedera private key is involved, here or anywhere else on the provider
# side: the node receives payment, it never signs (CLAUDE.md invariant 1).
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

CONFIG="${CONFIG:-config.yaml}"
CLEARGATE_REPO="${CLEARGATE_REPO:-https://github.com/YashIIT0909/ClearGate.git}"
CLEARGATE_DIR="${CLEARGATE_DIR:-$HOME/.cleargate/ClearGate}"

leases_on() { [[ "$LEASES" == "1" || "$LEASES" == "true" ]]; }
gpu_on()    { [[ "$GPU" == "1" || "$GPU" == "true" ]]; }

if [[ -z "$PAY_TO" ]]; then
  echo "PAY_TO is required — the Hedera testnet account your earnings are paid into." >&2
  echo "Get one at https://portal.hedera.com, then re-run with PAY_TO=0.0.1234" >&2
  exit 1
fi

for tool in git go docker; do
  if ! command -v "$tool" >/dev/null 2>&1; then
    echo "$tool is not installed, and the node needs it." >&2
    exit 1
  fi
done

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

echo "==> configuring"
./bin/cleargate-node setup "${setup_args[@]}"

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

echo
echo "==> starting the node — ctrl-c to stop selling"
exec ./bin/cleargate-node tui --config "$CONFIG"
