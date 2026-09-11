"use client";

import { useState } from "react";
import Link from "next/link";
import { ArrowRight, Check, Copy } from "lucide-react";

import type { NodeListing } from "@cleargate/types";
import { hbar } from "@/lib/registry";
import { CONTAINER } from "./layout";
import { useReveal } from "./primitives";

/**
 * The pricing section reads the registry rather than a table of plans.
 *
 * There are no tiers to invent: a node's price is a flat per-job figure its
 * own operator chose, and the marketplace takes no cut in code. So the three
 * cheapest nodes that are actually online stand in for the pricing table.
 */
export function PricingSection({ nodes }: { nodes: NodeListing[] }) {
  const [inHbar, setInHbar] = useState(true);
  const [copied, setCopied] = useState<string | null>(null);
  const { ref, revealed } = useReveal();

  async function copyRunCommand(node: NodeListing) {
    const image = node.image_allowlist[0] ?? "python:3.11-slim";
    const command = [
      "cleargate run \\",
      `  --node ${node.public_url} \\`,
      `  --image ${image} \\`,
      "  --script examples/train.py \\",
      `  --budget ${node.price_tinybars} \\`,
      "  --output result.tar",
    ].join("\n");

    await navigator.clipboard.writeText(command);
    setCopied(node.node_id);
    setTimeout(() => setCopied(null), 2000);
  }

  return (
    <section id="pricing" ref={ref} className="relative border-t border-foreground/10 py-32 lg:py-40">
      <div className={CONTAINER}>
        <div className="mb-20 max-w-3xl">
          <span className="mb-6 block font-mono text-xs tracking-widest text-muted-foreground uppercase">
            Pricing
          </span>
          <h2
            className={`mb-6 font-display text-5xl tracking-tight transition-all duration-700 md:text-6xl lg:text-7xl ${
              revealed ? "translate-y-0 opacity-100" : "translate-y-4 opacity-0"
            }`}
          >
            Priced by the
            <br />
            <span className="text-stroke">provider</span>
          </h2>
          <p className="max-w-xl text-lg text-muted-foreground">
            There are no plans to choose between. Each provider sets a flat price per job, publishes
            it for free, and is paid it directly. What you see below is what is online right now.
          </p>
        </div>

        {nodes.length === 0 ? (
          <EmptyState />
        ) : (
          <>
            <div className="mb-16 flex items-center gap-4">
              <span
                className={`text-sm transition-colors ${
                  inHbar ? "text-foreground" : "text-muted-foreground"
                }`}
              >
                HBAR
              </span>
              <button
                onClick={() => setInHbar(!inHbar)}
                className="relative h-7 w-14 rounded-full bg-foreground/10 p-1 transition-colors hover:bg-foreground/20"
                aria-label="Switch between HBAR and tinybars"
              >
                <div
                  className={`h-5 w-5 rounded-full bg-foreground transition-transform duration-300 ${
                    inHbar ? "translate-x-0" : "translate-x-7"
                  }`}
                />
              </button>
              <span
                className={`text-sm transition-colors ${
                  inHbar ? "text-muted-foreground" : "text-foreground"
                }`}
              >
                Tinybars
              </span>
              {/* The unit that actually crosses the wire. */}
              <span className="ml-2 bg-foreground/10 px-2 py-1 font-mono text-xs text-muted-foreground">
                1 HBAR = 100,000,000 tinybars
              </span>
            </div>

            <div className="grid gap-px bg-foreground/10 md:grid-cols-3">
              {nodes.map((node, index) => (
                <div
                  key={node.node_id}
                  className={`relative bg-background p-8 lg:p-12 ${
                    index === 0 ? "border-2 border-accent md:-my-4 md:py-12 lg:py-16" : ""
                  }`}
                >
                  {index === 0 ? (
                    <span className="absolute -top-3 left-8 bg-accent px-3 py-1 font-mono text-xs tracking-widest text-accent-foreground uppercase">
                      Cheapest online
                    </span>
                  ) : null}

                  <div className="mb-8">
                    <span className="font-mono text-xs text-muted-foreground">
                      {String(index + 1).padStart(2, "0")}
                    </span>
                    <h3 className="mt-2 font-mono text-xl break-all">{node.node_id}</h3>
                    <p className="mt-2 font-mono text-sm break-all text-muted-foreground">
                      {node.public_url}
                    </p>
                  </div>

                  <div className="mb-8 border-b border-foreground/10 pb-8">
                    <div className="flex items-baseline gap-2">
                      <span className="font-display text-5xl break-all lg:text-6xl">
                        {inHbar ? hbar(node.price_tinybars) : node.price_tinybars}
                      </span>
                      <span className="text-muted-foreground">{inHbar ? "HBAR" : "tℏ"}</span>
                    </div>
                    <span className="mt-2 block text-sm text-muted-foreground">per job, flat</span>
                  </div>

                  <ul className="mb-10 space-y-4">
                    <Spec>
                      {node.gpu.available
                        ? `${node.gpu.model ?? "GPU"}${
                            node.gpu.vram_mb ? ` · ${Math.round(node.gpu.vram_mb / 1024)} GB VRAM` : ""
                          }`
                        : "CPU-fallback mode — no usable GPU"}
                    </Spec>
                    <Spec>
                      {node.limits.cpu_cores} cores · {Math.round(node.limits.memory_mb / 1024)} GB
                      memory
                    </Spec>
                    <Spec>{node.limits.max_seconds}s wall-clock limit</Spec>
                    <Spec>
                      {node.image_allowlist.length} allowlisted image
                      {node.image_allowlist.length === 1 ? "" : "s"}
                    </Spec>
                    <Spec>
                      Paid to <span className="font-mono text-foreground">{node.pay_to}</span>
                    </Spec>
                  </ul>

                  <button
                    onClick={() => void copyRunCommand(node)}
                    className={`group flex w-full items-center justify-center gap-2 py-4 text-sm font-medium transition-all ${
                      index === 0
                        ? "bg-primary text-primary-foreground hover:bg-primary/90"
                        : "border border-foreground/20 hover:border-foreground hover:bg-foreground/5"
                    }`}
                  >
                    {copied === node.node_id ? (
                      <>
                        Copied <Check className="h-4 w-4" />
                      </>
                    ) : (
                      <>
                        Copy run command <Copy className="h-4 w-4" />
                      </>
                    )}
                  </button>
                </div>
              ))}
            </div>
          </>
        )}

        <p className="mt-12 text-center text-sm text-muted-foreground">
          The registry never receives, holds or forwards funds — payment is renter to node, direct.{" "}
          <Link href="/nodes" className="text-foreground underline-offset-4 hover:text-accent hover:underline">
            See every listed node
          </Link>
        </p>
      </div>
    </section>
  );
}

function Spec({ children }: { children: React.ReactNode }) {
  return (
    <li className="flex items-start gap-3">
      <Check className="mt-0.5 h-4 w-4 shrink-0 text-accent" />
      <span className="text-sm text-muted-foreground">{children}</span>
    </li>
  );
}

function EmptyState() {
  return (
    <div className="border border-foreground/10 p-8 lg:p-12">
      <span className="mb-4 block font-mono text-xs tracking-widest text-accent uppercase">
        Nothing online right now
      </span>
      <p className="mb-6 max-w-2xl text-lg text-muted-foreground">
        Prices come from nodes that are currently beating to the registry, and none are. That does
        not mean nothing is for sale — listing is opt-in, and a renter who knows a node&apos;s URL
        can still quote it and pay it normally.
      </p>
      <Link
        href="/provide"
        className="group inline-flex items-center gap-2 text-sm font-medium text-foreground"
      >
        List your GPU and set the first price
        <ArrowRight className="h-4 w-4 transition-transform group-hover:translate-x-1" />
      </Link>
    </div>
  );
}
