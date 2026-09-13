"use client";

import { useEffect, useState } from "react";
import { Check, Copy } from "lucide-react";

import { Button } from "@/components/ui/button";
import { CONTAINER } from "./layout";
import { Eyebrow, useSectionReveal, useSeen } from "./primitives";

/*
 * The one line that wires this into an agent. `npx` rather than a global
 * install, because the point of the package is that an agent host can reach
 * it with nothing but Node — no checkout of this repo, no pnpm workspace.
 */
const ADD_COMMAND = "claude mcp add mach402 -- npx -y mach402-mcp-server";

/*
 * The tool surface, from packages/mcp-server/README.md, in the README's order
 * — which is the sequence an agent walks: look, price, buy, use, stop.
 *
 * Names only. Every gloss written for these ("search_compute_nodes — online
 * nodes selling metered sessions") restated the name in longer words and cost
 * a second line each, which is what made this column twice the height of the
 * panel beside it. `pays` is the one thing a name does not say, and the only
 * thing a reader deciding whether to hand an agent a key needs from a list.
 */
const tools = [
  { name: "search_compute_nodes", pays: false },
  { name: "get_node_quote", pays: false },
  { name: "open_session", pays: true },
  { name: "get_session_access", pays: false },
  { name: "top_up_session", pays: true },
  { name: "stop_session", pays: false },
  { name: "confirm_payment", pays: true },
];

/*
 * A mock agent, not a recording of one: the numbers are the same ones the
 * developer panel's 402 uses (3334 tinybars a second, a 300-second chunk), so
 * the two do not quote different prices for the same thing three screens apart.
 *
 * It shows the chunk cap doing its job on purpose — the agent asks for ten
 * minutes and the first payment buys five — because that cap is the whole
 * reason prepaying a stranger is bounded, and a transcript that hid it would
 * be selling a guarantee the node does not make.
 */
const transcript = [
  { kind: "user", text: "Fine-tune this adapter on a 4090. Ten minutes should do it." },
  { kind: "call", text: "search_compute_nodes", detail: "4 online", pays: false },
  { kind: "call", text: "get_node_quote", detail: "3334 tinybars/s", pays: false },
  { kind: "call", text: "open_session", detail: "1000200 tinybars", pays: true },
  { kind: "result", text: "sess_9f2c", detail: "jupyter ready · 300s credit" },
  { kind: "call", text: "top_up_session", detail: "1000200 tinybars", pays: true },
  { kind: "call", text: "stop_session", detail: "after 7 min", pays: false },
  { kind: "result", text: "refunded", detail: "600120 tinybars" },
  {
    kind: "agent",
    text:
      "Done. The first payment bought five minutes and one top-up bought the rest; the three minutes I didn't use came back to you.",
  },
] as const;

/** One beat per row, then one more where the finished transcript just sits. */
const STEP_MS = 1400;

export function McpSection() {
  const ref = useSectionReveal<HTMLElement>();
  const seen = useSeen(ref);
  const [shown, setShown] = useState(0);
  const [copied, setCopied] = useState(false);

  /*
   * Starts only once the section has been on screen, so a reader who scrolls
   * down two minutes in does not arrive to a transcript already half-played —
   * the same reason the how-it-works steps wait for `useSeen`.
   *
   * The extra step past the end is the pause: nothing renders differently, the
   * completed transcript simply holds for one beat before it starts over.
   */
  useEffect(() => {
    if (!seen) return;
    if (window.matchMedia("(prefers-reduced-motion: reduce)").matches) {
      setShown(transcript.length);
      return;
    }
    const interval = setInterval(() => {
      setShown((previous) => (previous > transcript.length ? 0 : previous + 1));
    }, STEP_MS);
    return () => clearInterval(interval);
  }, [seen]);

  async function copy() {
    await navigator.clipboard.writeText(ADD_COMMAND);
    setCopied(true);
    setTimeout(() => setCopied(false), 1500);
  }

  return (
    <section id="mcp" ref={ref} className="relative overflow-hidden py-16 lg:py-24">
      <div className={CONTAINER}>
        {/*
         * `minmax(0, 1fr)` on the single-column track for the same reason the
         * developer panel needs it: the transcript's longest mono line would
         * otherwise size the phone column past the viewport.
         */}
        <div className="grid grid-cols-[minmax(0,1fr)] gap-12 lg:grid-cols-2 lg:gap-16">
          <div data-reveal className="min-w-0">
            <Eyebrow className="mb-5">Model Context Protocol</Eyebrow>
            <h2 className="mb-5 type-title">
              Add one server.
              <br />
              <span className="text-muted-foreground">The agent rents the GPU.</span>
            </h2>
            <p className="mb-8 type-lede text-muted-foreground">
              It finds a node, prices it, pays by the second and stops — no glue code. The only
              secret is your own Hedera key, and it never leaves your machine.
            </p>

            <div className="border border-foreground/10">
              <div className="flex items-center justify-between gap-4 border-b border-foreground/10 px-5 py-2.5">
                <span className="type-label text-muted-foreground">Wire it up</span>
                <Button
                  variant="accent"
                  size="sm"
                  onClick={() => void copy()}
                  className="type-label h-7 px-4"
                >
                  {copied ? (
                    <>
                      Copied <Check className="h-3 w-3" />
                    </>
                  ) : (
                    <>
                      Copy <Copy className="h-3 w-3" />
                    </>
                  )}
                </Button>
              </div>
              {/*
               * The env vars ride inside the block as comment lines rather than
               * as a paragraph under it: they are part of what you set up, and
               * a prose sentence naming three SCREAMING_SNAKE identifiers reads
               * worse than the identifiers themselves.
               */}
              <pre className="overflow-x-auto bg-foreground/[0.02] p-5 font-mono text-sm leading-relaxed text-foreground/85">
                {ADD_COMMAND}
                {"\n"}
                <span className="text-muted-foreground/60">
                  {"\n# env: HEDERA_ACCOUNT_ID, HEDERA_PRIVATE_KEY, mach402_REGISTRY_URL"}
                </span>
              </pre>
            </div>
          </div>

          {/*
           * Stretched to the text column's height, so the two columns end on the
           * same line instead of leaving an empty patch under the terminal.
           */}
          <div data-reveal className="flex min-w-0 flex-col">
            <div className="flex flex-1 flex-col border border-foreground/10">
              <div className="flex items-center justify-between gap-4 border-b border-foreground/10 px-5 py-3">
                <ToolsHint />
                <span className="inline-flex items-center gap-2 type-label text-[0.6rem] text-accent">
                  <span className="h-1.5 w-1.5 rounded-full bg-accent">
                    <span className="sr-only">live</span>
                  </span>
                  connected
                </span>
              </div>

              {/*
               * The height is the finished transcript's own height, measured
               * rather than guessed: a hidden copy of every row sizes the box,
               * and the rows that have arrived are laid over it. A box that
               * grew as rows appeared would shove the page under the reader on
               * every beat, and a hand-set height is wrong again at the next
               * breakpoint — the agent's closing line wraps to three lines in
               * one column and four in another.
               */}
              <div className="relative flex-1 bg-foreground/[0.02] p-6 font-mono text-sm">
                <div aria-hidden="true" className="invisible space-y-4">
                  {transcript.map((line, index) => (
                    <TranscriptRow key={index} line={line} measuring />
                  ))}
                </div>
                <div className="absolute inset-0 space-y-4 overflow-hidden p-6">
                  {transcript.slice(0, shown).map((line, index) => (
                    <TranscriptRow key={index} line={line} />
                  ))}
                </div>
              </div>
            </div>
          </div>
        </div>
      </div>
    </section>
  );
}

/*
 * The terminal's title, with the tool list folded behind the "i" beside it.
 *
 * Seven rows of identifiers is reference material rather than an argument, so
 * it stays out of the column until asked for — on hover, or on keyboard focus,
 * since the button is real and focusable.
 */
function ToolsHint() {
  return (
    <div className="group relative flex items-center gap-3">
      <span className="type-label text-muted-foreground">agent · mcp</span>
      <button
        type="button"
        aria-label="List the seven tools"
        className="flex h-5 w-5 cursor-help items-center justify-center rounded-full border border-foreground/25 font-mono text-[0.65rem] text-muted-foreground transition-colors ease-brand dur-base outline-none group-hover:border-accent/60 group-hover:text-accent focus-visible:border-accent focus-visible:text-accent"
      >
        i
      </button>

      {/*
       * Opens downward over the transcript, anchored to the title it belongs
       * to. The top padding is a hover bridge, so moving the pointer from the
       * button into the card does not close it.
       *
       * `bg-panel` rather than a translucent ground: the page has a live
       * backdrop, and anything see-through here puts moving green behind mono
       * text.
       */}
      <div className="pointer-events-none absolute top-full left-0 z-20 w-[min(22rem,calc(100vw-4rem))] translate-y-1 pt-3 opacity-0 transition-[opacity,translate] ease-brand dur-base group-focus-within:translate-y-0 group-focus-within:opacity-100 group-hover:translate-y-0 group-hover:opacity-100">
        <div className="border border-foreground/15 bg-panel shadow-xl shadow-black/50">
          <div className="flex items-center justify-between gap-4 border-b border-foreground/10 px-4 py-2.5">
            <span className="type-label text-muted-foreground">Seven tools</span>
            <span className="flex items-center gap-4 type-label text-[0.6rem] text-muted-foreground">
              <span className="inline-flex items-center gap-1.5">
                <span className="h-1.5 w-1.5 rounded-full bg-accent" />
                pays
              </span>
              <span className="inline-flex items-center gap-1.5">
                <span className="h-1.5 w-1.5 rounded-full border border-foreground/40" />
                free
              </span>
            </span>
          </div>
          <ul className="divide-y divide-foreground/10">
            {tools.map((tool) => (
              <li key={tool.name} className="flex items-center justify-between gap-6 px-4 py-2.5">
                <span className="font-mono text-sm text-foreground">{tool.name}</span>
                {/* The one thing a name does not say: whether it can spend. */}
                <span
                  className={`h-1.5 w-1.5 shrink-0 rounded-full ${tool.pays ? "bg-accent" : "border border-foreground/40"}`}
                >
                  <span className="sr-only">{tool.pays ? "pays" : "free"}</span>
                </span>
              </li>
            ))}
          </ul>
        </div>
      </div>
    </div>
  );
}

/*
 * A row of the transcript. Four shapes, one grid: a marker column wide enough
 * for the widest marker, and the line beside it. Keeping them on one grid is
 * what makes the tool calls read as a column of calls rather than four
 * differently-indented paragraphs.
 */
function TranscriptRow({
  line,
  measuring = false,
}: {
  line: (typeof transcript)[number];
  /** A hidden copy used only to give the panel its height. */
  measuring?: boolean;
}) {
  const marker = { user: ">", call: "→", result: "←", agent: "" }[line.kind];
  const detail = "detail" in line ? line.detail : null;
  const pays = "pays" in line ? line.pays : false;

  return (
    <div
      className={`grid grid-cols-[1rem_minmax(0,1fr)_auto] items-baseline gap-x-3 ${measuring ? "" : "code-line-reveal"}`}
    >
      <span className={line.kind === "call" || line.kind === "result" ? "text-accent/70" : "text-muted-foreground"}>
        {marker}
      </span>
      <span
        className={
          line.kind === "agent"
            ? "col-span-2 leading-relaxed text-muted-foreground"
            : line.kind === "user"
              ? "col-span-2 text-foreground"
              : "min-w-0 truncate text-foreground/85"
        }
      >
        {line.text}
      </span>
      {detail ? (
        <span className={`text-xs ${pays ? "text-accent" : "text-muted-foreground/60"}`}>{detail}</span>
      ) : null}
    </div>
  );
}
