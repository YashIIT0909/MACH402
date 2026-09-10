#!/usr/bin/env bash
#
# The provider one-liner: build the node daemon, configure it, and start
# selling GPU time.
#
# Run it from a ClearGate checkout. Everything is passed as an environment
# variable so the website can hand a provider a command with their own values
# already filled in:
#
#   PAY_TO=0.0.1234 \
#   PRICE_TINYBARS=100000 \
#   PUBLIC_URL=http://1.2.3.4:8402 \
#   REGISTRY_URL=http://localhost:4400 \
#   GPU=1 \
#   ./scripts/install.sh
#
# No Hedera private key is involved, here or anywhere else on the provider
# side: the node receives payment, it never signs (CLAUDE.md invariant 1).
set -euo pipefail

PAY_TO="${PAY_TO:-}"
PRICE_TINYBARS="${PRICE_TINYBARS:-100000}"
PUBLIC_URL="${PUBLIC_URL:-http://localhost:8402}"
REGISTRY_URL="${REGISTRY_URL:-}"
GPU="${GPU:-0}"
CONFIG="${CONFIG:-config.yaml}"

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"

if [[ -z "$PAY_TO" ]]; then
  echo "PAY_TO is required — the Hedera testnet account your earnings are paid into." >&2
  echo "Get one at https://portal.hedera.com, then re-run with PAY_TO=0.0.1234" >&2
  exit 1
fi

for tool in go docker; do
  if ! command -v "$tool" >/dev/null 2>&1; then
    echo "$tool is not installed, and the node needs it." >&2
    exit 1
  fi
done

echo "==> building cleargate-node"
make agent

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
if [[ "$GPU" == "1" || "$GPU" == "true" ]]; then
  setup_args+=(--gpu)
fi

echo "==> configuring"
./bin/cleargate-node setup "${setup_args[@]}"

echo
echo "==> starting the node — ctrl-c to stop selling"
exec ./bin/cleargate-node serve --config "$CONFIG"
