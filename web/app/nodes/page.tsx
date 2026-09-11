import Link from "next/link";
import type { NodeListing } from "@cleargate/types";

import { fetchNodes, hbar, REGISTRY_URL } from "@/lib/registry";
import { PageHero } from "@/components/landing/page-hero";
import { CONTAINER } from "@/components/landing/layout";
import { Eyebrow } from "@/components/landing/primitives";

// Listings go stale the moment a node stops beating, so never cache this page.
export const dynamic = "force-dynamic";

export default async function NodesPage() {
  const result = await fetchNodes();
  const online = result.ok ? result.nodes.filter((node) => node.online).length : 0;

  return (
    <>
      <PageHero
        eyebrow={
          result.ok ? `${result.nodes.length} listed · ${online} online` : "Registry unreachable"
        }
        title="Nodes"
      >
        Everything a node has announced about itself. Payment goes to the account in the last
        column, directly — the registry records where nodes are and nothing else.
      </PageHero>

      <section className="py-16 lg:py-24">
        <div className={CONTAINER}>
          {!result.ok ? (
            <Notice title="Could not reach the registry">
              <p className="mb-4 font-mono text-sm break-all text-destructive">{result.error}</p>
              <p className="text-muted-foreground">
                The registry is expected at <Code>{REGISTRY_URL}</Code>. Start it with{" "}
                <Code>make dev-registry</Code>, or run <Code>make dev-registry-sample</Code> for
                fixture data. Nodes keep selling jobs either way: a registry outage never
                interrupts a paid job.
              </p>
            </Notice>
          ) : result.nodes.length === 0 ? (
            <Notice title="Nothing listed yet">
              <p className="text-muted-foreground">
                No node has ever checked in.{" "}
                <Link
                  href="/provide"
                  className="text-foreground underline-offset-4 hover:text-accent hover:underline"
                >
                  List your GPU
                </Link>{" "}
                to be the first — a node appears here on its first heartbeat, seconds after it
                starts.
              </p>
            </Notice>
          ) : (
            <>
              {/*
               * Two renderings of the same rows. Node ids, tinybar prices and
               * payout accounts are values a renter compares before spending
               * money, and they compare far better column-aligned — but six
               * columns below phone width is a sideways scroll, so the same
               * data stacks into cards there.
               */}
              <div className="hidden md:block">
                <NodeTable nodes={result.nodes} />
              </div>
              <div className="grid gap-px bg-foreground/10 md:hidden">
                {result.nodes.map((node) => (
                  <NodeCard key={node.node_id} node={node} />
                ))}
              </div>
            </>
          )}
        </div>
      </section>

      {result.ok && result.nodes.length > 0 ? (
        <section className="border-t border-foreground/10 py-16 lg:py-24">
          <div className={CONTAINER}>
            <Eyebrow className="mb-6">Renting one</Eyebrow>
            <h2 className="mb-6 type-title">
              Quote first.
              <br />
              <span className="text-muted-foreground">It costs nothing.</span>
            </h2>
            <p className="mb-10 max-w-2xl type-lede text-muted-foreground">
              <Code>/v1/specs</Code> is free on every node, because discovery that costs money is
              discovery agents cannot do. Only <Code>cleargate run</Code> pays.
            </p>

            <div className="max-w-3xl border border-foreground/10">
              <div className="flex items-center justify-between border-b border-foreground/10 px-6 py-4">
                <span className="type-label text-muted-foreground">
                  terminal
                </span>
                <span className="flex items-center gap-2 font-mono text-xs text-accent">
                  <span className="h-2 w-2 rounded-full bg-accent" />
                  free until you run
                </span>
              </div>
              <pre className="overflow-x-auto bg-foreground/[0.02] p-6 font-mono text-sm leading-relaxed text-foreground/80">{`cleargate quote --node <public url>

cleargate run \\
  --node <public url> \\
  --image python:3.11-slim \\
  --script examples/train.py \\
  --output result.tar`}</pre>
            </div>
          </div>
        </section>
      ) : null}
    </>
  );
}

function NodeTable({ nodes }: { nodes: NodeListing[] }) {
  return (
    <div className="overflow-x-auto border-t border-foreground/40">
      <table className="w-full border-collapse text-sm">
        <thead>
          <tr>
            {["Node", "Status", "GPU", "Price", "Limits", "Paid to"].map((heading) => (
              <th
                key={heading}
                className="type-label py-4 pr-5 text-left font-medium whitespace-nowrap text-muted-foreground last:pr-0"
              >
                {heading}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {nodes.map((node) => (
            <tr
              key={node.node_id}
              className="border-t border-foreground/10 transition-colors ease-brand dur-base hover:bg-accent/[0.04]"
            >
              <td className="py-5 pr-5 align-top">
                <span className="font-mono">{node.node_id}</span>
                <Sub mono>{node.public_url}</Sub>
                <Sub>agent {node.agent_version}</Sub>
              </td>
              <td className="py-5 pr-5 align-top">
                <Status node={node} />
                <Sub>seen {new Date(node.last_seen_at).toLocaleTimeString()}</Sub>
              </td>
              <td className="py-5 pr-5 align-top">
                <Gpu node={node} />
              </td>
              <td className="py-5 pr-5 align-top">
                <span className="font-mono whitespace-nowrap">{hbar(node.price_tinybars)} HBAR</span>
                <Sub mono>{node.price_tinybars} tinybars · per job</Sub>
                {/* Two products on one node. A node that never opted into
                    leasing announces no lease block at all, so this cell reads
                    exactly as it did before leasing existed. */}
                <Lease node={node} />
              </td>
              <td className="py-5 pr-5 align-top">
                <span className="whitespace-nowrap">
                  {node.limits.cpu_cores} cores · {Math.round(node.limits.memory_mb / 1024)} GB
                </span>
                <Sub mono>{node.limits.max_seconds}s max</Sub>
              </td>
              <td className="py-5 align-top">
                <span className="font-mono">{node.pay_to}</span>
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

function NodeCard({ node }: { node: NodeListing }) {
  return (
    <div className="bg-background p-6">
      <div className="mb-4 flex items-start justify-between gap-4">
        <div className="min-w-0">
          <div className="font-mono break-all">{node.node_id}</div>
          <Sub mono>{node.public_url}</Sub>
        </div>
        <Status node={node} />
      </div>

      <div className="mb-5 border-b border-foreground/10 pb-5">
        <div className="type-stat">{hbar(node.price_tinybars)} HBAR</div>
        <Sub mono>{node.price_tinybars} tinybars per job</Sub>
        <Lease node={node} />
      </div>

      <dl className="space-y-3">
        <Row label="GPU">
          <Gpu node={node} />
        </Row>
        <Row label="Limits">
          <Sub flush>
            {node.limits.cpu_cores} cores · {Math.round(node.limits.memory_mb / 1024)} GB ·{" "}
            {node.limits.max_seconds}s max
          </Sub>
        </Row>
        <Row label="Paid to">
          <span className="font-mono text-sm">{node.pay_to}</span>
        </Row>
        <Row label="Seen">
          <Sub flush>
            {new Date(node.last_seen_at).toLocaleTimeString()} · agent {node.agent_version}
          </Sub>
        </Row>
      </dl>
    </div>
  );
}

function Row({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="flex items-start justify-between gap-4">
      <dt className="shrink-0 type-label text-muted-foreground">
        {label}
      </dt>
      <dd className="min-w-0 text-right">{children}</dd>
    </div>
  );
}

/**
 * The metered half of what a node sells, when it sells it.
 *
 * Rendered as a second line under the flat job price rather than its own
 * column: most nodes offer only jobs, and a column that is empty on most rows
 * costs every reader width to tell a minority of them something.
 */
function Lease({ node }: { node: NodeListing }) {
  if (node.leases === undefined) return null;

  const offer = node.leases;
  const reach = offer.ssh && offer.jupyter ? "ssh + jupyter" : offer.ssh ? "ssh" : "jupyter only";

  return (
    <span className="mt-3 block border-t border-foreground/10 pt-3">
      <span className="font-mono whitespace-nowrap text-accent">
        {hbar(offer.price_tinybars_per_minute)} HBAR
      </span>
      <Sub mono>
        per minute · {reach}
        {offer.gpu ? "" : " · cpu only"}
      </Sub>
    </span>
  );
}

function Gpu({ node }: { node: NodeListing }) {
  if (node.gpu.available) {
    return (
      <>
        {node.gpu.model ?? "GPU"}
        {node.gpu.vram_mb === null || node.gpu.vram_mb === undefined ? null : (
          <Sub mono>{Math.round(node.gpu.vram_mb / 1024)} GB VRAM</Sub>
        )}
      </>
    );
  }

  return (
    <>
      <Sub flush>CPU-fallback mode</Sub>
      {/* The reason is the honest part: a card that cannot be passed through
          is advertised as no card at all. */}
      {node.gpu.reason === undefined ? null : <Sub>{node.gpu.reason}</Sub>}
    </>
  );
}

function Status({ node }: { node: NodeListing }) {
  const base =
    "type-label inline-flex items-center gap-2 whitespace-nowrap";

  if (!node.online) {
    return (
      <span className={`${base} text-muted-foreground`}>
        <span className="h-1.5 w-1.5 rounded-full border border-current" />
        offline
      </span>
    );
  }

  if (node.paused) {
    return (
      <span className={`${base} text-warn`}>
        <span className="h-1.5 w-1.5 rounded-full bg-current" />
        paused
      </span>
    );
  }

  return (
    <span className={`${base} text-accent`}>
      <span className="h-1.5 w-1.5 rounded-full bg-current" />
      available
    </span>
  );
}

function Sub({
  children,
  mono = false,
  flush = false,
}: {
  children: React.ReactNode;
  mono?: boolean;
  flush?: boolean;
}) {
  return (
    <span
      className={`block text-xs text-muted-foreground ${flush ? "" : "mt-1"} ${
        mono ? "font-mono break-all" : ""
      }`}
    >
      {children}
    </span>
  );
}

function Notice({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <div className="border border-foreground/10 bg-foreground/[0.02] p-8 lg:p-10">
      <span className="mb-4 block type-label text-accent">
        {title}
      </span>
      {children}
    </div>
  );
}

function Code({ children }: { children: React.ReactNode }) {
  return <code className="font-mono text-[0.9em] text-foreground">{children}</code>;
}
