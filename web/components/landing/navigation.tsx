"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import Link from "next/link";
import { usePathname } from "next/navigation";


/**
 * The site chrome: a vertical navigation that is hidden until it is asked for.
 *
 * At rest there is no navigation bar, only a readout — where you are, top
 * right. The readout is also the control: clicking it slides a column in from
 * the right holding every destination stacked vertically.
 *
 * Three decisions, each of them about not inventing anything:
 *
 *   - **The readout is not a box.** It borrows the `Eyebrow` grammar that opens
 *     every section on this site — a short rule, then a tracked mono label — so
 *     it reads as the same kind of object as the labels further down the page
 *     rather than a control parked on top of them. What keeps it legible over
 *     the hero is a full-bleed gradient at the top of the page, which has no
 *     edge to notice, instead of a bordered panel, which is all edge.
 *   - **That rule is the scroll progress.** The eyebrow's rule already exists;
 *     filling it with the accent as the document advances makes a decorative
 *     mark carry information instead of adding a second element that does.
 *   - **The names roll.** Every section name is rendered stacked inside a
 *     one-line window, and the stack is translated so the current one sits in
 *     it. Moving between sections rolls the next name up in the direction the
 *     page just moved — a crossfade would say "this changed", the roll says
 *     "you moved", which is the true statement.
 *
 * The open column is the how-it-works step list, vertically: `bg-panel` one
 * step off the background, the same hatch wash, rows on the same hairline
 * rhythm, state carried by opacity and one accent. Nothing here is blurred —
 * a `backdrop-filter` on a transforming full-height surface is what made this
 * tear on the way open.
 */

/** The landing page's sections, in document order — a real sequence, so numbered. */
const SECTIONS = [
  { id: "top", name: "Overview" },
  { id: "mcp", name: "MCP" },
  { id: "how-it-works", name: "How it works" },
  { id: "developers", name: "Developers" },
] as const;

/**
 * Destinations, not positions — so deliberately not numbered. Numbering these
 * alongside the sections was what made one list look like two.
 */
const ROUTES = [
  { name: "Browse nodes", href: "/nodes" },
  { name: "List your GPU", href: "/provide" },
] as const;

const EXTERNAL = [{ name: "Get testnet HBAR", href: "https://portal.hedera.com" }] as const;

/** Where in the viewport a section counts as "the one you are in". */
const PROBE = 0.35;

export function Navigation() {
  const pathname = usePathname();
  const onLanding = pathname === "/";

  const [open, setOpen] = useState(false);
  const [index, setIndex] = useState(0);
  const [progress, setProgress] = useState(0);
  const frame = useRef<number | null>(null);

  /*
   * Scroll spy. A rAF-coalesced scroll listener rather than an
   * IntersectionObserver: the hero is a pinned section more than two viewports
   * tall, so "is it intersecting" is true for far longer than "am I in it", and
   * the thresholds needed to recover the difference are the same arithmetic
   * this does directly.
   */
  useEffect(() => {
    if (!onLanding) return;

    const measure = () => {
      frame.current = null;

      const probe = window.innerHeight * PROBE;
      let current = 0;
      SECTIONS.forEach((section, i) => {
        const el = document.getElementById(section.id);
        if (el && el.getBoundingClientRect().top <= probe) current = i;
      });
      setIndex(current);

      const span = document.documentElement.scrollHeight - window.innerHeight;
      setProgress(span > 0 ? Math.min(1, Math.max(0, window.scrollY / span)) : 0);
    };

    const onScroll = () => {
      if (frame.current === null) frame.current = requestAnimationFrame(measure);
    };

    measure();
    window.addEventListener("scroll", onScroll, { passive: true });
    window.addEventListener("resize", onScroll, { passive: true });
    return () => {
      window.removeEventListener("scroll", onScroll);
      window.removeEventListener("resize", onScroll);
      if (frame.current !== null) cancelAnimationFrame(frame.current);
    };
  }, [onLanding]);

  useEffect(() => {
    if (!open) return;
    const onKey = (event: KeyboardEvent) => {
      if (event.key === "Escape") setOpen(false);
    };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, [open]);

  /*
   * The page behind is deliberately NOT scroll-locked.
   *
   * `overflow: hidden` on the body removes the scrollbar, which shifts the
   * whole layout left by its width the instant the panel opens — the jump that
   * read as the panel opening badly — and it also yanks the ground out from
   * under the hero's pinned ScrollTrigger. `overscroll-contain` on the list
   * below is what actually stops a flick inside the column from chaining into
   * the page, which was the only thing the lock was there for.
   */

  const goToSection = useCallback((id: string) => {
    setOpen(false);
    // The panel's close and a smooth scroll starting in the same frame compete
    // for the same 560ms; letting the column leave first makes the scroll
    // legible rather than something that happened behind a moving edge.
    window.setTimeout(() => {
      document.getElementById(id)?.scrollIntoView({ behavior: "smooth", block: "start" });
    }, 180);
  }, []);

  const routeLabel = ROUTES.find((route) => pathname.startsWith(route.href))?.name;
  const labels = onLanding ? SECTIONS.map((s) => s.name) : [routeLabel ?? "ClearGate"];
  const position = onLanding ? index : 0;

  return (
    <>
      {/*
       * The only reason the readout is readable over the hero. A gradient has
       * no edge to register as a second surface, where the bordered box it
       * replaces announced itself as one.
       *
       * Solid for its first 45% — 72px, the band the wordmark and readout sit
       * in — and only then fading. Fading from the top edge left a section
       * title scrolling under the header showing through the wordmark at a
       * fifth of its brightness, which read as the two colliding.
       */}
      <div
        aria-hidden="true"
        className="pointer-events-none fixed inset-x-0 top-0 z-40 h-40 bg-gradient-to-b from-background from-45% to-transparent"
      />

      <header className="pointer-events-none fixed inset-x-0 top-0 z-50">
        <div className="mx-auto flex max-w-[1400px] items-center justify-between px-6 pt-7 lg:px-12">
          <Link
            href="/"
            className="group pointer-events-auto flex items-baseline gap-2"
            aria-label="ClearGate home"
          >
            <span className="type-wordmark">ClearGate</span>
            {/*
             * Stated in the chrome of every page, because it changes what every
             * amount on the site means.
             */}
            <span className="type-label text-muted-foreground transition-colors ease-brand dur-base group-hover:text-accent">
              testnet
            </span>
          </Link>

          <button
            type="button"
            onClick={() => setOpen(true)}
            aria-expanded={open}
            aria-controls="site-nav-panel"
            aria-label="Open navigation"
            className={`group flex items-center gap-3 transition-opacity ease-brand dur-base ${
              open ? "pointer-events-none opacity-0" : "pointer-events-auto opacity-100"
            }`}
          >
            {/*
             * The Eyebrow rule, carrying the document's scroll progress. Same
             * 2rem hairline that opens every section; the accent is the only
             * thing added to it.
             */}
            <span className="hidden h-px w-8 bg-foreground/30 sm:block" aria-hidden="true">
              <span
                className="block h-px origin-left bg-accent transition-transform ease-brand dur-base motion-reduce:transition-none"
                style={{ transform: `scaleX(${onLanding ? progress : 0})` }}
              />
            </span>

            {onLanding ? (
              <span className="type-nav text-accent/70">
                {String(position + 1).padStart(2, "0")}
              </span>
            ) : null}

            {/*
             * One line tall, with every name stacked inside it — so the
             * transform rolls the next one into view instead of swapping text.
             */}
            {/*
             * Slots are 1.2em — `.type-nav`'s own line-height, and 40px at the
             * face's native size. A 1em slot would be 33⅓px, so every name past
             * the first would come to rest a third of a pixel off the grid and
             * blur; 1.2em keeps each resting position on a whole pixel.
             */}
            {/*
             * Hidden below sm: at 33⅓px the wordmark and a section name need
             * about 405px, and a phone has 342. The number still says where you
             * are, and the panel it opens names every section.
             *
             * Width follows the current name. The face is monospaced, so a
             * label is exactly its length in advances — 0.45em a glyph plus
             * 0.03em of tracking — and the menu mark can hug whichever name is
             * showing instead of waiting at the end of the longest one.
             */}
            <span
              className="type-nav hidden h-[1.2em] overflow-hidden text-left text-foreground/80 transition-[width,color] ease-brand dur-slow group-hover:text-foreground motion-reduce:transition-none sm:block"
              style={{ width: `${(labels[position]?.length ?? 0) * 0.48}em` }}
            >
              <span
                className="block transition-transform ease-brand dur-slow motion-reduce:transition-none"
                style={{ transform: `translateY(-${position * 1.2}em)` }}
              >
                {labels.map((label) => (
                  <span key={label} className="block h-[1.2em] whitespace-nowrap">
                    {label}
                  </span>
                ))}
              </span>
            </span>

            {/* Three rules, the middle one short: a menu mark that is not a hamburger. */}
            <span className="ml-1 flex w-4 flex-col items-end gap-[3px]" aria-hidden="true">
              <span className="h-px w-full bg-foreground/40 transition-colors ease-brand dur-base group-hover:bg-accent" />
              <span className="h-px w-1/2 bg-foreground/40 transition-all ease-brand dur-base group-hover:w-full group-hover:bg-accent" />
              <span className="h-px w-full bg-foreground/40 transition-colors ease-brand dur-base group-hover:bg-accent" />
            </span>
          </button>
        </div>
      </header>

      {/* Click-catcher. Dims only — no blur, which is what cost the frames. */}
      <div
        onClick={() => setOpen(false)}
        aria-hidden={!open}
        className={`fixed inset-0 z-40 bg-background/70 transition-opacity ease-brand dur-slow motion-reduce:transition-none ${
          open ? "pointer-events-auto opacity-100" : "pointer-events-none opacity-0"
        }`}
      />

      <nav
        id="site-nav-panel"
        aria-hidden={!open}
        style={{ willChange: "transform" }}
        className={`fixed inset-y-0 right-0 z-50 flex w-[min(82vw,340px)] flex-col border-l border-foreground/10 bg-panel transition-transform ease-brand dur-slow motion-reduce:transition-none md:w-[20vw] md:min-w-[300px] md:max-w-[420px] ${
          open ? "translate-x-0" : "pointer-events-none translate-x-full"
        }`}
      >
        {/* The same emphasis wash the how-it-works band uses, at the same opacity. */}
        <div className="hatch pointer-events-none absolute inset-0 opacity-[0.035]" />

        <div className="relative flex items-center justify-end px-6 pt-7 pb-4">
          <button
            type="button"
            onClick={() => setOpen(false)}
            aria-label="Close navigation"
            className="type-label text-muted-foreground transition-colors ease-brand dur-base hover:text-foreground"
          >
            Esc
          </button>
        </div>

        {/*
         * The list scrolls on its own. `overscroll-contain` keeps a flick at
         * either end from chaining into the page behind it.
         */}
        <div className="nav-scroll relative flex-1 overflow-y-auto overscroll-contain px-6">
          {/*
           * No headings over the two lists: the page sections carry numerals and
           * the destinations carry dots, and a heavier rule between them is
           * enough to read them as two groups. The rule sits in the middle of
           * its gap — the last section drops its own hairline and the space
           * above and below the rule is the same.
           */}
          <ul className="border-t border-foreground/10 [&>li:last-child>*]:border-b-0">
            {SECTIONS.map((section, i) => {
              const current = onLanding && i === index;
              const body = <SectionRow number={i + 1} name={section.name} current={current} />;
              return (
                <li key={section.id}>
                  {onLanding ? (
                    <button
                      type="button"
                      onClick={() => goToSection(section.id)}
                      aria-current={current ? "true" : undefined}
                      className="group block w-full border-b border-foreground/10 py-5 text-left"
                    >
                      {body}
                    </button>
                  ) : (
                    <Link
                      href={`/#${section.id}`}
                      onClick={() => setOpen(false)}
                      className="group block w-full border-b border-foreground/10 py-5"
                    >
                      {body}
                    </Link>
                  )}
                </li>
              );
            })}
          </ul>

          <ul className="mt-5 border-t border-foreground/25 pt-5 pb-6">
            {ROUTES.map((route) => (
              <li key={route.href}>
                <Link
                  href={route.href}
                  onClick={() => setOpen(false)}
                  aria-current={pathname.startsWith(route.href) ? "page" : undefined}
                  className="group block w-full border-b border-foreground/10 py-5"
                >
                  <DestinationRow
                    name={route.name}
                    current={pathname.startsWith(route.href)}
                  />
                </Link>
              </li>
            ))}
            {EXTERNAL.map((link) => (
              <li key={link.href}>
                <a
                  href={link.href}
                  target="_blank"
                  rel="noreferrer"
                  onClick={() => setOpen(false)}
                  className="group block w-full border-b border-foreground/10 py-5"
                >
                  <DestinationRow name={link.name} current={false} external />
                </a>
              </li>
            ))}
          </ul>
        </div>
      </nav>
    </>
  );
}

/**
 * A section row, which is the how-it-works step turned sideways: a mono numeral
 * that goes accent when it is the live one, a name that slides on hover, and
 * the whole row carried between states on opacity rather than on colour.
 */
function SectionRow({
  number,
  name,
  current,
}: {
  number: number;
  name: string;
  current: boolean;
}) {
  return (
    <span
      className={`flex items-baseline gap-4 transition-opacity ease-brand dur-base ${
        current ? "opacity-100" : "opacity-45 group-hover:opacity-80"
      }`}
    >
      <span
        className={`type-nav w-[calc(var(--tk)*0.9)] shrink-0 transition-colors ease-brand dur-slow ${
          current ? "text-accent" : "text-foreground/30"
        }`}
      >
        {String(number).padStart(2, "0")}
      </span>
      <span className="type-nav flex-1 transition-transform ease-brand dur-base group-hover:translate-x-1.5 motion-reduce:transition-none">
        {name}
      </span>
      <span
        aria-hidden="true"
        className={`h-px bg-accent transition-all ease-brand dur-base motion-reduce:transition-none ${
          current ? "w-6 opacity-100" : "w-0 opacity-0"
        }`}
      />
    </span>
  );
}

/**
 * A destination row. Same geometry, same type, same hover — the only thing it
 * drops is the numeral, because a link to another page has no position in a
 * sequence to report. The blank keeps the names on one optical column.
 */
function DestinationRow({
  name,
  current,
  external = false,
}: {
  name: string;
  current: boolean;
  external?: boolean;
}) {
  return (
    <span
      className={`flex items-baseline gap-4 transition-opacity ease-brand dur-base ${
        current ? "opacity-100" : "opacity-45 group-hover:opacity-80"
      }`}
    >
      <span className="type-nav w-[calc(var(--tk)*0.9)] shrink-0 text-foreground/20" aria-hidden="true">
        ·
      </span>
      <span className="type-nav flex-1 transition-transform ease-brand dur-base group-hover:translate-x-1.5 motion-reduce:transition-none">
        {name}
      </span>
      {external ? (
        <span className="type-label text-foreground/30" aria-hidden="true">
          ↗
        </span>
      ) : (
        <span
          aria-hidden="true"
          className={`h-px bg-accent transition-all ease-brand dur-base motion-reduce:transition-none ${
            current ? "w-6 opacity-100" : "w-0 opacity-0"
          }`}
        />
      )}
    </span>
  );
}
