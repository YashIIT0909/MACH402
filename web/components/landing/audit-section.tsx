"use client";

import { useEffect, useState } from "react";
import { FileText, Radio, SearchCheck, Server } from "lucide-react";

import { CONTAINER } from "./layout";
import { useReveal } from "./primitives";

/*
 * Where the testimonials would go on a site that had customers.
 *
 * A provider is being asked to run strangers' code on their own hardware for
 * money, so the useful thing to put here is not praise but the four places
 * they can check the money arrived without taking this website's word for it.
 */
const claims = [
  {
    icon: FileText,
    claim: "Every settlement is appended to a file on your own disk, the moment it happens.",
    source: "receipts.jsonl",
    detail: "on the provider's machine",
    result: "Earnings you can audit offline",
  },
  {
    icon: Radio,
    claim: "The same settlement is mirrored to a consensus topic we cannot go back and edit.",
    source: "HCS topic",
    detail: "ordered and timestamped",
    result: "A log nobody can rewrite",
  },
  {
    icon: SearchCheck,
    claim: "Every payment carries a real transaction id you can open in a block explorer.",
    source: "HashScan",
    detail: "0.0.x@seconds.nanos",
    result: "Independent confirmation",
  },
  {
    icon: Server,
    claim: "A node publishes its price, hardware and limits for free, before anyone is asked to pay.",
    source: "GET /v1/specs",
    detail: "unauthenticated, on every node",
    result: "No surprises at the 402",
  },
];

const surfaces = [
  "receipts.jsonl",
  "HCS topic",
  "HashScan",
  "Mirror Node REST",
  "GET /v1/specs",
  "PAYMENT-RESPONSE",
  "cleargate-node.log",
];

export function AuditSection() {
  const [activeIndex, setActiveIndex] = useState(0);
  const [isAnimating, setIsAnimating] = useState(false);
  const { ref, revealed } = useReveal();

  const goTo = (index: number) => {
    setIsAnimating(true);
    setTimeout(() => {
      setActiveIndex(index);
      setIsAnimating(false);
    }, 300);
  };

  useEffect(() => {
    if (!revealed) return;
    const interval = setInterval(() => {
      setIsAnimating(true);
      setTimeout(() => {
        setActiveIndex((prev) => (prev + 1) % claims.length);
        setIsAnimating(false);
      }, 300);
    }, 6000);
    return () => clearInterval(interval);
  }, [revealed]);

  const active = claims[activeIndex];
  const Icon = active.icon;

  return (
    <section
      id="audit"
      ref={ref}
      className="relative border-t border-foreground/10 py-32 lg:py-40 lg:pb-14"
    >
      <div className={CONTAINER}>
        <div className="mb-16 flex items-center gap-4">
          <span className="font-mono text-xs tracking-widest text-muted-foreground uppercase">
            Audit, not trust
          </span>
          <div className="h-px flex-1 bg-foreground/10" />
          <span className="font-mono text-xs text-muted-foreground">
            {String(activeIndex + 1).padStart(2, "0")} / {String(claims.length).padStart(2, "0")}
          </span>
        </div>

        <div className="grid gap-12 lg:grid-cols-12 lg:gap-20">
          <div className="lg:col-span-8">
            <blockquote
              className={`transition-all duration-300 ${
                isAnimating ? "translate-y-4 opacity-0" : "translate-y-0 opacity-100"
              }`}
            >
              <p className="font-display text-4xl leading-[1.1] tracking-tight md:text-5xl lg:text-6xl">
                {active.claim}
              </p>
            </blockquote>

            <div
              className={`mt-12 flex items-center gap-4 transition-all duration-300 ${
                isAnimating ? "opacity-0" : "opacity-100"
              }`}
            >
              <div className="flex h-16 w-16 shrink-0 items-center justify-center border border-foreground/10 bg-foreground/5">
                <Icon className="h-6 w-6 text-accent" />
              </div>
              <div>
                <p className="font-mono text-lg">{active.source}</p>
                <p className="text-muted-foreground">{active.detail}</p>
              </div>
            </div>
          </div>

          <div className="flex flex-col justify-center lg:col-span-4">
            <div
              className={`border border-foreground/10 p-8 transition-all duration-300 ${
                isAnimating ? "scale-95 opacity-0" : "scale-100 opacity-100"
              }`}
            >
              <span className="mb-4 block font-mono text-xs tracking-widest text-muted-foreground uppercase">
                What that buys you
              </span>
              <p className="font-display text-3xl md:text-4xl">{active.result}</p>
            </div>

            <div className="mt-8 flex gap-2">
              {claims.map((claim, index) => (
                <button
                  key={claim.source}
                  onClick={() => goTo(index)}
                  aria-label={`Show ${claim.source}`}
                  className={`h-2 transition-all duration-300 ${
                    index === activeIndex
                      ? "w-8 bg-accent"
                      : "w-2 bg-foreground/20 hover:bg-foreground/40"
                  }`}
                />
              ))}
            </div>
          </div>
        </div>

        <div className="mt-24 border-t border-foreground/10 pt-12">
          <p className="mb-8 text-center font-mono text-xs tracking-widest text-muted-foreground uppercase">
            Check it without trusting this website
          </p>
        </div>
      </div>

      <div className="w-full">
        <div className="marquee flex items-center gap-16">
          {[...Array(2)].map((_, setIndex) => (
            <div key={setIndex} className="flex shrink-0 items-center gap-16">
              {surfaces.map((surface) => (
                <span
                  key={`${setIndex}-${surface}`}
                  className="font-mono text-xl whitespace-nowrap text-foreground/30 transition-colors duration-300 hover:text-accent md:text-2xl"
                >
                  {surface}
                </span>
              ))}
            </div>
          ))}
        </div>
      </div>
    </section>
  );
}
