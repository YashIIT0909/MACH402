"use client";

import { CONTAINER } from "./layout";
import { Eyebrow, useSectionReveal } from "./primitives";

const features = [
  {
    number: "01",
    title: "Paid only once it answers",
    description:
      "The node answers 402 with what a chunk of time costs, verifies the signed transfer, starts the container, proves it is reachable, and settles — in that order, inside the 300 seconds a signed payment lives. A session that never came up costs nothing, and whatever credit is left when it stops comes back.",
    visual: "settle",
  },
  {
    number: "02",
    title: "Earnings need no key",
    description:
      "A provider gives the daemon an account id to be paid into, never its key, and the binary links no Hedera SDK at all. The only key on the machine is a separate operator key that pays refunds and topic fees — a compromise costs that float, never the earnings.",
    visual: "nokey",
  },
  {
    number: "03",
    title: "The container is fenced in",
    description:
      "A session runs on an internal network with no route off the host except a proxy that allows package and model registries and refuses the rest. Every capability is dropped but the few sshd needs, and the workspace is wiped when the session ends.",
    visual: "sandbox",
  },
  {
    number: "04",
    title: "Nodes announce themselves",
    description:
      "A provider's box is usually behind NAT, so discovery is push, not poll. The node heartbeats every 30 seconds and goes offline 90 seconds after it stops — or the instant it says so on shutdown. A registry that is down never interrupts a paid session.",
    visual: "heartbeat",
  },
];

/** Renter pays, node accepts, transaction id comes back. */
function SettleVisual() {
  return (
    <svg viewBox="0 0 200 160" className="h-full w-full font-mono" aria-hidden="true">
      <rect x="12" y="52" width="52" height="52" rx="4" fill="none" stroke="currentColor" strokeWidth="2" />
      <text x="38" y="82" textAnchor="middle" fontSize="9" fill="currentColor">
        renter
      </text>

      <rect x="136" y="52" width="52" height="52" rx="4" fill="none" stroke="currentColor" strokeWidth="2" />
      <text x="162" y="82" textAnchor="middle" fontSize="9" fill="currentColor">
        node
      </text>

      {/* 402 goes back, payment goes forward. */}
      <path id="settlePath" d="M 64 78 L 136 78" fill="none" />
      <line x1="64" y1="78" x2="136" y2="78" stroke="currentColor" strokeWidth="1.5" strokeDasharray="4 4" opacity="0.4" />
      <circle r="4" fill="currentColor">
        <animateMotion dur="2s" repeatCount="indefinite">
          <mpath href="#settlePath" />
        </animateMotion>
      </circle>

      <text x="100" y="38" textAnchor="middle" fontSize="11" fill="currentColor" opacity="0.35">
        402
        <animate attributeName="opacity" values="0.8;0.15;0.15" dur="2s" repeatCount="indefinite" />
      </text>

      <line x1="136" y1="96" x2="64" y2="96" stroke="currentColor" strokeWidth="1" opacity="0.2" />

      <text x="100" y="132" textAnchor="middle" fontSize="9" fill="currentColor" opacity="0">
        0.0.x@1730.0
        <animate attributeName="opacity" values="0;0;0.7;0.7" dur="2s" repeatCount="indefinite" />
      </text>
    </svg>
  );
}

/** A key that approaches the node and is turned away every time. */
function NoKeyVisual() {
  return (
    <svg viewBox="0 0 200 160" className="h-full w-full font-mono" aria-hidden="true">
      <rect x="98" y="42" width="76" height="76" rx="4" fill="none" stroke="currentColor" strokeWidth="2" />
      <text x="136" y="136" textAnchor="middle" fontSize="9" fill="currentColor" opacity="0.6">
        node
      </text>

      {/* An empty keyhole: the slot exists, nothing is ever in it. */}
      <circle cx="136" cy="74" r="7" fill="none" stroke="currentColor" strokeWidth="2" opacity="0.35" />
      <path d="M 133 80 L 139 80 L 137.5 92 L 134.5 92 Z" fill="none" stroke="currentColor" strokeWidth="2" opacity="0.35" />

      {/* The key never arrives. */}
      <g>
        <animateTransform
          attributeName="transform"
          type="translate"
          values="0 0; 34 0; 0 0"
          keyTimes="0; 0.55; 1"
          dur="3s"
          repeatCount="indefinite"
        />
        <circle cx="34" cy="80" r="9" fill="none" stroke="currentColor" strokeWidth="2.5" />
        <line x1="43" y1="80" x2="66" y2="80" stroke="currentColor" strokeWidth="2.5" />
        <line x1="60" y1="80" x2="60" y2="87" stroke="currentColor" strokeWidth="2.5" />
        <line x1="66" y1="80" x2="66" y2="88" stroke="currentColor" strokeWidth="2.5" />
      </g>

      {/* The barrier it bounces off. */}
      <line x1="90" y1="34" x2="90" y2="126" stroke="currentColor" strokeWidth="1.5" strokeDasharray="3 5" opacity="0.5" />
      <g opacity="0">
        <animate attributeName="opacity" values="0;0;0.9;0" keyTimes="0;0.45;0.6;0.75" dur="3s" repeatCount="indefinite" />
        <line x1="83" y1="73" x2="97" y2="87" stroke="currentColor" strokeWidth="2.5" />
        <line x1="97" y1="73" x2="83" y2="87" stroke="currentColor" strokeWidth="2.5" />
      </g>
    </svg>
  );
}

/** A container with no direct route out — only the egress proxy below it. */
function SandboxVisual() {
  return (
    <svg viewBox="0 0 200 160" className="h-full w-full font-mono" aria-hidden="true">
      <rect x="52" y="26" width="96" height="76" rx="4" fill="none" stroke="currentColor" strokeWidth="2" />

      {/* Work happening inside. */}
      {[0, 1, 2].map((i) => (
        <rect key={i} x="66" y={44 + i * 16} width="68" height="7" rx="2" fill="currentColor" opacity="0.15">
          <animate
            attributeName="width"
            values="14;68;14"
            dur="2.4s"
            begin={`${i * 0.25}s`}
            repeatCount="indefinite"
          />
          <animate
            attributeName="opacity"
            values="0.15;0.7;0.15"
            dur="2.4s"
            begin={`${i * 0.25}s`}
            repeatCount="indefinite"
          />
        </rect>
      ))}

      {/* The severed network leg. */}
      <line x1="8" y1="64" x2="34" y2="64" stroke="currentColor" strokeWidth="2" opacity="0.45" />
      <line x1="52" y1="64" x2="44" y2="64" stroke="currentColor" strokeWidth="2" opacity="0.45" />
      <g>
        <line x1="34" y1="57" x2="44" y2="71" stroke="currentColor" strokeWidth="2.5">
          <animate attributeName="opacity" values="0.4;1;0.4" dur="2s" repeatCount="indefinite" />
        </line>
        <line x1="44" y1="57" x2="34" y2="71" stroke="currentColor" strokeWidth="2.5">
          <animate attributeName="opacity" values="0.4;1;0.4" dur="2s" repeatCount="indefinite" />
        </line>
      </g>
      <text x="21" y="84" textAnchor="middle" fontSize="7.5" fill="currentColor" opacity="0.55">
        net
      </text>

      {/* The only way out: the deny-by-default egress proxy. */}
      <line x1="100" y1="102" x2="100" y2="120" stroke="currentColor" strokeWidth="2" opacity="0.5" />
      <rect x="70" y="120" width="60" height="24" rx="3" fill="none" stroke="currentColor" strokeWidth="2" />
      <text x="100" y="136" textAnchor="middle" fontSize="10" fill="currentColor">
        proxy
      </text>
    </svg>
  );
}

/** A beat travelling from node to registry, and the window it has to arrive in. */
function HeartbeatVisual() {
  return (
    <svg viewBox="0 0 200 160" className="h-full w-full font-mono" aria-hidden="true">
      <rect x="10" y="58" width="44" height="44" rx="4" fill="none" stroke="currentColor" strokeWidth="2" />
      <text x="32" y="118" textAnchor="middle" fontSize="8" fill="currentColor" opacity="0.6">
        node
      </text>

      <rect x="146" y="58" width="44" height="44" rx="4" fill="none" stroke="currentColor" strokeWidth="2" />
      <text x="168" y="118" textAnchor="middle" fontSize="8" fill="currentColor" opacity="0.6">
        registry
      </text>

      {/* The trace, drawn as one pulse crossing the gap. */}
      <path
        d="M 54 80 L 72 80 L 78 62 L 88 98 L 96 80 L 146 80"
        fill="none"
        stroke="currentColor"
        strokeWidth="2"
        opacity="0.25"
      />
      <path
        d="M 54 80 L 72 80 L 78 62 L 88 98 L 96 80 L 146 80"
        fill="none"
        stroke="currentColor"
        strokeWidth="2"
        strokeDasharray="30 170"
      >
        <animate attributeName="stroke-dashoffset" values="200;0" dur="2s" repeatCount="indefinite" />
      </path>

      <text x="100" y="40" textAnchor="middle" fontSize="9" fill="currentColor" opacity="0.5">
        every 30s
      </text>

      <circle cx="168" cy="80" r="4" fill="currentColor" opacity="0.3">
        <animate attributeName="opacity" values="0.3;1;0.3" dur="2s" repeatCount="indefinite" />
      </circle>
    </svg>
  );
}

function AnimatedVisual({ type }: { type: string }) {
  switch (type) {
    case "settle":
      return <SettleVisual />;
    case "nokey":
      return <NoKeyVisual />;
    case "sandbox":
      return <SandboxVisual />;
    case "heartbeat":
      return <HeartbeatVisual />;
    default:
      return null;
  }
}

function FeatureCard({ feature }: { feature: (typeof features)[number] }) {
  return (
    <div data-reveal className="group relative">
      {/*
       * The numeral and the title share one line and one size, so they read as
       * a single label rather than a small tag floating beside a large heading
       * — the same pairing the how-it-works steps use. Text is top-aligned so
       * that pairing holds whatever the diagram's height; only the diagram
       * centres itself.
       */}
      <div className="grid grid-cols-[minmax(0,1fr)] gap-8 border-b border-foreground/10 py-10 lg:grid-cols-[minmax(0,1fr)_auto] lg:items-center lg:gap-16 lg:py-14">
        {/* Two Ticketing advances at subtitle size (0.9 × --tk); `2ch` here would measure the body font. */}
        <div className="grid min-w-0 grid-cols-[calc(var(--tk)*0.9)_minmax(0,1fr)] items-baseline gap-x-4 sm:gap-x-6">
          <span className="type-subtitle text-accent">{feature.number}</span>
          <h3 className="type-subtitle transition-transform ease-brand dur-slow group-hover:translate-x-2">
            {feature.title}
          </h3>
          {/* Spans the row on a phone rather than sitting indented behind the numeral. */}
          <p className="col-span-2 mt-3 max-w-[40rem] text-muted-foreground sm:col-span-1 sm:col-start-2">
            {feature.description}
          </p>
        </div>

        <div className="flex justify-center lg:justify-end">
          <div className="h-40 w-52 text-foreground/80 transition-colors ease-brand dur-slow group-hover:text-accent">
            <AnimatedVisual type={feature.visual} />
          </div>
        </div>
      </div>
    </div>
  );
}

export function FeaturesSection() {
  const ref = useSectionReveal<HTMLElement>();

  return (
    <section id="features" ref={ref} className="relative py-16 lg:py-24">
      <div className={CONTAINER}>
        <div className="mb-10 lg:mb-14">
          <Eyebrow className="mb-6">Capabilities</Eyebrow>
          <h2
            data-reveal className="type-title"
          >
            Everything it does.
            <br />
            <span className="text-muted-foreground">Nothing it holds.</span>
          </h2>
        </div>

        <div>
          {features.map((feature) => (
            <FeatureCard key={feature.number} feature={feature} />
          ))}
        </div>
      </div>
    </section>
  );
}
