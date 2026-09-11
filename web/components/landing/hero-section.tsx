"use client";

import { useEffect, useRef } from "react";
import Link from "next/link";
import { ArrowRight } from "lucide-react";
import gsap from "gsap";
import { ScrollTrigger } from "gsap/ScrollTrigger";

import { Button } from "@/components/ui/button";
import { GpuModel } from "./gpu-model";
import { CONTAINER } from "./layout";
import { Eyebrow } from "./primitives";

gsap.registerPlugin(ScrollTrigger);

/**
 * How much of a viewport height the cover holds for before the page moves on.
 * Enough travel for the copy to arrive deliberately, short enough that the
 * hero is not two full screens of scrolling on its own.
 */
const HOLD_VH = 55;

/**
 * The copy's resting state, server-rendered.
 *
 * GSAP cannot hide these until it has hydrated, and by then the text has
 * already been painted — so the page flashed its headline and then dropped it
 * to reveal the card. Shipping the hidden state in the markup removes the
 * flash; the <noscript> below puts it back for anyone without JS, who would
 * otherwise get a blank hero.
 */
const HIDDEN: React.CSSProperties = {
  opacity: 0,
  transform: "scale(0.965)",
  filter: "blur(10px)",
};

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
      <noscript>
        {/* Without JS nothing ever reveals the copy, so undo the resting state. */}
        <style>{`[data-reveal]{opacity:1!important;transform:none!important;filter:none!important}`}</style>
      </noscript>

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

        {/*
         * The copy sits bottom-left rather than centred, and inside a column
         * roughly half the width. Centred and full-bleed it landed exactly on
         * the card — the two occupied the same band of the screen — so the
         * hero read as text over a texture rather than text beside an object.
         */}
        <div
          ref={copyRef}
          className="relative z-10 flex h-full flex-col justify-end pb-20 lg:pb-28"
        >
          {/*
           * `w-full` is load-bearing: CONTAINER carries `mx-auto`, and auto
           * horizontal margins on a flex item override `align-items: stretch`,
           * so without it the container shrink-wraps to the copy and centres
           * itself instead of spanning the viewport.
           */}
          <div className={`${CONTAINER} w-full`}>
            <div className="max-w-[34rem] lg:max-w-[44rem]">
              <Eyebrow data-reveal style={HIDDEN} className="mb-8">
                GPU rental, settled per job
              </Eyebrow>

              <h1 data-reveal style={HIDDEN} className="type-display mb-8">
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

              <p
                data-reveal
                style={HIDDEN}
                className="type-lede mb-10 max-w-xl text-muted-foreground"
              >
                Providers run a daemon on an idle GPU. Renters — people at a terminal, or
                autonomous agents — pay that node directly for one job and get a container run on
                it. No API keys, no subscriptions, no escrow.
              </p>

              <div
                data-reveal
                style={HIDDEN}
                className="pointer-events-auto flex flex-col items-start gap-4 sm:flex-row"
              >
                <Button asChild size="lg" className="group">
                  <Link href="/nodes">
                    Browse nodes
                    <ArrowRight className="transition-transform group-hover:translate-x-1" />
                  </Link>
                </Button>
                <Button asChild size="lg" variant="outline">
                  <Link href="/provide">List your GPU</Link>
                </Button>
              </div>
            </div>
          </div>
        </div>
      </div>
    </section>
  );
}
