# The lease runtime: an SSH shell and a Jupyter server, in one container, on the
# provider's GPU.
#
# This is the image a renter is actually buying time on. It differs from the
# batch-job images in the node's allowlist in one important way: nothing is
# uploaded into it. The renter's code and data stay on the renter's machine and
# arrive over their own SSH session or their own notebook, which is the whole
# point of leasing rather than submitting a job.
#
# Build it with `make lease-image`, which picks the base from what the machine
# actually has: the CUDA base where the nvidia runtime is installed, a plain
# Python base otherwise. Passing a GPU into a container with no CUDA toolkit
# gives a lease that reports a card and cannot compute on it, which is worse
# than selling CPU honestly — so the node refuses to sell `require_gpu` leases
# on a CUDA-less image rather than letting a renter find out afterwards.
ARG BASE_IMAGE=python:3.11-slim
FROM ${BASE_IMAGE}

# tini reaps the zombies a long-lived interactive container inevitably produces;
# without a real init, every ssh session that exits badly leaves one behind.
RUN apt-get update && apt-get install -y --no-install-recommends \
      openssh-server \
      tini \
      ca-certificates \
      curl \
      git \
      nano \
      procps \
    && rm -rf /var/lib/apt/lists/*

# jupyter_http_over_ws is what lets Google Colab's "Connect to a local runtime"
# talk to this server. Without it Colab reaches the URL and still refuses to
# attach.
#
# Notebook is pinned below 7 because that extension is the constraint, not a
# preference: it imports `notebook.base`, which Notebook 7 removed, so on a
# modern JupyterLab stack it fails to load with
# "No module named 'notebook.base'" and Colab connect is silently dead. The
# classic server is the one Colab's own local-runtime instructions target.
RUN pip install --no-cache-dir --root-user-action=ignore \
      "notebook>=6.5.4,<7" \
      "jupyter_http_over_ws>=0.0.8" \
 && jupyter serverextension enable --py jupyter_http_over_ws --sys-prefix

# Optional extras, baked in at build time rather than left to the renter.
#
# Deliberately not the answer to TensorFlow. TF cannot use the GPU alongside the
# PyTorch CUDA base at all: its nvidia-* wheels resolve to versions torch pins
# against, and the result is "Cannot dlopen some GPU libraries" and a silent
# fall back to CPU. Measured three ways on this image — `tensorflow`,
# `tensorflow[and-cuda]`, and an isolated virtualenv — all CPU. A provider who
# wants TensorFlow renters changes BASE_IMAGE instead of adding a package.
ARG EXTRA_PIP=""
RUN if [ -n "$EXTRA_PIP" ]; then \
      pip install --no-cache-dir --root-user-action=ignore $EXTRA_PIP; \
    fi

COPY sshd_config /etc/ssh/sshd_config
COPY entrypoint.sh /usr/local/bin/cleargate-lease-entrypoint
RUN chmod +x /usr/local/bin/cleargate-lease-entrypoint \
 && mkdir -p /etc/ssh/principals /run/sshd /workspace

WORKDIR /workspace

# 22 for the shell, 8888 for the notebook. Neither is ever published to the
# provider's host: cloudflared dials the container directly on its own network.
EXPOSE 22 8888

ENTRYPOINT ["/usr/bin/tini", "--", "/usr/local/bin/cleargate-lease-entrypoint"]
