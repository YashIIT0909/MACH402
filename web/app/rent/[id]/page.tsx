import Link from "next/link";
import type { NodeListing } from "@cleargate/types";

import { fetchNode, hbar } from "@/lib/registry";
import { CONTAINER } from "@/components/landing/layout";
import { PageHero } from "@/components/landing/page-hero";
import { WalletProvider } from "@/components/wallet/wallet-provider";
import { RentFlow } from "@/components/rent/rent-flow";

// A listing is stale the moment a node stops beating, and this page is where
// someone is about to spend money on one.
export const dynamic = "force-dynamic";

export default async function RentPage({ params }: { params: Promise<{ id: string }> }) {
  const { id } = await params;
  const result = await fetchNode(id);

  if (!result.ok) {
    return (
      <>
        <PageHero eyebrow="Not found" title="No such node">
          {result.error}
        </PageHero>
        <section className="py-16">
          <div className={CONTAINER}>
            <Link href="/nodes" className="underline underline-offset-4 hover:text-accent">
              Back to all nodes
            </Link>
          </div>
        </section>
      </>
    );
  }

  const node = result.node;

  return (
    <>
      <PageHero
        eyebrow={node.online ? "Online" : "Offline"}
        title={node.gpu.model ?? (node.gpu.available ? "GPU node" : "CPU node")}
      >
        You pay this node directly — the registry only records where it is. Your code and data never
        leave your machine: a lease gives you a container on the provider&apos;s hardware and you
        connect to it.
      </PageHero>

      <section className="py-16 lg:py-24">
        <div className={CONTAINER}>
          <div className="grid gap-12 lg:grid-cols-[1fr_420px] lg:gap-16">
            <Specs node={node} />
            <WalletProvider>
              <RentFlow node={node} />
            </WalletProvider>
          </div>
        </div>
      </section>
    </>
  );
}

function Specs({ node }: { node: NodeListing }) {
  const offer = node.leases;

  return (
    <div>
      <dl className="divide-y divide-foreground/10 border-y border-foreground/10">
        <Row label="Node">
          <span className="font-mono break-all">{node.node_id}</span>
          <Sub>agent {node.agent_version}</Sub>
        </Row>

        <Row label="GPU">
          {node.gpu.available ? (
            <>
              <span>{node.gpu.model ?? "available"}</span>
              {node.gpu.vram_mb !== null ? (
                <Sub>{Math.round(node.gpu.vram_mb / 1024)} GB VRAM on the host</Sub>
              ) : null}
            </>
          ) : (
            <>
              <span className="text-muted-foreground">CPU only</span>
              {node.gpu.reason !== undefined ? <Sub>{node.gpu.reason}</Sub> : null}
            </>
          )}
          {offer !== undefined && node.gpu.available && !offer.gpu ? (
            <Sub>
              The host has a card but this node&apos;s lease image carries no CUDA runtime, so a
              lease container here cannot compute on it. GPU leases are refused before payment.
            </Sub>
          ) : null}
        </Row>

        {offer !== undefined ? (
          <>
            <Row label="Rate">
              <span className="font-mono">{hbar(offer.price_tinybars_per_minute)} HBAR / min</span>
              <Sub>
                {offer.min_minutes}–{offer.max_minutes} minutes per slice, {offer.max_total_minutes}{" "}
                total
              </Sub>
            </Row>

            <Row label="Container">
              <span>
                {offer.cpu_cores} cores · {Math.round(offer.memory_mb / 1024)} GB RAM ·{" "}
                {offer.workspace_gb} GB workspace
              </span>
            </Row>

            <Row label="Access">
              <span>
                {offer.jupyter ? "Jupyter" : null}
                {offer.jupyter && offer.ssh ? " · " : null}
                {offer.ssh ? "SSH" : null}
              </span>
              <Sub>
                {offer.ssh
                  ? "A browser uses Jupyter; SSH needs cloudflared on your machine."
                  : "This node's tunnel carries HTTP only, so there is no SSH — Jupyter includes a terminal."}
              </Sub>
            </Row>

            <Row label="Egress">
              <Sub flush>
                The container has no route off the host except a deny-by-default proxy allowing{" "}
                {offer.egress_allowlist.length} host
                {offer.egress_allowlist.length === 1 ? "" : "s"}. A lease that cannot reach the index
                you need is not the lease you wanted, so it is published rather than discovered.
              </Sub>
              {offer.egress_allowlist.length > 0 ? (
                <p className="mt-2 font-mono text-xs break-all text-muted-foreground">
                  {offer.egress_allowlist.join(" · ")}
                </p>
              ) : null}
            </Row>
          </>
        ) : null}

        <Row label="Paid to">
          <span className="font-mono break-all">{node.pay_to}</span>
          <Sub>Renter to node, directly. The registry never touches funds.</Sub>
        </Row>
      </dl>

      <p className="mt-8 text-sm text-muted-foreground">
        <Link href="/nodes" className="underline underline-offset-4 hover:text-accent">
          All nodes
        </Link>
      </p>
    </div>
  );
}

function Row({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="grid gap-1 py-5 sm:grid-cols-[140px_1fr] sm:gap-6">
      <dt className="type-label text-muted-foreground">{label}</dt>
      <dd className="min-w-0">{children}</dd>
    </div>
  );
}

function Sub({ children, flush }: { children: React.ReactNode; flush?: boolean }) {
  return (
    <span className={`block text-sm text-muted-foreground ${flush === true ? "" : "mt-1"}`}>
      {children}
    </span>
  );
}
