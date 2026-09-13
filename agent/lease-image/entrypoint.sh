#!/usr/bin/env bash
#
# Starts the two things a lease is: a shell and a notebook.
#
# Everything this needs arrives as environment variables set by the node when it
# created the container. Nothing is baked into the image, so the same image
# serves every lease and carries no secret between them.
#
#   SSH_CA_PUBKEY     the node's certificate authority, public half only
#   LEASE_PRINCIPAL   the certificate principal this container will accept
#   JUPYTER_TOKEN     the notebook server's token, minted per lease
#   LEASE_EXPIRES_AT  when the currently paid slice runs out, for the motd
set -euo pipefail

: "${SSH_CA_PUBKEY:?the node must supply its CA public key}"
: "${LEASE_PRINCIPAL:?the node must supply the certificate principal for this lease}"
: "${JUPYTER_TOKEN:?the node must supply a Jupyter token}"

# Only the CA's public half is ever in here. The private half stays on the
# provider's machine and never enters a container.
printf '%s\n' "$SSH_CA_PUBKEY" > /etc/ssh/cleargate_ca.pub
chmod 644 /etc/ssh/cleargate_ca.pub

# The single principal this container accepts. A certificate minted for another
# lease is signed by the same CA and still refused here.
mkdir -p /etc/ssh/principals
printf '%s\n' "$LEASE_PRINCIPAL" > /etc/ssh/principals/root
chmod 644 /etc/ssh/principals/root

# Host keys are generated per container rather than baked into the image, so two
# leases on two different machines never present the same host identity.
ssh-keygen -A >/dev/null
mkdir -p /run/sshd

# What the GPU story actually is in this container, decided at start rather
# than asserted: a renter who runs `pip install tensorflow` and watches it fall
# back to CPU has no way to tell whether the card is missing, the passthrough
# is broken, or the wheel simply cannot find CUDA. Answer it up front.
gpu_note="no GPU is attached to this lease"
if command -v nvidia-smi >/dev/null 2>&1 && nvidia-smi -L >/dev/null 2>&1; then
  gpu_note="$(nvidia-smi --query-gpu=name --format=csv,noheader 2>/dev/null | head -1)"
  if python -c "import ctypes,sys; sys.exit(0 if ctypes.CDLL('libcuda.so.1').cuInit(0)==0 else 1)" 2>/dev/null; then
    gpu_note="$gpu_note (CUDA ready)"
  else
    gpu_note="$gpu_note (visible, but CUDA will not initialise)"
  fi
fi

cat > /etc/motd <<MOTD

  ClearGate lease ${LEASE_ID:-}
  paid until ${LEASE_EXPIRES_AT:-unknown}

  GPU: ${gpu_note}

  /workspace is yours and is wiped when this lease ends.
  Network access goes through an allowlist proxy: package and model registries
  work, arbitrary hosts do not.

  This is a PyTorch box: torch is installed and already sees the GPU.

  TensorFlow will NOT get the GPU here, whatever you install. \`tensorflow\`,
  \`tensorflow[and-cuda]\`, and a separate virtualenv were all measured on this
  image and all end at "Cannot dlopen some GPU libraries" and run on CPU: TF's
  CUDA wheels and the ones this base pins for torch cannot coexist. It is a
  property of the image, not something to debug.

  If you need TensorFlow on a GPU, rent a node whose provider built their lease
  image on a TensorFlow base. A node's free /v1/specs shows what it runs.

MOTD

echo "cleargate: GPU — ${gpu_note}"

echo "cleargate: starting sshd"
/usr/sbin/sshd -D -e &
sshd_pid=$!

# Bound to 0.0.0.0 inside this container's own network namespace, which is not
# the host's: the container sits on an internal Docker network with no route
# out, and the only things that can dial this port are the node's cloudflared
# and a renter's own forwarded SSH session.
#
# allow_origin is what lets Colab's "Connect to a local runtime" attach; without
# it Colab reaches the server and is refused by the browser. It is deliberately
# exactly Colab's origin and nothing else — a wildcard here would let any page
# the renter visits talk to their notebook server.
#
# A renter connecting from something that is not Colab — VS Code, or a browser
# on the tunnel URL — does not go through that path and does not need it.
echo "cleargate: starting jupyter"
jupyter notebook \
  --ip=0.0.0.0 \
  --port=8888 \
  --no-browser \
  --allow-root \
  --NotebookApp.token="$JUPYTER_TOKEN" \
  --NotebookApp.allow_origin='https://colab.research.google.com' \
  --NotebookApp.allow_remote_access=True \
  --NotebookApp.port_retries=0 \
  --NotebookApp.notebook_dir=/workspace &
jupyter_pid=$!

# If either half dies the lease is broken, not half-working. Exiting lets the
# node see a stopped container and reap it rather than keep charging for a
# session the renter cannot use.
wait -n "$sshd_pid" "$jupyter_pid"
echo "cleargate: a lease process exited; shutting the container down" >&2
kill "$sshd_pid" "$jupyter_pid" 2>/dev/null || true
exit 1
