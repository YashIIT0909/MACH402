#!/usr/bin/env node
/**
 * A stand-in registry for working on the website without Postgres or a GPU.
 *
 * It serves the same read routes as `registry/` from a fixed table of nodes,
 * so `/nodes` can be styled against every state that page has to render:
 * an available GPU node, a CPU-fallback node, a paused one, and one that has
 * gone offline. Writes are refused — nothing here claims a listing.
 *
 * This is a development fixture, not a second implementation. The real
 * registry is `registry/`; run that against Postgres for anything real.
 *
 *   node scripts/sample-registry.mjs      # or: make dev-registry-sample
 */
import { createServer } from "node:http";

const PORT = Number(process.env.PORT ?? 4400);

/** Matches the real registry's freshness window, so `online` means the same thing. */
const ONLINE_WINDOW_SECONDS = 90;

const now = Date.now();
const secondsAgo = (seconds) => new Date(now - seconds * 1000).toISOString();
const daysAgo = (days) => new Date(now - days * 86_400_000).toISOString();

/**
 * One row per node, in the shape `NodeListing` from packages/types.
 *
 * Prices are strings in tinybars, never numbers: the rest of ClearGate treats
 * amounts as strings end to end and the browse page is no exception.
 */
const NODES = [
  {
    node_id: "node_4f2a9c1e7b3d5086",
    agent_version: "0.1.0",
    public_url: "http://192.168.1.42:8402",
    pay_to: "0.0.8011510",
    price_tinybars: "100000",
    facilitator_url: "https://api.testnet.blocky402.com",
    network: "hedera:testnet",
    asset: "0.0.0",
    fee_payer: "0.0.7162784",
    paused: false,
    gpu: { available: true, model: "NVIDIA GeForce RTX 4090", vram_mb: 24564 },
    limits: { max_seconds: 3600, memory_mb: 32768, cpu_cores: 8, max_artifact_mb: 2048 },
    image_allowlist: ["python:3.11-slim", "pytorch/pytorch:2.4.1-cuda12.1-cudnn9-runtime"],
    // Sells both products, with a lease that can actually use the card.
    leases: {
      price_tinybars_per_minute: "200000",
      min_minutes: 15,
      max_minutes: 240,
      max_total_minutes: 1440,
      ssh: true,
      jupyter: true,
      gpu: true,
      memory_mb: 32768,
      cpu_cores: 8,
      workspace_gb: 64,
      egress_allowlist: ["pypi.org", "files.pythonhosted.org", "huggingface.co"],
    },
    online: true,
    first_seen_at: daysAgo(12),
    last_seen_at: secondsAgo(4),
  },
  {
    node_id: "node_db8cc286021b15fe",
    agent_version: "0.1.0",
    public_url: "http://localhost:8402",
    pay_to: "0.0.10446279",
    price_tinybars: "100000",
    facilitator_url: "https://api.testnet.blocky402.com",
    network: "hedera:testnet",
    asset: "0.0.0",
    fee_payer: "0.0.7162784",
    paused: false,
    gpu: { available: true, model: "NVIDIA GeForce RTX 3050 Laptop GPU", vram_mb: 4096 },
    limits: { max_seconds: 900, memory_mb: 4096, cpu_cores: 2, max_artifact_mb: 512 },
    image_allowlist: ["python:3.11-slim", "pytorch/pytorch:2.4.1-cuda12.1-cudnn9-runtime"],
    // The awkward case worth being able to see: an HTTP-only tunnel, so no
    // SSH, on a lease image without CUDA, so the host's card is no use inside
    // a lease even though `gpu.available` is true.
    leases: {
      price_tinybars_per_minute: "60000",
      min_minutes: 10,
      max_minutes: 120,
      max_total_minutes: 480,
      ssh: false,
      jupyter: true,
      gpu: false,
      memory_mb: 4096,
      cpu_cores: 2,
      workspace_gb: 16,
      egress_allowlist: ["pypi.org", "files.pythonhosted.org"],
    },
    online: true,
    first_seen_at: daysAgo(3),
    last_seen_at: secondsAgo(11),
  },
  {
    node_id: "node_91b0e4d7a2c68f35",
    agent_version: "0.1.0",
    public_url: "https://gpu-fra1.example.net",
    pay_to: "0.0.9204411",
    price_tinybars: "2500000",
    facilitator_url: "https://api.testnet.blocky402.com",
    network: "hedera:testnet",
    asset: "0.0.0",
    fee_payer: "0.0.7162784",
    paused: false,
    gpu: { available: true, model: "NVIDIA A100-SXM4-40GB", vram_mb: 40960 },
    limits: { max_seconds: 7200, memory_mb: 131072, cpu_cores: 16, max_artifact_mb: 4096 },
    image_allowlist: ["pytorch/pytorch:2.4.1-cuda12.1-cudnn9-runtime"],
    online: true,
    first_seen_at: daysAgo(27),
    last_seen_at: secondsAgo(22),
  },
  {
    // The honest CPU-fallback listing: a card the daemon cannot pass through is
    // advertised as no card at all, with the reason attached.
    node_id: "node_6c7e15af90d2b483",
    agent_version: "0.1.0",
    public_url: "http://10.0.0.7:8402",
    pay_to: "0.0.7788221",
    price_tinybars: "40000",
    facilitator_url: "https://api.testnet.blocky402.com",
    network: "hedera:testnet",
    asset: "0.0.0",
    fee_payer: "0.0.7162784",
    paused: false,
    gpu: {
      available: false,
      model: null,
      vram_mb: null,
      reason: "gpu_enabled is false in config; running in CPU-fallback mode",
    },
    limits: { max_seconds: 600, memory_mb: 8192, cpu_cores: 4, max_artifact_mb: 512 },
    image_allowlist: ["python:3.11-slim"],
    online: true,
    first_seen_at: daysAgo(1),
    last_seen_at: secondsAgo(37),
  },
  {
    // Online but not selling: the operator wants their machine back without
    // killing the jobs already running on it.
    node_id: "node_2ad8f309c4e71b6a",
    agent_version: "0.1.0",
    public_url: "http://192.168.1.88:8402",
    pay_to: "0.0.8451190",
    price_tinybars: "750000",
    facilitator_url: "https://api.testnet.blocky402.com",
    network: "hedera:testnet",
    asset: "0.0.0",
    fee_payer: "0.0.7162784",
    paused: true,
    gpu: { available: true, model: "NVIDIA GeForce RTX 3090", vram_mb: 24576 },
    limits: { max_seconds: 1800, memory_mb: 16384, cpu_cores: 8, max_artifact_mb: 1024 },
    image_allowlist: ["python:3.11-slim", "pytorch/pytorch:2.4.1-cuda12.1-cudnn9-runtime"],
    online: true,
    first_seen_at: daysAgo(8),
    last_seen_at: secondsAgo(19),
  },
  {
    // Stopped beating. The row stays: a renter looking for a node they used
    // yesterday should find it listed as offline rather than silently gone.
    node_id: "node_ff3410e6b8d92057",
    agent_version: "0.1.0",
    public_url: "http://84.21.190.14:8402",
    pay_to: "0.0.6620913",
    price_tinybars: "150000",
    facilitator_url: "https://api.testnet.blocky402.com",
    network: "hedera:testnet",
    asset: "0.0.0",
    paused: false,
    gpu: { available: true, model: "NVIDIA GeForce RTX 4070 Ti", vram_mb: 12282 },
    limits: { max_seconds: 1200, memory_mb: 16384, cpu_cores: 6, max_artifact_mb: 1024 },
    image_allowlist: ["python:3.11-slim"],
    online: false,
    first_seen_at: daysAgo(19),
    last_seen_at: secondsAgo(2_400),
  },
];

function send(res, status, body) {
  const payload = JSON.stringify(body);
  res.writeHead(status, {
    "Content-Type": "application/json",
    "Cache-Control": "no-store",
    "Content-Length": Buffer.byteLength(payload),
  });
  res.end(payload);
}

const server = createServer((req, res) => {
  const url = new URL(req.url ?? "/", `http://localhost:${PORT}`);

  if (req.method !== "GET") {
    return send(res, 405, { error: "the sample registry is read-only; run the real one to accept heartbeats" });
  }

  if (url.pathname === "/health") {
    return send(res, 200, { ok: true, online_window_seconds: ONLINE_WINDOW_SECONDS, sample: true });
  }

  if (url.pathname === "/v1/nodes") {
    const onlyOnline = url.searchParams.get("online") === "true";
    const nodes = onlyOnline ? NODES.filter((node) => node.online) : NODES;
    // Online first, then most recently seen — the real registry's ordering.
    const sorted = [...nodes].sort(
      (a, b) =>
        Number(b.online) - Number(a.online) ||
        Date.parse(b.last_seen_at) - Date.parse(a.last_seen_at),
    );
    return send(res, 200, { nodes: sorted, online_window_seconds: ONLINE_WINDOW_SECONDS });
  }

  const match = /^\/v1\/nodes\/([^/]+)$/.exec(url.pathname);
  if (match) {
    const node = NODES.find((candidate) => candidate.node_id === match[1]);
    return node === undefined ? send(res, 404, { error: "no such node" }) : send(res, 200, node);
  }

  send(res, 404, { error: "not found" });
});

server.listen(PORT, () => {
  console.log(`sample registry  http://localhost:${PORT}`);
  console.log(`nodes            ${NODES.length} (${NODES.filter((n) => n.online).length} online)`);
  console.log(`routes           GET /health, /v1/nodes, /v1/nodes/:id`);
  console.log(`\nfixture data only — no database, no heartbeats, no money.`);
});
