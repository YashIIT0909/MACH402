"use client";

import { useEffect, useRef, useState } from "react";

import type { RegistrySnapshot } from "@/lib/registry";
import { hbar } from "@/lib/registry";
import { CONTAINER } from "./layout";
import { Eyebrow, useReveal } from "./primitives";

/**
 * A figure that counts up the first time it scrolls into view.
 *
 * A metric carrying a `display` string is shown verbatim and never counted:
 * the only one of those is a tinybar price, and a tinybar figure can exceed
 * what a double holds exactly. Amounts are strings end to end in ClearGate,
 * and an entrance animation is not a good enough reason to make one a number.
 */
function Figure({ end, display, suffix }: Metric) {
  const [count, setCount] = useState(0);
  const [shown, setShown] = useState(false);
  const ref = useRef<HTMLDivElement>(null);
  const startedRef = useRef(false);

  useEffect(() => {
    const element = ref.current;
    if (!element) return;

    const observer = new IntersectionObserver(
      ([entry]) => {
        if (!entry.isIntersecting || startedRef.current) return;
        startedRef.current = true;
        setShown(true);

        if (end === undefined) return;

        if (window.matchMedia("(prefers-reduced-motion: reduce)").matches) {
          setCount(end);
          return;
        }

        const duration = 1600;
        const startTime = performance.now();

        const animate = (now: number) => {
          const progress = Math.min((now - startTime) / duration, 1);
          const eased = 1 - Math.pow(1 - progress, 3);
          setCount(Math.floor(eased * end));
          if (progress < 1) requestAnimationFrame(animate);
        };

        requestAnimationFrame(animate);
      },
      { threshold: 0.5 },
    );

    observer.observe(element);
    return () => observer.disconnect();
  }, [end]);

  return (
    <div
      ref={ref}
      className={`font-display text-6xl tracking-tight lg:text-8xl ${
        display === undefined ? "" : `transition-opacity duration-700 ${shown ? "opacity-100" : "opacity-0"}`
      }`}
    >
      {display ?? count.toLocaleString()}
      {suffix}
    </div>
  );
}

/** Either a number to count to, or a string to show as-is. Never both. */
type Metric = { end?: number; display?: string; suffix?: string; label: string };

/** What the registry can tell us right now. */
function liveMetrics(snapshot: RegistrySnapshot): Metric[] {
  return [
    { end: snapshot.listed, label: "Nodes listed" },
    { end: snapshot.online, label: "Online and taking jobs" },
    { end: snapshot.gpus, label: "With a usable GPU" },
    {
      display: snapshot.cheapestTinybars === null ? "—" : hbar(snapshot.cheapestTinybars),
      label: "Cheapest job, in HBAR",
    },
  ];
}

/**
 * What is true whether or not the registry answered. Listing is opt-in and a
 * registry outage never interrupts a paid job, so the page has something
 * honest to say either way rather than reporting zero nodes.
 */
const constantMetrics: Metric[] = [
  { end: 300, suffix: "s", label: "Before a signed payment expires" },
  { end: 30, suffix: "s", label: "Between node heartbeats" },
  { end: 90, suffix: "s", label: "Until a silent node reads offline" },
  { end: 0, label: "Private keys held by a node" },
];

export function MetricsSection({ snapshot }: { snapshot: RegistrySnapshot | null }) {
  const { ref, revealed } = useReveal();
  const [time, setTime] = useState<string | null>(null);

  // Rendered only after mount: the server's clock and the reader's disagree,
  // and hydration would flag it.
  useEffect(() => {
    const tick = () => setTime(new Date().toLocaleTimeString());
    tick();
    const interval = setInterval(tick, 1000);
    return () => clearInterval(interval);
  }, []);

  const live = snapshot !== null;
  const metrics = live ? liveMetrics(snapshot) : constantMetrics;

  return (
    <section
      ref={ref}
      className="relative border-y border-foreground/10 py-24 lg:py-32"
    >
      <div className={CONTAINER}>
        <div className="mb-16 flex flex-col gap-8 lg:mb-24 lg:flex-row lg:items-end lg:justify-between">
          <div>
            <Eyebrow className="mb-6">{live ? "Live from the registry" : "Protocol constants"}</Eyebrow>
            <h2
              className={`font-display text-4xl tracking-tight transition-all duration-700 lg:text-6xl ${
                revealed ? "translate-y-0 opacity-100" : "translate-y-4 opacity-0"
              }`}
            >
              {live ? (
                <>
                  What is on the
                  <br />
                  network right now.
                </>
              ) : (
                <>
                  The numbers that
                  <br />
                  do not move.
                </>
              )}
            </h2>
          </div>

          <div className="flex items-center gap-4 font-mono text-sm text-muted-foreground">
            {live ? (
              <>
                <span className="flex items-center gap-2 text-accent">
                  <span className="h-2 w-2 rounded-full bg-accent" />
                  Live
                </span>
                <span className="text-foreground/30">|</span>
                <span>{time ?? "--:--:--"}</span>
              </>
            ) : (
              <span className="flex items-center gap-2">
                <span className="h-2 w-2 rounded-full border border-muted-foreground" />
                Registry unreachable
              </span>
            )}
          </div>
        </div>

        <div className="grid grid-cols-1 gap-px bg-foreground/10 md:grid-cols-2">
          {metrics.map((metric, index) => (
            <div
              key={metric.label}
              className={`bg-background p-8 transition-all duration-700 lg:p-12 ${
                revealed ? "translate-y-0 opacity-100" : "translate-y-8 opacity-0"
              }`}
              style={{ transitionDelay: `${index * 100}ms` }}
            >
              <Figure {...metric} />
              <div className="mt-4 text-lg text-muted-foreground">{metric.label}</div>
            </div>
          ))}
        </div>

        {!live ? (
          <p className="mt-8 font-mono text-sm text-muted-foreground">
            Listing is opt-in and failure is silent — a registry that is down never interrupts a
            paid job. Start one with <span className="text-foreground">make dev-registry</span>.
          </p>
        ) : null}
      </div>
    </section>
  );
}
