"use client";

import { useEffect, useRef, useState } from "react";

import { cn } from "@/lib/utils";

/**
 * Reveal-on-scroll, shared by every section.
 *
 * Returns a ref to attach and a flag that latches true the first time the
 * element enters the viewport — it never flips back, so scrolling up does not
 * re-run the entrance and the page settles down after one pass.
 */
export function useReveal<T extends HTMLElement = HTMLDivElement>(threshold = 0.1) {
  const ref = useRef<T>(null);
  const [revealed, setRevealed] = useState(false);

  useEffect(() => {
    const element = ref.current;
    if (!element) return;

    const observer = new IntersectionObserver(
      ([entry]) => {
        if (entry.isIntersecting) setRevealed(true);
      },
      { threshold },
    );

    observer.observe(element);
    return () => observer.disconnect();
  }, [threshold]);

  return { ref, revealed };
}

/** The rule-and-label that opens every section. */
export function Eyebrow({
  children,
  className,
  centered = false,
}: {
  children: React.ReactNode;
  className?: string;
  centered?: boolean;
}) {
  return (
    <span
      className={cn(
        "inline-flex items-center gap-3 font-mono text-sm text-muted-foreground",
        className,
      )}
    >
      <span className="h-px w-8 bg-foreground/30" />
      {children}
      {centered ? <span className="h-px w-8 bg-foreground/30" /> : null}
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
            <span className="inline-block w-8 select-none text-foreground/25">{lineIndex + 1}</span>
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
