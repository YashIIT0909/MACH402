#!/usr/bin/env bash
#
# The provider one-liner: fetch MACH402, build the node daemon, configure
# it, and start selling GPU time.
#
# The website hands out this exact form — it works with no prior checkout:
#
#   PAY_TO=0.0.1234 \
#   PUBLIC_URL=http://1.2.3.4:8402 \
#   REGISTRY_URL=http://localhost:4400 \
#   LEASE_PRICE_TINYBARS_PER_MINUTE=200000 \
#   bash -c "$(curl -fsSL https://raw.githubusercontent.com/YashIIT0909/MACH402/main/scripts/install.sh)"
#
# It also works run in place as ./scripts/install.sh from an existing
# checkout (what a developer does), in which case it reuses that checkout
# instead of cloning one.
#
# The operator key this script generates may
# come back unfunded on a first run — that is a Hedera account that does not
# exist until someone sends it HBAR, not a failure. `cleargate-node setup`
# prints the address and waits for funding on its own, so the whole install
# — build, configure, fund, start — happens in one sitting rather than two
# invocations of this script. CLAUDE.md's session lifecycle section describes
# what refunds change about the operator key's role.
#
# No Hedera private key is involved on the earnings side, here or anywhere
# else on the provider side: the node receives payment into PAY_TO, and PAY_TO
# never signs (CLAUDE.md invariant 1). The node also gets a *separate*,
# node-local operator key that pays its own gas and pays session refunds; it
# never touches PAY_TO or its earnings.
set -euo pipefail

PAY_TO="${PAY_TO:-}"
PUBLIC_URL="${PUBLIC_URL:-http://localhost:8402}"
REGISTRY_URL="${REGISTRY_URL:-}"

# What this node sells: a Jupyter server in a container on this machine, as a
# metered session — billed by the second, with unused credit refunded
# automatically from the node's operator key.
LEASE_PRICE_TINYBARS_PER_MINUTE="${LEASE_PRICE_TINYBARS_PER_MINUTE:-200000}"

# Which base the lease image is built from. Empty lets `make lease-image` pick
# from what the machine actually has: the CUDA base where the nvidia runtime is
# installed, and the CPU base otherwise — the lease side of the CPU fallback.
LEASE_BASE_IMAGE="${LEASE_BASE_IMAGE:-}"

# PRICE_TINYBARS, LEASES, HCS, SESSIONS and SELF_SETTLE used to choose between
# batch jobs, prepaid leases and optional refunds. A node now sells metered
# sessions and nothing else, with its audit topic and automatic refunds always
# on, so all five are ignored — still harmless to pass, which keeps commands
# copied from older docs working.

CONFIG="${CONFIG:-config.yaml}"
CLEARGATE_REPO="${CLEARGATE_REPO:-https://github.com/YashIIT0909/MACH402.git}"
CLEARGATE_DIR="${CLEARGATE_DIR:-$HOME/.cleargate/MACH402}"

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

# The Hedera sidecar (`cleargate-hedera`) is a TS workspace package, not part
# of the Go binary `make agent` builds — pnpm is how it gets onto this machine.
if ! command -v pnpm >/dev/null 2>&1; then
  echo "pnpm is required — it builds the Hedera signing sidecar that publishes and pays refunds." >&2
  echo "Install it (https://pnpm.io/installation) and re-run." >&2
  exit 1
fi

# ssh-keygen is a hard runtime dependency: it is
# what generates this node's certificate authority and signs each renter's
# certificate. Checking here means a missing binary fails now, with a sentence
# that explains it, rather than in the middle of a renter's paid request as an
# opaque "exec: ssh-keygen: not found".
if ! command -v ssh-keygen >/dev/null 2>&1; then
  echo "ssh-keygen is required — it signs the certificates that let renters in." >&2
  echo "Install your platform's openssh client package and re-run." >&2
  exit 1
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

install_cloudflared

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
  echo "==> cloning MACH402 into $CLEARGATE_DIR"
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
echo "==> installing the Hedera signing sidecar"
make install
hedera_sidecar="$repo_root/node_modules/.bin/cleargate-hedera"
if [[ ! -x "$hedera_sidecar" ]]; then
  echo "expected the sidecar at $hedera_sidecar after \`make install\` but did not find it" >&2
  exit 1
fi

# A lease settles only once its container is up and reachable, so there is no
# staging phase to hide an image pull in — the image has to be on the box
# before the first renter arrives. Built locally rather than pulled from a
# registry: standing one up is not a prerequisite for renting out a GPU, and
# the egress proxy every byte of a lease passes through should be built from
# source rather than trusted as somebody's published tag.
echo "==> building the lease images (this is the slow part of a first install)"
if [[ -n "$LEASE_BASE_IMAGE" ]]; then
  make lease-image LEASE_BASE_IMAGE="$LEASE_BASE_IMAGE"
else
  make lease-image
fi

setup_args=(
  --config "$CONFIG"
  --pay-to "$PAY_TO"
  --public-url "$PUBLIC_URL"
  --lease-price-tinybars-per-minute "$LEASE_PRICE_TINYBARS_PER_MINUTE"
  --hedera-sidecar "$hedera_sidecar"
  --force
)

if [[ -n "$REGISTRY_URL" ]]; then
  setup_args+=(--registry-url "$REGISTRY_URL")
fi

# No GPU switch: setup always turns the GPU on. The node re-checks that against
# the nvidia container runtime and falls back to CPU if the card cannot actually
# be passed through, so a machine without one still runs — it just is not
# advertised as having a GPU.

echo "==> configuring"
# If this node's operator key has no HBAR yet, setup prints the address and
# waits for it to be funded rather than exiting — so if the key was just
# generated, this is the point at which the script pauses
# for you to send a few testnet HBAR from https://portal.hedera.com. It
# continues on its own the moment that lands; ctrl-c aborts and re-running the
# whole script picks up from a config that already has everything but Hedera.
./bin/cleargate-node setup "${setup_args[@]}"

# setup exits 0 without writing $CONFIG when the operator key never got funded within its wait window — that is a
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

cat <<'NOTE'

==> a note on metered sessions

Renters pay for interactive time in chunks of credit that burn by the second,
and whatever they do not use is refunded automatically from this node's own
operator account. That account needs to hold more than a fee float — `setup`
just checked whether it does. Earnings are untouched: they still land in PAY_TO,
which signs nothing, so a compromise of the operator key can only ever cost what
is sitting in it for refunds — never your earnings.

NOTE

echo
echo "==> starting the node — ctrl-c to stop selling"
exec ./bin/cleargate-node tui --config "$CONFIG"
