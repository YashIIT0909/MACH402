"use client";

import { useEffect, useState } from "react";

import { Button } from "@/components/ui/button";
import { CONTAINER } from "./layout";
import { Eyebrow, RevealedCode, useSectionReveal, useSeen } from "./primitives";

const steps = [
  {
    number: "I",
    title: "The renter asks for a session",
    description:
      "A session request goes to the node: how long, and the renter's SSH public key. Anything knowable in advance is checked now — the node's bounds, whether its lease image can use the GPU, whether its one slot is free — so a rejection costs the renter nothing.",
    file: "session.http",
    state: "402 issued",
    code: `POST /v1/sessions

{ "seconds": 300,
  "public_key": "ssh-ed25519 AAAA…",
  "require_gpu": true }

402 Payment Required
PAYMENT-REQUIRED: <base64>`,
  },
  {
    number: "II",
    title: "The renter signs",
    description:
      "A Hedera transfer is signed locally and the request is retried. The facilitator co-signs as fee payer, so the renter spends no gas — the property that makes this work for agents.",
    file: "payment.ts",
    state: "signed",
    code: `const payload = await signExact({
  amount:   "1000200",
  asset:    "0.0.0",
  payTo:    "0.0.1234",
  feePayer: supported.extra.feePayer
})

PAYMENT-SIGNATURE: <base64>`,
  },
  {
    number: "III",
    title: "The node proves it, then settles",
    description:
      "It verifies the payment, starts the container, points a tunnel at it and checks that it actually answers — and only then settles. A signed payment expires in 300 seconds, so all of that happens inside the window, and nobody is charged for a session that never came up.",
    file: "settle.http",
    state: "settled",
    code: `POST /verify       -> { isValid: true }
GET  <tunnel>/api  -> 302

POST /settle -> {
  success: true,
  transaction:
    "0.0.802@1730000000.1"
}`,
  },
  {
    number: "IV",
    title: "The credit burns, the rest comes back",
    description:
      "The payment is credit, burned by the second while the container runs. Every 15 seconds the node publishes what it would owe if the session stopped now to its own Hedera topic, and stopping refunds exactly that.",
    file: "meter.log",
    state: "refunded",
    code: `session_burn    elapsed 180s
                refund_tinybars 400080
session_burn    elapsed 195s
                refund_tinybars 350070

POST /v1/sessions/:id/stop
session_settle  refund_tinybars 350070`,
  },
];

const ROTATE_MS = 6000;

export function HowItWorksSection() {
  const [activeStep, setActiveStep] = useState(0);
  const ref = useSectionReveal<HTMLElement>();
  const seen = useSeen(ref);

  // Rotation starts only once the section is on screen, so a reader arriving
  // late does not land mid-cycle on a step they never saw begin.
  useEffect(() => {
    if (!seen) return;
    const interval = setInterval(() => {
      setActiveStep((prev) => (prev + 1) % steps.length);
    }, ROTATE_MS);
    return () => clearInterval(interval);
  }, [seen]);

  const active = steps[activeStep];

  return (
    <section
      id="how-it-works"
      ref={ref}
      className="relative overflow-hidden py-16 lg:py-24"
      style={{
        /*
         * A translucent panel that feathers in and out, not a solid band. The
         * landing page has a live backdrop behind it now, and a solid
         * `bg-panel` with hairline borders cut that backdrop off at two hard
         * horizontal edges. The emphasis band still reads — about half the
         * panel's weight through the middle — but its edges dissolve into
         * whatever is behind the page.
         */
        background:
          "linear-gradient(to bottom, transparent 0%, color-mix(in srgb, var(--panel) 55%, transparent) 16%, color-mix(in srgb, var(--panel) 55%, transparent) 84%, transparent 100%)",
      }}
    >
      <div
        className="hatch pointer-events-none absolute inset-0 opacity-[0.035]"
        style={{
          // Feathered on the same stops as the panel, so the hatch has no edge of its own.
          maskImage: "linear-gradient(to bottom, transparent, black 16%, black 84%, transparent)",
          WebkitMaskImage: "linear-gradient(to bottom, transparent, black 16%, black 84%, transparent)",
        }}
      />

      <div className={`relative z-10 ${CONTAINER}`}>
        <div className="mb-10 lg:mb-14">
          <Eyebrow className="mb-6">How a session is paid for</Eyebrow>
          <h2
            data-reveal className="type-title"
          >
            Four steps. The last one
            <br />
            <span className="text-muted-foreground">gives money back.</span>
          </h2>
        </div>

        <div className="grid grid-cols-[minmax(0,1fr)] gap-16 lg:grid-cols-2 lg:gap-24">
          <div className="min-w-0">
            {steps.map((step, index) => (
              <Button
                key={step.number}
                variant="quiet"
                shape="square"
                onClick={() => setActiveStep(index)}
                aria-pressed={activeStep === index}
                className={`group h-auto w-full justify-start border-b border-foreground/10 px-0 py-6 text-left whitespace-normal hover:bg-transparent ${
                  activeStep === index ? "opacity-100" : "opacity-40 hover:opacity-70"
                }`}
              >
                {/*
                 * Numeral, title and description are grid siblings. The numeral
                 * column is a fixed three advances — I, II, III and IV are
                 * different widths, and without it each title started at its own
                 * x. On a phone the description leaves that column and spans the
                 * row; indented, it was a 10-line strip a third of the screen wide.
                 *
                 * The width is written in `--tk`, not `3ch`: in a grid template
                 * `ch` resolves against this container's body font, which made
                 * the column 30px and wrapped "III" onto two lines. Three
                 * Ticketing advances at the subtitle size are 1.35 × --tk.
                 */}
                <div className="grid w-full grid-cols-[calc(var(--tk)*1.35)_minmax(0,1fr)] items-baseline gap-x-4 sm:gap-x-6">
                  <span
                    className={`type-subtitle transition-colors ease-brand dur-slow ${
                      activeStep === index ? "text-accent" : "text-foreground/30"
                    }`}
                  >
                    {step.number}
                  </span>
                  <h3 className="type-subtitle transition-transform ease-brand dur-base group-hover:translate-x-2">
                    {step.title}
                  </h3>
                  <div className="col-span-2 mt-2 sm:col-span-1 sm:col-start-2">
                    <p className="leading-relaxed text-muted-foreground">{step.description}</p>

                    {activeStep === index ? (
                      <div className="mt-4 h-px overflow-hidden bg-foreground/15">
                        <div
                          key={activeStep}
                          className="h-full w-0 bg-accent"
                          style={{ animation: `hiw-progress ${ROTATE_MS}ms linear forwards` }}
                        />
                      </div>
                    ) : null}
                  </div>
                </div>
              </Button>
            ))}
          </div>

          <div className="min-w-0 self-start lg:sticky lg:top-32">
            <div className="overflow-hidden border border-foreground/10 bg-background/40">
              <div className="flex items-center justify-between border-b border-foreground/10 px-6 py-4">
                <div className="flex gap-2">
                  <div className="h-3 w-3 rounded-full bg-foreground/20" />
                  <div className="h-3 w-3 rounded-full bg-foreground/20" />
                  <div className="h-3 w-3 rounded-full bg-foreground/20" />
                </div>
                <span className="font-mono text-xs text-muted-foreground">{active.file}</span>
              </div>

              <div className="min-h-[280px] overflow-x-auto p-8 font-mono text-sm">
                <RevealedCode code={active.code} revealKey={activeStep} lineNumbers />
              </div>

              <div className="flex items-center gap-3 border-t border-foreground/10 px-6 py-4">
                <span className="h-2 w-2 rounded-full bg-accent" />
                <span className="font-mono text-xs text-muted-foreground">{active.state}</span>
              </div>
            </div>
          </div>
        </div>
      </div>

      <style>{`
        @keyframes hiw-progress {
          from { width: 0%; }
          to { width: 100%; }
        }
      `}</style>
    </section>
  );
}
