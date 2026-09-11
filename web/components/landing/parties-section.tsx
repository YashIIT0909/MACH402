"use client";

import { useEffect, useState } from "react";

import { CONTAINER } from "./layout";
import { Eyebrow, useReveal } from "./primitives";

/*
 * The four parties, in the order they appear in a job. The right-hand tag is
 * what each one is allowed to do with money — which is the whole point of the
 * section, and the reason the registry sits in the middle looking inert.
 */
const parties = [
  { name: "Renter", role: "Wants compute. Holds a Hedera key.", tag: "signs" },
  { name: "Provider node", role: "The x402 resource server. Runs the container.", tag: "receives" },
  { name: "Registry + web", role: "Discovery only. Records where nodes are.", tag: "no funds" },
  { name: "Facilitator", role: "Co-signs as fee payer, submits to Hedera.", tag: "submits" },
];

const stats = [
  { value: "4", label: "parties in a paid job" },
  { value: "1", label: "hop, renter to node" },
  { value: "0", label: "places funds are held" },
];

export function PartiesSection() {
  const { ref, revealed } = useReveal();
  const [activeParty, setActiveParty] = useState(0);

  useEffect(() => {
    if (!revealed) return;
    const interval = setInterval(() => {
      setActiveParty((prev) => (prev + 1) % parties.length);
    }, 2400);
    return () => clearInterval(interval);
  }, [revealed]);

  return (
    <section ref={ref} className="relative overflow-hidden py-24 lg:py-32">
      <div className={CONTAINER}>
        <div className="grid items-center gap-16 lg:grid-cols-2 lg:gap-24">
          <div
            className={`transition-all duration-700 ${
              revealed ? "translate-x-0 opacity-100" : "-translate-x-8 opacity-0"
            }`}
          >
            <Eyebrow className="mb-6">Architecture</Eyebrow>
            <h2 className="mb-8 font-display text-4xl tracking-tight lg:text-6xl">
              Four parties.
              <br />
              <span className="text-muted-foreground">One payment.</span>
            </h2>
            <p className="mb-12 text-xl leading-relaxed text-muted-foreground">
              Money moves once, from the renter&apos;s account to the provider&apos;s, and nothing
              in between ever holds it. The registry is discovery and nothing else — if it vanished
              mid-job, the job would finish and the provider would still be paid.
            </p>

            <div className="grid grid-cols-3 gap-8">
              {stats.map((stat) => (
                <div key={stat.label}>
                  <div className="mb-2 font-display text-4xl lg:text-5xl">{stat.value}</div>
                  <div className="text-sm text-muted-foreground">{stat.label}</div>
                </div>
              ))}
            </div>
          </div>

          <div
            className={`transition-all delay-200 duration-700 ${
              revealed ? "translate-x-0 opacity-100" : "translate-x-8 opacity-0"
            }`}
          >
            <div className="border border-foreground/10">
              <div className="flex items-center justify-between border-b border-foreground/10 px-6 py-4">
                <span className="font-mono text-sm text-muted-foreground">One paid job</span>
                <span className="flex items-center gap-2 font-mono text-xs text-accent">
                  <span className="h-2 w-2 rounded-full bg-accent" />
                  renter → node
                </span>
              </div>

              <div>
                {parties.map((party, index) => (
                  <div
                    key={party.name}
                    className={`flex items-center justify-between border-b border-foreground/5 px-6 py-5 transition-all duration-300 last:border-b-0 ${
                      activeParty === index ? "bg-foreground/[0.03]" : ""
                    }`}
                  >
                    <div className="flex items-center gap-4">
                      <span
                        className={`h-2 w-2 shrink-0 rounded-full transition-colors duration-300 ${
                          activeParty === index ? "bg-accent" : "bg-foreground/20"
                        }`}
                      />
                      <div>
                        <div className="font-medium">{party.name}</div>
                        <div className="text-sm text-muted-foreground">{party.role}</div>
                      </div>
                    </div>
                    <span className="ml-4 shrink-0 font-mono text-sm text-muted-foreground">
                      {party.tag}
                    </span>
                  </div>
                ))}
              </div>
            </div>
          </div>
        </div>
      </div>
    </section>
  );
}
