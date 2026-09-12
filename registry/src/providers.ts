/**
 * The agent-facing view of a node.
 *
 * `/v1/nodes` is shaped for the website and is what every deployed node and
 * page already reads, so it is left alone. This is a second projection of the
 * same rows under the names an autonomous renter would look for — provider id,
 * agent id, capabilities, pricing — not a second table and not a second source
 * of truth.
 *
 * Nothing here is stored. `capabilities` in particular is derived on every
 * read: a stored copy would be one more thing to keep in step with the node's
 * own heartbeat, and it would silently go stale the first time it drifted.
 */
import type { NodeListing } from "@cleargate/types";

export type ProviderView = {
  provider_id: string;
  /** The ERC-8004 agent id, or null if this provider never registered one. */
  agent_id: number | null;
  agent_address: string | null;
  identity_registry: string | null;
  /** The HCS topic carrying this provider's settlement history, if any. */
  audit_topic: string | null;

  gpu: {
    available: boolean;
    vendor: string | null;
    model: string | null;
    vram_gb: number | null;
  };

  /** Derived from what the node advertises, never stored separately. */
  capabilities: string[];

  pricing: {
    /** Flat price of one batch job. */
    per_job_tinybars: string;
    /**
     * Interactive time, per second. Null on a node that sells no interactive
     * time at all.
     */
    per_second_tinybars: string | null;
    /**
     * Whether unused interactive time is refunded. False means this node's
     * interactive time is forward-paid per slice and stopping early forfeits
     * the remainder — a real difference an agent should price in.
     */
    refundable: boolean;
    escrow_contract: string | null;
  };

  endpoint: {
    url: string;
    /** Where the provider's own agent card is served. */
    agent_card: string;
    network: string;
    pay_to: string;
  };

  status: "online" | "paused" | "offline";
  agent_version: string;
  first_seen_at: string;
  last_seen_at: string;
};

/** NVIDIA is the only vendor the node can pass through today. */
function gpuVendor(model: string | null | undefined): string | null {
  if (model === null || model === undefined || model === "") return null;
  return /nvidia|rtx|gtx|tesla|quadro|a100|h100|l4|t4/i.test(model) ? "NVIDIA" : null;
}

function capabilitiesOf(node: NodeListing): string[] {
  const capabilities: string[] = ["docker", "x402"];
  if (node.gpu.available) capabilities.push("cuda");
  if (node.leases !== undefined) {
    if (node.leases.ssh) capabilities.push("ssh");
    if (node.leases.jupyter) capabilities.push("jupyter");
    if (node.leases.escrow_contract !== undefined) capabilities.push("escrow");
  }
  if (node.audit_topic !== undefined) capabilities.push("hcs-audit");
  return capabilities;
}

/**
 * Three states, not two.
 *
 * "paused" is distinct from "offline" because they mean different things to a
 * renter: a paused node is up and deliberately not selling, so waiting is
 * reasonable; an offline one may never come back. Collapsing them would throw
 * away the difference.
 */
function statusOf(node: NodeListing): ProviderView["status"] {
  if (!node.online) return "offline";
  return node.paused ? "paused" : "online";
}

export function toProviderView(node: NodeListing): ProviderView {
  const perSecond = node.leases?.price_tinybars_per_second ?? null;
  const escrowContract = node.leases?.escrow_contract ?? null;
  const model = node.gpu.model ?? null;

  return {
    provider_id: node.node_id,
    agent_id: node.agent_id ?? null,
    agent_address: node.agent_address ?? null,
    identity_registry: node.identity_registry ?? null,
    audit_topic: node.audit_topic ?? null,

    gpu: {
      available: node.gpu.available,
      vendor: gpuVendor(model),
      model,
      // Advertised in GB because that is the unit a renter thinks in; the node
      // reports MB because that is what the driver reports.
      vram_gb:
        node.gpu.vram_mb === null || node.gpu.vram_mb === undefined
          ? null
          : Math.round(node.gpu.vram_mb / 1024),
    },

    capabilities: capabilitiesOf(node),

    pricing: {
      per_job_tinybars: node.price_tinybars,
      per_second_tinybars: perSecond,
      refundable: escrowContract !== null,
      escrow_contract: escrowContract,
    },

    endpoint: {
      url: node.public_url,
      agent_card: `${node.public_url.replace(/\/$/, "")}/.well-known/agent-card.json`,
      network: node.network,
      pay_to: node.pay_to,
    },

    status: statusOf(node),
    agent_version: node.agent_version,
    first_seen_at: node.first_seen_at,
    last_seen_at: node.last_seen_at,
  };
}
