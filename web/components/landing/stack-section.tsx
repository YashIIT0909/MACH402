"use client";

import { CONTAINER } from "./layout";
import { Eyebrow, useReveal } from "./primitives";

/*
 * The actual stack. Half of it is not ours and deliberately so — the
 * facilitator, the ledger and the explorer are things a provider can check
 * without taking our word for anything.
 */
const stack = [
  { name: "Hedera testnet", category: "Settlement network" },
  { name: "x402 v2", category: "Payment protocol" },
  { name: "Blocky402", category: "Facilitator, not ours" },
  { name: "@x402/hedera", category: "Client-side signing" },
  { name: "Docker", category: "Sandbox runtime" },
  { name: "NVIDIA Container Toolkit", category: "GPU passthrough" },
  { name: "HCS", category: "Receipt audit log" },
  { name: "HashScan", category: "Transaction explorer" },
  { name: "Go 1.25", category: "Node daemon" },
  { name: "Fastify + Postgres", category: "Discovery registry" },
  { name: "Next.js", category: "This website" },
  { name: "systemd", category: "Provider service" },
];

function StackCard({ item }: { item: (typeof stack)[number] }) {
  return (
    <div className="group shrink-0 border border-foreground/10 px-8 py-6 transition-all duration-300 hover:border-accent/40 hover:bg-foreground/[0.03]">
      <div className="text-lg font-medium transition-transform group-hover:translate-x-1">
        {item.name}
      </div>
      <div className="font-mono text-sm text-muted-foreground">{item.category}</div>
    </div>
  );
}

export function StackSection() {
  const { ref, revealed } = useReveal();

  return (
    <section id="stack" ref={ref} className="relative overflow-hidden py-24 lg:py-32">
      <div className={CONTAINER}>
        <div
          className={`mx-auto mb-16 max-w-3xl text-center transition-all duration-700 lg:mb-24 ${
            revealed ? "translate-y-0 opacity-100" : "translate-y-8 opacity-0"
          }`}
        >
          <Eyebrow className="mb-6" centered>
            The stack
          </Eyebrow>
          <h2 className="mb-6 font-display text-4xl tracking-tight lg:text-6xl">
            Built on things that
            <br />
            already work.
          </h2>
          <p className="text-xl text-muted-foreground">
            The facilitator is not ours and neither is the ledger. Nothing about settlement is
            reimplemented here, which is exactly why you can check it somewhere else.
          </p>
        </div>
      </div>

      {/* Full-bleed, so the rows read as continuous rather than as a list. */}
      <div className="mb-6 w-full">
        <div className="marquee flex gap-6">
          {[...Array(2)].map((_, setIndex) => (
            <div key={setIndex} className="flex shrink-0 gap-6">
              {stack.map((item) => (
                <StackCard key={`${item.name}-${setIndex}`} item={item} />
              ))}
            </div>
          ))}
        </div>
      </div>

      <div className="w-full">
        <div className="marquee-reverse flex gap-6">
          {[...Array(2)].map((_, setIndex) => (
            <div key={setIndex} className="flex shrink-0 gap-6">
              {[...stack].reverse().map((item) => (
                <StackCard key={`${item.name}-reverse-${setIndex}`} item={item} />
              ))}
            </div>
          ))}
        </div>
      </div>
    </section>
  );
}
