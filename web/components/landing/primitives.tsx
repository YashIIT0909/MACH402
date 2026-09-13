"use client";

import { useEffect, useRef, useState } from "react";
import gsap from "gsap";
import { ScrollTrigger } from "gsap/ScrollTrigger";

import { cn } from "@/lib/utils";

gsap.registerPlugin(ScrollTrigger);

/**
 * Reveal-on-scroll for a whole section.
 *
 * Attach the returned ref to the section and mark the pieces that should
 * arrive with `data-reveal`. One ScrollTrigger scoped to the section handles
 * all of them, rather than an IntersectionObserver per element and a boolean
 * threaded through every className.
 *
 * `gsap.from` leaves the markup visible, so a reader without JavaScript gets
 * the page rather than a blank one — GSAP only takes over once it has loaded.
 */
export function useSectionReveal<T extends HTMLElement = HTMLDivElement>() {
  const ref = useRef<T>(null);

  useEffect(() => {
    const section = ref.current;
    if (!section) return;

    const context = gsap.context(() => {
      const pieces = gsap.utils.toArray<HTMLElement>("[data-reveal]", section);
      if (pieces.length === 0) return;

      if (window.matchMedia("(prefers-reduced-motion: reduce)").matches) {
        gsap.set(pieces, { opacity: 1, y: 0 });
        return;
      }

      gsap.from(pieces, {
        opacity: 0,
        y: 24,
        duration: 0.5,
        // Small enough that the last item does not feel left behind. Sections
        // with long lists stagger their rows separately, not through this.
        stagger: 0.08,
        ease: "power2.out",
        scrollTrigger: { trigger: section, start: "top 85%" },
      });
    }, section);

    return () => context.revert();
  }, []);

  return ref;
}

/**
 * Whether an element has been seen yet, latching true the first time.
 *
 * Separate from the entrance animation on purpose: this is for sections that
 * rotate through content on a timer and should not start counting until
 * someone is actually looking, so a reader arriving late does not land
 * mid-cycle on a step they never saw begin.
 */
export function useSeen<T extends HTMLElement = HTMLDivElement>(ref: React.RefObject<T | null>) {
  const [seen, setSeen] = useState(false);

  useEffect(() => {
    const element = ref.current;
    if (!element || seen) return;

    const observer = new IntersectionObserver(
      ([entry]) => {
        if (entry.isIntersecting) setSeen(true);
      },
      { threshold: 0.1 },
    );

    observer.observe(element);
    return () => observer.disconnect();
  }, [ref, seen]);

  return seen;
}

/**
 * The rule-and-label that opens every section.
 *
 * This exists because the same thing was being hand-written as a bare
 * `font-mono ... uppercase` span in seventeen places, which is why the audit,
 * pricing and footer sections did not quite match the rest of the page.
 */
export function Eyebrow({
  children,
  className,
  tone = "muted",
  centered = false,
  ...rest
}: React.ComponentProps<"span"> & {
  /** `accent` for a label that reports something live or settled. */
  tone?: "muted" | "accent";
  centered?: boolean;
}) {
  return (
    <span
      {...rest}
      className={cn(
        // Top-aligned with the rule dropped to the middle of the first line
        // (0.6em of a 1.2em line), so a label that wraps on a phone keeps its
        // rule beside its opening words instead of floating between two lines.
        "type-eyebrow inline-flex items-start gap-3 [text-wrap:balance]",
        tone === "accent" ? "text-accent" : "text-muted-foreground",
        className,
      )}
    >
      <span className={cn("mt-[0.6em] h-px w-8 shrink-0", tone === "accent" ? "bg-accent/50" : "bg-foreground/30")} />
      {children}
      {centered ? (
        <span className={cn("mt-[0.6em] h-px w-8 shrink-0", tone === "accent" ? "bg-accent/50" : "bg-foreground/30")} />
      ) : null}
    </span>
  );
}

/**
 * Code rendered a character at a time.
 *
 * Shared by the how-it-works and developer panels so the two reveals stay in
 * step. `revealKey` restarts the animation when the displayed snippet changes.
 */
export function RevealedCode({
  code,
  revealKey,
  lineNumbers = false,
  className,
}: {
  code: string;
  revealKey: string | number;
  lineNumbers?: boolean;
  className?: string;
}) {
  return (
    <pre className={cn("text-foreground/75", className)}>
      {code.split("\n").map((line, lineIndex) => (
        <div
          key={`${revealKey}-${lineIndex}`}
          className="code-line-reveal leading-loose"
          style={{ animationDelay: `${lineIndex * 80}ms` }}
        >
          {lineNumbers ? (
            <span className="inline-block w-8 select-none text-accent/40">{lineIndex + 1}</span>
          ) : null}
          <span className="inline-flex">
            {line.split("").map((char, charIndex) => (
              <span
                key={`${revealKey}-${lineIndex}-${charIndex}`}
                className="code-char-reveal"
                style={{ animationDelay: `${lineIndex * 80 + charIndex * 15}ms` }}
              >
                {char === " " ? "\u00A0" : char}
              </span>
            ))}
          </span>
        </div>
      ))}
    </pre>
  );
}
