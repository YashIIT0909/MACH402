"use client";

import { useState } from "react";
import { Check, Copy } from "lucide-react";

import { CONTAINER } from "./layout";
import { Eyebrow, RevealedCode, useReveal } from "./primitives";

const examples = [
  {
    label: "Quote",
    code: `$ cleargate quote --node https://gpu.example

node        node_a1b2c3 (agent 0.1.0)
price       0.001 HBAR per job (100000 tinybars)
pay to      0.0.1234
network     hedera:testnet, asset 0.0.0
gpu         NVIDIA RTX 4090 (24576 MB)
limits      3600s, 16384 MB, 8 cores`,
  },
  {
    label: "Run",
    code: `$ cleargate run \\
    --node https://gpu.example \\
    --image python:3.11-slim \\
    --script examples/train.py \\
    --dataset https://example.com/set.tar.gz \\
    --gpu \\
    --budget 200000 \\
    --output result.tar`,
  },
  {
    label: "Keys",
    code: `# The renter is the only party with a key.
# A node never sees it, and it is read
# from the environment, never from disk.

$ export HEDERA_ACCOUNT_ID=0.0.1234
$ export HEDERA_PRIVATE_KEY=302e0201...

$ cleargate spend`,
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
  const { ref, revealed } = useReveal();

  async function copy() {
    await navigator.clipboard.writeText(examples[activeTab].code);
    setCopied(true);
    setTimeout(() => setCopied(false), 2000);
  }

  return (
    <section id="developers" ref={ref} className="relative overflow-hidden py-24 lg:py-32">
      <div className={CONTAINER}>
        <div className="grid items-start gap-16 lg:grid-cols-2 lg:gap-24">
          <div
            className={`transition-all duration-700 ${
              revealed ? "translate-y-0 opacity-100" : "translate-y-8 opacity-0"
            }`}
          >
            <Eyebrow className="mb-6">For developers</Eyebrow>
            <h2 className="mb-8 font-display text-4xl tracking-tight lg:text-6xl">
              Built for agents.
              <br />
              <span className="text-muted-foreground">Usable by people.</span>
            </h2>
            <p className="mb-12 text-xl leading-relaxed text-muted-foreground">
              An autonomous buyer cannot sign up, cannot hold an API key, and cannot pay for a quote
              it has not decided to accept yet. Everything here follows from that: free discovery, a
              402 that says exactly what it wants, and one signature.
            </p>

            <div className="grid grid-cols-2 gap-6">
              {traits.map((trait, index) => (
                <div
                  key={trait.title}
                  className={`transition-all duration-500 ${
                    revealed ? "translate-y-0 opacity-100" : "translate-y-4 opacity-0"
                  }`}
                  style={{ transitionDelay: `${index * 50 + 200}ms` }}
                >
                  <h3 className="mb-1 font-medium">{trait.title}</h3>
                  <p className="text-sm text-muted-foreground">{trait.description}</p>
                </div>
              ))}
            </div>
          </div>

          <div
            className={`transition-all delay-200 duration-700 lg:sticky lg:top-32 ${
              revealed ? "translate-x-0 opacity-100" : "translate-x-8 opacity-0"
            }`}
          >
            <div className="border border-foreground/10">
              <div className="flex items-center border-b border-foreground/10">
                {examples.map((example, index) => (
                  <button
                    key={example.label}
                    type="button"
                    onClick={() => setActiveTab(index)}
                    className={`relative px-6 py-4 font-mono text-sm transition-colors ${
                      activeTab === index
                        ? "text-foreground"
                        : "text-muted-foreground hover:text-foreground"
                    }`}
                  >
                    {example.label}
                    {activeTab === index ? (
                      <span className="absolute right-0 bottom-0 left-0 h-px bg-accent" />
                    ) : null}
                  </button>
                ))}
                <div className="flex-1" />
                <button
                  type="button"
                  onClick={() => void copy()}
                  className="px-4 py-4 text-muted-foreground transition-colors hover:text-foreground"
                  aria-label="Copy this snippet"
                >
                  {copied ? <Check className="h-4 w-4 text-accent" /> : <Copy className="h-4 w-4" />}
                </button>
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
