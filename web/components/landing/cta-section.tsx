"use client";

import { useState } from "react";
import Link from "next/link";
import { ArrowRight } from "lucide-react";

import { Button } from "@/components/ui/button";
import { AnimatedTetrahedron } from "./ascii-canvas";
import { CONTAINER } from "./layout";
import { useReveal } from "./primitives";

export function CtaSection() {
  const { ref, revealed } = useReveal(0.2);
  const [mouse, setMouse] = useState({ x: 50, y: 50 });

  const handleMouseMove = (event: React.MouseEvent<HTMLDivElement>) => {
    const rect = event.currentTarget.getBoundingClientRect();
    setMouse({
      x: ((event.clientX - rect.left) / rect.width) * 100,
      y: ((event.clientY - rect.top) / rect.height) * 100,
    });
  };

  return (
    <section ref={ref} className="relative overflow-hidden py-24 lg:py-32">
      <div className={CONTAINER}>
        <div
          className={`relative border border-foreground/40 transition-all duration-1000 ${
            revealed ? "translate-y-0 opacity-100" : "translate-y-8 opacity-0"
          }`}
          onMouseMove={handleMouseMove}
        >
          <div
            className="pointer-events-none absolute inset-0 opacity-40 transition-opacity duration-300"
            style={{
              background: `radial-gradient(600px circle at ${mouse.x}% ${mouse.y}%, rgba(0, 232, 122, 0.06), transparent 40%)`,
            }}
          />

          <div className="relative z-10 px-8 py-16 lg:px-16 lg:py-24">
            <div className="flex flex-col items-center justify-between gap-12 lg:flex-row">
              <div className="flex-1">
                <h2 className="mb-8 font-display text-4xl leading-[0.95] tracking-tight lg:text-7xl">
                  Someone&apos;s GPU
                  <br />
                  is idle right now.
                </h2>

                <p className="mb-12 max-w-xl text-xl leading-relaxed text-muted-foreground">
                  Rent one for the length of a job, or put yours to work between your own runs.
                  Either side takes a few minutes and neither side signs up for anything.
                </p>

                <div className="flex flex-col items-start gap-4 sm:flex-row">
                  <Button asChild size="lg" className="group h-14 rounded-full px-8 text-base">
                    <Link href="/nodes">
                      Browse nodes
                      <ArrowRight className="ml-2 h-4 w-4 transition-transform group-hover:translate-x-1" />
                    </Link>
                  </Button>
                  <Button
                    asChild
                    size="lg"
                    variant="outline"
                    className="h-14 rounded-full px-8 text-base"
                  >
                    <Link href="/provide">List your GPU</Link>
                  </Button>
                </div>

                <p className="mt-8 font-mono text-sm text-muted-foreground">
                  Hedera testnet only. Real transactions, real transaction ids — not real money.
                </p>
              </div>

              <div className="-mr-16 hidden h-[500px] w-[500px] items-center justify-center lg:flex">
                <AnimatedTetrahedron ink="0, 232, 122" />
              </div>
            </div>
          </div>

          <div className="absolute top-0 right-0 h-32 w-32 border-b border-l border-foreground/10" />
          <div className="absolute bottom-0 left-0 h-32 w-32 border-t border-r border-foreground/10" />
        </div>
      </div>
    </section>
  );
}
