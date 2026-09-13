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
                fixture data. Nodes keep selling either way: a registry outage never
                interrupts a paid session.
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
              discovery agents cannot do. Asking for a session is free too: the node answers{" "}
              <Code>402</Code> with its price, and only a signed payment moves money.
            </p>

            <div className="max-w-3xl border border-foreground/10">
              <div className="flex items-center justify-between border-b border-foreground/10 px-6 py-4">
                <span className="type-label text-muted-foreground">
                  terminal
                </span>
                <span className="flex items-center gap-2 font-mono text-xs text-accent">
                  <span className="h-2 w-2 rounded-full bg-accent" />
                  free until you pay
                </span>
              </div>
              <pre className="overflow-x-auto bg-foreground/[0.02] p-6 font-mono text-sm leading-relaxed text-foreground/80">{`curl -s <public url>/v1/specs

curl -si -X POST <public url>/v1/sessions \\
  -H 'Content-Type: application/json' \\
  -d '{"seconds":300,"public_key":"ssh-ed25519 AAAA…"}'`}</pre>
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
            {["Node", "Status", "GPU", "Price", "Container", "Paid to"].map((heading) => (
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
                <Sub mono nowrap>{node.public_url}</Sub>
                <Sub>agent {node.agent_version}</Sub>
              </td>
              <td className="py-5 pr-5 align-top">
                <Status node={node} />
                <Sub nowrap>seen {new Date(node.last_seen_at).toLocaleTimeString()}</Sub>
              </td>
              <td className="py-5 pr-5 align-top">
                <Gpu node={node} />
              </td>
              <td className="py-5 pr-5 align-top">
                <Price node={node} />
              </td>
              <td className="py-5 pr-5 align-top">
                <Container node={node} />
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
        <Price node={node} stat />
      </div>

      <dl className="space-y-3">
        <Row label="GPU">
          <Gpu node={node} />
        </Row>
        <Row label="Container">
          <Sub flush>
            {node.leases === undefined
              ? "—"
              : `${node.leases.cpu_cores} cores · ${Math.round(node.leases.memory_mb / 1024)} GB · ${node.leases.workspace_gb} GB workspace`}
          </Sub>
        </Row>
        <Row label="Paid to">
          <span className="font-mono text-sm">{node.pay_to}</span>
        </Row>
        <Identity node={node} />
        <Row label="Seen">
          <Sub flush>
            {new Date(node.last_seen_at).toLocaleTimeString()} · agent {node.agent_version}
          </Sub>
        </Row>
      </dl>
    </div>
  );
}

/**
 * This provider's on-chain identity, if they registered one.
 *
 * Rendered as a link to the registry contract rather than as a bare number,
 * because the number on its own is not checkable — the point of the id is that
 * a visitor can go and verify it somewhere that is not this website.
 *
 * Absent for an unregistered node, which is normal: registration costs a
 * transaction and is opt-in, so showing "not registered" would read as a defect
 * rather than as a choice.
 */
function Identity({ node }: { node: NodeListing }) {
  if (node.agent_id === undefined || node.agent_id === 0) return null;

  const registry = node.identity_registry;
  const label = `Agent ${node.agent_id}`;

  return (
    <Row label="Identity">
      {registry === undefined ? (
        <span className="font-mono text-sm">{label}</span>
      ) : (
        <a
          className="font-mono text-sm underline underline-offset-4 hover:no-underline"
          href={`https://hashscan.io/testnet/contract/${registry}`}
          target="_blank"
          rel="noreferrer"
        >
          {label} ↗
        </a>
      )}
      <Sub flush>ERC-8004, verifiable without trusting this site</Sub>
    </Row>
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
 * What a node charges for a session, and the way into renting one.
 *
 * A node running a build from before metered sessions sells prepaid minutes,
 * which this site no longer buys: it is listed, so it does not silently vanish,
 * but not offered. A node with leasing switched off sells nothing at all.
 */
function Price({ node, stat = false }: { node: NodeListing; stat?: boolean }) {
  const offer = node.leases;
  if (offer === undefined) {
    return <Sub flush>not selling</Sub>;
  }

  const metered = offer.payment_mode === "session";

  return (
    <>
      <span className={stat ? "block type-stat" : "font-mono whitespace-nowrap"}>
        {hbar(offer.price_tinybars_per_minute)} HBAR
      </span>
      <Sub mono>
        {metered ? "per minute · billed by the second · refundable" : "older build · not rentable"}
        {offer.gpu ? "" : " · cpu only"}
      </Sub>
      {/* Only an online node can actually be rented. Offering the link on an
          offline one would send a renter to a page whose only content is an
          explanation of why they cannot buy anything. */}
      {node.online && metered ? (
        <Link
          href={`/rent/${node.node_id}`}
          className="mt-2 inline-block text-sm text-accent underline-offset-4 hover:underline"
        >
          Rent by the second →
        </Link>
      ) : null}
    </>
  );
}

/** The container a session gets — the part of the machine a renter is buying. */
function Container({ node }: { node: NodeListing }) {
  const offer = node.leases;
  if (offer === undefined) {
    return <Sub flush>—</Sub>;
  }

  return (
    <>
      <span className="whitespace-nowrap">
        {offer.cpu_cores} cores · {Math.round(offer.memory_mb / 1024)} GB
      </span>
      <Sub mono>{offer.workspace_gb} GB workspace</Sub>
    </>
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
  nowrap = false,
}: {
  children: React.ReactNode;
  mono?: boolean;
  flush?: boolean;
  /**
   * Keep to one line. For table cells, where the table scrolls sideways
   * rather than breaking a URL mid-hostname ("example.n / et") or a time in
   * two ("12:48:26 / PM"). Card layouts leave it off and wrap as before.
   */
  nowrap?: boolean;
}) {
  return (
    <span
      className={`block text-xs text-muted-foreground ${flush ? "" : "mt-1"} ${
        mono ? (nowrap ? "font-mono" : "font-mono break-all") : ""
      } ${nowrap ? "whitespace-nowrap" : ""}`}
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
