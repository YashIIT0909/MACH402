"use client";

import { useEffect, useState } from "react";

import { CONTAINER } from "./layout";
import { Eyebrow, RevealedCode, useReveal } from "./primitives";

const steps = [
  {
    number: "I",
    title: "The renter asks for work",
    description:
      "A job spec goes to the node: an image from its allowlist, a script, optionally a dataset URL. Anything knowable in advance is checked now — the image, the GPU requirement, a HEAD on the dataset — so a rejection costs the renter nothing.",
    file: "job.http",
    state: "402 issued",
    code: `POST /v1/jobs

{ "image": "python:3.11-slim",
  "script": { "filename": "train.py" },
  "require_gpu": true }

402 Payment Required
PAYMENT-REQUIRED: <base64>`,
  },
  {
    number: "II",
    title: "The renter signs",
    description:
      "A Hedera transfer is signed locally and the request is retried. The facilitator co-signs as fee payer, so the renter spends no gas — the property that makes this work for agents.",
    file: "pay.ts",
    state: "signed",
    code: `const payload = await signExact({
  amount:   "100000",
  asset:    "0.0.0",
  payTo:    "0.0.1234",
  feePayer: supported.extra.feePayer
})

PAYMENT-SIGNATURE: <base64>`,
  },
  {
    number: "III",
    title: "The node verifies, then settles",
    description:
      "In that order, and immediately. A signed payment expires in 300 seconds and a training run outlives that many times over, so settlement happens when the job is accepted, never when it finishes. Slow work — pulling an image, downloading a dataset — happens after, in staging.",
    file: "settle.http",
    state: "settled",
    code: `POST /verify -> { isValid: true }

POST /settle -> {
  success: true,
  transaction:
    "0.0.802@1730000000.1"
}`,
  },
  {
    number: "IV",
    title: "The work runs sealed",
    description:
      "No network at all, a read-only root filesystem, capped memory, CPU and runtime. A dataset URL is fetched by the node and mounted at /data; the container itself can never reach out.",
    file: "sandbox.sh",
    state: "running",
    code: `docker run \\
  --network=none \\
  --read-only \\
  --memory 8g --cpus 4 \\
  --gpus all \\
  -v ./data:/data:ro \\
  python:3.11-slim`,
  },
];

const ROTATE_MS = 6000;

export function HowItWorksSection() {
  const [activeStep, setActiveStep] = useState(0);
  const { ref, revealed } = useReveal();

  // Rotation starts only once the section is on screen, so a reader arriving
  // late does not land mid-cycle on a step they never saw begin.
  useEffect(() => {
    if (!revealed) return;
    const interval = setInterval(() => {
      setActiveStep((prev) => (prev + 1) % steps.length);
    }, ROTATE_MS);
    return () => clearInterval(interval);
  }, [revealed]);

  const active = steps[activeStep];

  return (
    <section
      id="how-it-works"
      ref={ref}
      className="relative overflow-hidden border-y border-foreground/10 bg-panel py-24 lg:py-32"
    >
      <div className="hatch pointer-events-none absolute inset-0 opacity-[0.035]" />

      <div className={`relative z-10 ${CONTAINER}`}>
        <div className="mb-16 lg:mb-24">
          <Eyebrow className="mb-6">How a job is paid for</Eyebrow>
          <h2
            className={`font-display text-4xl tracking-tight transition-all duration-700 lg:text-6xl ${
              revealed ? "translate-y-0 opacity-100" : "translate-y-4 opacity-0"
            }`}
          >
            Four steps, one of which
            <br />
            <span className="text-muted-foreground">moves money.</span>
          </h2>
        </div>

        <div className="grid gap-16 lg:grid-cols-2 lg:gap-24">
          <div className="space-y-0">
            {steps.map((step, index) => (
              <button
                key={step.number}
                type="button"
                onClick={() => setActiveStep(index)}
                className={`group w-full border-b border-foreground/10 py-8 text-left transition-all duration-500 ${
                  activeStep === index ? "opacity-100" : "opacity-40 hover:opacity-70"
                }`}
              >
                <div className="flex items-start gap-6">
                  <span
                    className={`font-display text-3xl transition-colors duration-500 ${
                      activeStep === index ? "text-accent" : "text-foreground/30"
                    }`}
                  >
                    {step.number}
                  </span>
                  <div className="flex-1">
                    <h3 className="mb-3 font-display text-2xl transition-transform duration-300 group-hover:translate-x-2 lg:text-3xl">
                      {step.title}
                    </h3>
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
              </button>
            ))}
          </div>

          <div className="self-start lg:sticky lg:top-32">
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
