"use client";

import { useState } from "react";
import { Check, Copy } from "lucide-react";

import { Button } from "@/components/ui/button";
import { CONTAINER } from "./layout";
import { Eyebrow, RevealedCode, useSectionReveal } from "./primitives";

/*
 * What an agent actually touches: three plain HTTP calls, no client to install.
 * Field names are the node's real ones — `/v1/specs` is `nodespec.Spec`, the
 * card is `httpapi.agentCard`, the challenge is the x402 v2 shape in CLAUDE.md.
 */
const examples = [
  {
    label: "Discover",
    code: `$ curl -s https://gpu.example/v1/specs

{
  "node_id": "node_a1b2c3",
  "agent_version": "0.1.0",
  "pay_to": "0.0.1234",
  "network": "hedera:testnet",
  "asset": "0.0.0",
  "gpu": { "available": true, "model": "NVIDIA RTX 4090", "vram_mb": 24576 },
  "leases": { "payment_mode": "session", "chunk_seconds": 300,
              "price_tinybars_per_second": "3334", "jupyter": true },
  ...
}`,
  },
  {
    label: "Challenge",
    code: `$ curl -si -X POST https://gpu.example/v1/sessions \\
    -H 'Content-Type: application/json' \\
    -d '{"seconds":300,"public_key":"ssh-ed25519 AAAA…"}'

HTTP/1.1 402 Payment Required
PAYMENT-REQUIRED: <base64 PaymentRequired>

# decoded accepts[0]: what one signature buys
{ "scheme": "exact", "network": "hedera:testnet",
  "amount": "1000200", "asset": "0.0.0",
  "payTo": "0.0.1234", "maxTimeoutSeconds": 300 }`,
  },
  {
    label: "Identity",
    code: `$ curl -s https://gpu.example/.well-known/agent-card.json

{
  "url": "https://gpu.example",
  "capabilities": { "cuda": true, "docker": true, "ssh": false, "jupyter": true },
  "payments": { "scheme": "x402", "network": "hedera:testnet",
                "asset": "0.0.0", "payTo": "0.0.1234", "refundable": true },
  ...
}`,
  },
];

const traits = [
  {
    title: "Discovery is free",
    description: "GET /v1/specs costs nothing. Discovery that costs money is discovery an agent cannot do.",
  },
  {
    title: "No API keys",
    description: "The payment is the authentication. There is no account to create and nothing to revoke.",
  },
  {
    title: "Versioned endpoints",
    description: "Everything lives under /v1 and old versions keep working. Nodes update on their own schedule.",
  },
  {
    title: "One static binary",
    description: "The node ships as a single Go binary that talks to Docker over its socket. No SDKs linked.",
  },
];

export function DevelopersSection() {
  const [activeTab, setActiveTab] = useState(0);
  const [copied, setCopied] = useState(false);
  const ref = useSectionReveal<HTMLElement>();

  async function copy() {
    await navigator.clipboard.writeText(examples[activeTab].code);
    setCopied(true);
    setTimeout(() => setCopied(false), 2000);
  }

  return (
    <section id="developers" ref={ref} className="relative overflow-hidden py-16 lg:py-24">
      <div className={CONTAINER}>
        {/*
         * `minmax(0, 1fr)`, not the implicit `auto`: a grid track sizes to its
         * widest unbreakable content, and the code panel's longest line made
         * the single phone column 582px wide on a 390px screen — cutting the
         * title and lede off at the edge.
         */}
        <div className="grid grid-cols-[minmax(0,1fr)] items-start gap-16 lg:grid-cols-2 lg:gap-24">
          <div
            data-reveal
            className="min-w-0"
          >
            <Eyebrow className="mb-6">For developers</Eyebrow>
            <h2 className="mb-8 type-title">
              Built for agents.
              <br />
              <span className="text-muted-foreground">Usable by people.</span>
            </h2>
            <p className="mb-12 type-lede text-muted-foreground">
              An autonomous buyer cannot sign up, cannot hold an API key, and cannot pay for a quote
              it has not decided to accept yet. Everything here follows from that: free discovery, a
              402 that says exactly what it wants, and one signature.
            </p>

            <div className="grid gap-6 sm:grid-cols-2">
              {/*
               * Not `data-reveal` targets of their own. They sit inside the left
               * column, which already reveals as one unit and carries them in.
               * As nested targets of the same tween they were left at its start
               * state — opacity 0, 24px down — on every load, so the four
               * traits never appeared at all. The inline `transitionDelay` was a
               * leftover from a CSS-transition reveal GSAP replaced.
               */}
              {traits.map((trait) => (
                <div key={trait.title}>
                  <h3 className="mb-1 font-medium">{trait.title}</h3>
                  <p className="text-sm text-muted-foreground">{trait.description}</p>
                </div>
              ))}
            </div>
          </div>

          <div
            data-reveal className="min-w-0 lg:sticky lg:top-32"
          >
            <div className="border border-foreground/10">
              <div className="flex items-center border-b border-foreground/10">
                {examples.map((example, index) => (
                  <Button
                    key={example.label}
                    variant="quiet"
                    shape="square"
                    onClick={() => setActiveTab(index)}
                    aria-pressed={activeTab === index}
                    className={`relative h-auto px-6 py-4 font-mono ${
                      activeTab === index ? "text-accent" : ""
                    }`}
                  >
                    {example.label}
                    {activeTab === index ? (
                      <span className="absolute right-0 bottom-0 left-0 h-px bg-accent" />
                    ) : null}
                  </Button>
                ))}
                <div className="flex-1" />
                <Button
                  variant="quiet"
                  shape="square"
                  onClick={() => void copy()}
                  className="h-auto px-4 py-4"
                  aria-label="Copy this snippet"
                >
                  {copied ? <Check className="text-accent" /> : <Copy />}
                </Button>
              </div>

              <div className="min-h-[240px] overflow-x-auto bg-foreground/[0.02] p-8 font-mono text-sm">
                <RevealedCode code={examples[activeTab].code} revealKey={activeTab} />
              </div>
            </div>

            <div className="mt-6 flex items-center gap-6 text-sm">
              <a
                href="https://docs.hedera.com/solutions/ai/x402"
                className="text-foreground underline-offset-4 hover:text-accent hover:underline"
              >
                x402 on Hedera
              </a>
              <span className="text-foreground/20">|</span>
              <a
                href="https://blocky402.com/docs/"
                className="text-muted-foreground transition-colors hover:text-foreground"
              >
                Facilitator docs
              </a>
            </div>
          </div>
        </div>
      </div>
    </section>
  );
}
