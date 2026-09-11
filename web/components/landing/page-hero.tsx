import { AnimatedSphere } from "./ascii-canvas";
import { CONTAINER } from "./layout";

/**
 * The opening band on the pages that are not the landing page.
 *
 * Same vocabulary as the hero — grid lines, glyph sphere, serif display — at
 * about half the height, because these pages have work below them and the
 * reader arrived wanting to get to it.
 */
export function PageHero({
  eyebrow,
  title,
  children,
}: {
  eyebrow: React.ReactNode;
  title: React.ReactNode;
  children?: React.ReactNode;
}) {
  return (
    <section className="relative overflow-hidden border-b border-foreground/10">
      <div className="pointer-events-none absolute top-1/2 right-0 h-[420px] w-[420px] -translate-y-1/2 opacity-30 lg:h-[560px] lg:w-[560px]">
        <AnimatedSphere ink="0, 232, 122" />
      </div>

      <div className="pointer-events-none absolute inset-0 overflow-hidden opacity-30">
        {[...Array(4)].map((_, i) => (
          <div
            key={`h-${i}`}
            className="absolute right-0 left-0 h-px bg-foreground/10"
            style={{ top: `${25 * (i + 1)}%` }}
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

      <div className={`relative z-10 pt-40 pb-20 lg:pt-48 lg:pb-28 ${CONTAINER}`}>
        <span className="mb-6 inline-flex items-center gap-3 font-mono text-sm text-muted-foreground">
          <span className="h-px w-8 bg-foreground/30" />
          {eyebrow}
        </span>

        <h1 className="font-display text-[clamp(2.75rem,9vw,7rem)] leading-[0.92] tracking-tight">
          {title}
        </h1>

        {children ? (
          <div className="mt-8 max-w-2xl text-xl leading-relaxed text-muted-foreground">
            {children}
          </div>
        ) : null}
      </div>
    </section>
  );
}
