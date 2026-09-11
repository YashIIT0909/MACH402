"use client";

import { useEffect, useRef } from "react";
import Link from "next/link";
import { ArrowRight } from "lucide-react";
import gsap from "gsap";
import { ScrollTrigger } from "gsap/ScrollTrigger";

import { Button } from "@/components/ui/button";
import { GpuModel } from "./gpu-model";
import { CONTAINER } from "./layout";

gsap.registerPlugin(ScrollTrigger);

/*
 * The marquee under the hero. Every figure here is a constant the code
 * actually enforces, not a marketing number — `maxTimeoutSeconds` on the 402,
 * the registry's freshness window, the sandbox flags, the count of private
 * keys a provider node holds.
 */
const facts = [
  { value: "0", label: "private keys held by a node", tag: "INVARIANT" },
  { value: "300s", label: "before a signed payment expires", tag: "MAXTIMEOUTSECONDS" },
  { value: "90s", label: "until a silent node reads offline", tag: "HEARTBEAT" },
  { value: "none", label: "network inside a job container", tag: "--network=none" },
];

/**
 * How many viewport heights of scrolling the cover holds for before the page
 * moves on. One full screen of travel: enough for the copy to arrive
 * deliberately, short enough that nobody wonders if the page is stuck.
 */
const HOLD_VH = 100;

export function HeroSection() {
  const sectionRef = useRef<HTMLElement>(null);
  const copyRef = useRef<HTMLDivElement>(null);
  // Read by the render loop every frame; never a React state update.
  const progress = useRef(0);

  useEffect(() => {
    const section = sectionRef.current;
    const copy = copyRef.current;
    if (!section || !copy) return;

    const still = window.matchMedia("(prefers-reduced-motion: reduce)").matches;
    const pieces = gsap.utils.toArray<HTMLElement>("[data-reveal]", copy);
    const scrim = section.querySelector("[data-scrim]");

    if (still) {
      // No cover sequence: show the copy immediately and leave the card static.
      gsap.set(pieces, { opacity: 1, scale: 1, filter: "none" });
      gsap.set(scrim, { opacity: 1 });
      progress.current = 1;
      return;
    }

    const context = gsap.context(() => {
      gsap.set(pieces, { opacity: 0, scale: 0.965, filter: "blur(10px)" });

      /*
       * The pin is CSS `position: sticky` on the inner frame rather than
       * ScrollTrigger's own pinning, which reparents the DOM and fights the
       * fixed navigation. ScrollTrigger is used only to scrub the timeline.
       */
      const timeline = gsap.timeline({
        scrollTrigger: {
          trigger: section,
          start: "top top",
          end: "bottom bottom",
          scrub: 0.6,
          onUpdate: (self) => {
            progress.current = self.progress;
          },
        },
      });

      // Nothing happens for the first stretch — the cover is allowed to be a
      // cover before the copy starts arriving.
      timeline
        .to({}, { duration: 0.25 })
        .to(scrim, { opacity: 1, duration: 0.6, ease: "none" }, "<")
        .to(
          pieces,
          {
            opacity: 1,
            scale: 1,
            filter: "blur(0px)",
            duration: 0.75,
            stagger: 0.12,
            ease: "power2.out",
          },
          "<0.1",
        )
        // The one colour move: the accent bar draws itself under the word.
        .fromTo(
          "[data-accent-bar]",
          { scaleX: 0, transformOrigin: "left center" },
          { scaleX: 1, duration: 0.5, ease: "power2.out" },
          "<0.25",
        );
    }, section);

    return () => context.revert();
  }, []);

  return (
    <section ref={sectionRef} className="relative" style={{ height: `${100 + HOLD_VH}vh` }}>
      {/* The frame that stays put while the section scrolls past it. */}
      <div className="sticky top-0 h-screen overflow-hidden">
        <GpuModel className="pointer-events-none absolute inset-0" progress={progress} />

        <div className="pointer-events-none absolute inset-0 overflow-hidden opacity-20">
          {[...Array(8)].map((_, i) => (
            <div
              key={`h-${i}`}
              className="absolute right-0 left-0 h-px bg-foreground/10"
              style={{ top: `${12.5 * (i + 1)}%` }}
            />
          ))}
          {[...Array(12)].map((_, i) => (
            <div
              key={`v-${i}`}
              className="absolute top-0 bottom-0 w-px bg-foreground/10"
              style={{ left: `${8.33 * (i + 1)}%` }}
            />
          ))}
        </div>

        {/*
         * The copy lands on top of a lit graphics card, so it needs a ground to
         * sit on. This rakes in from the left exactly as the text arrives —
         * dark where the words are, clear where the fan is.
         */}
        <div
          data-scrim
          className="pointer-events-none absolute inset-0 z-[5] opacity-0"
          style={{
            background:
              "linear-gradient(97deg, var(--background) 10%, rgba(8,9,10,0.88) 34%, rgba(8,9,10,0.45) 58%, rgba(8,9,10,0.12) 78%, transparent 92%)",
          }}
        />

        <div ref={copyRef} className="relative z-10 flex h-full flex-col justify-center">
          <div className={CONTAINER}>
            <span
              data-reveal
              className="mb-8 inline-flex items-center gap-3 font-mono text-sm text-muted-foreground"
            >
              <span className="h-px w-8 bg-foreground/30" />
              GPU rental, settled per job
            </span>

            <h1
              data-reveal
              className="mb-12 font-display text-[clamp(2.75rem,9vw,7.5rem)] leading-[0.92] tracking-tight"
            >
              <span className="block">Rent the machine.</span>
              <span className="block text-muted-foreground">
                Pay the{" "}
                <span className="relative inline-block text-foreground">
                  machine
                  <span
                    data-accent-bar
                    className="absolute -bottom-1 left-0 h-2 w-full bg-accent/30"
                  />
                </span>
                .
              </span>
            </h1>

            <div className="grid items-end gap-10 lg:grid-cols-2 lg:gap-20">
              <p data-reveal className="max-w-xl text-lg leading-relaxed text-muted-foreground lg:text-xl">
                Providers run a daemon on an idle GPU. Renters — people at a terminal, or
                autonomous agents — pay that node directly for one job and get a container run on
                it. No API keys, no subscriptions, no escrow.
              </p>

              <div data-reveal className="pointer-events-auto flex flex-col items-start gap-4 sm:flex-row">
                <Button asChild size="lg" className="group h-14 rounded-full px-8 text-base">
                  <Link href="/nodes">
                    Browse nodes
                    <ArrowRight className="ml-2 h-4 w-4 transition-transform group-hover:translate-x-1" />
                  </Link>
                </Button>
                <Button asChild size="lg" variant="outline" className="h-14 rounded-full px-8 text-base">
                  <Link href="/provide">List your GPU</Link>
                </Button>
              </div>
            </div>
          </div>

          <div data-reveal className="absolute right-0 bottom-16 left-0">
            <div className="marquee flex gap-16 whitespace-nowrap">
              {[...Array(2)].map((_, i) => (
                <div key={i} className="flex shrink-0 gap-16">
                  {facts.map((fact) => (
                    <div key={`${fact.tag}-${i}`} className="flex items-baseline gap-4">
                      <span className="font-display text-4xl lg:text-5xl">{fact.value}</span>
                      <span className="text-sm text-muted-foreground">
                        {fact.label}
                        <span className="mt-1 block font-mono text-xs text-foreground/40">
                          {fact.tag}
                        </span>
                      </span>
                    </div>
                  ))}
                </div>
              ))}
            </div>
          </div>
        </div>
      </div>
    </section>
  );
}
