"use client";

import { KeyRound, Unplug, Boxes, Route } from "lucide-react";

import { CONTAINER } from "./layout";
import { Eyebrow, useReveal } from "./primitives";

/*
 * The invariants, as the repo states them. Each of these is enforced in code
 * and covered by a test; weakening any of them is a bug, not a simplification.
 */
const invariants = [
  {
    icon: KeyRound,
    title: "The node holds no key",
    description:
      "A provider supplies an account id to be paid into, never a private key, and the daemon links no Hedera SDK at all. All signing lives on the renter's side.",
  },
  {
    icon: Unplug,
    title: "No network in the container",
    description:
      "NetworkMode is none, capabilities are dropped, the root filesystem is read only. Not even for datasets — the node downloads those and mounts them at /data.",
  },
  {
    icon: Boxes,
    title: "Allowlisted images only",
    description:
      "A node runs the images it has agreed to run and nothing else. The allowlist is checked before the 402, so a rejected job costs the renter nothing.",
  },
  {
    icon: Route,
    title: "Dataset fetches are hardened",
    description:
      "A renter's URL is fetched from inside the provider's network, so loopback, link-local, private and CGNAT addresses are refused — and re-checked on every redirect hop.",
  },
];

/** Real flags from `runner.go` and `fetch.go`, not compliance badges. */
const flags = [
  "--network=none",
  "ReadonlyRootfs",
  "CapDrop: ALL",
  "no-new-privileges",
  "PidsLimit",
  "NanoCpus",
  "wall-clock timeout",
  "symlink refusal",
];

export function SecuritySection() {
  const { ref, revealed } = useReveal();

  return (
    <section
      id="security"
      ref={ref}
      className="relative overflow-hidden bg-foreground/[0.02] py-24 lg:py-32"
    >
      <div className={CONTAINER}>
        <div className="grid gap-16 lg:grid-cols-2 lg:gap-24">
          <div
            className={`transition-all duration-700 ${
              revealed ? "translate-y-0 opacity-100" : "translate-y-8 opacity-0"
            }`}
          >
            <Eyebrow className="mb-6">Sandbox and custody</Eyebrow>
            <h2 className="mb-8 font-display text-4xl tracking-tight lg:text-6xl">
              These are invariants,
              <br />
              <span className="text-muted-foreground">not preferences.</span>
            </h2>
            <p className="mb-12 text-xl leading-relaxed text-muted-foreground">
              Untrusted code from a stranger runs on a machine in someone&apos;s house, paid for by
              a different stranger. Every rule below is enforced in code and covered by a test.
              Weakening one is a vulnerability, not a simplification.
            </p>

            <div className="flex flex-wrap gap-3">
              {flags.map((flag, index) => (
                <span
                  key={flag}
                  className={`border border-foreground/10 px-4 py-2 font-mono text-sm text-muted-foreground transition-all duration-500 ${
                    revealed ? "translate-y-0 opacity-100" : "translate-y-4 opacity-0"
                  }`}
                  style={{ transitionDelay: `${index * 50 + 200}ms` }}
                >
                  {flag}
                </span>
              ))}
            </div>
          </div>

          <div className="grid gap-6">
            {invariants.map((invariant, index) => (
              <div
                key={invariant.title}
                className={`group border border-foreground/10 p-6 transition-all duration-500 hover:border-accent/40 ${
                  revealed ? "translate-x-0 opacity-100" : "translate-x-8 opacity-0"
                }`}
                style={{ transitionDelay: `${index * 100}ms` }}
              >
                <div className="flex items-start gap-4">
                  <div className="flex h-10 w-10 shrink-0 items-center justify-center border border-foreground/10 transition-colors duration-300 group-hover:bg-accent group-hover:text-accent-foreground">
                    <invariant.icon className="h-5 w-5" />
                  </div>
                  <div>
                    <h3 className="mb-1 text-lg font-medium transition-transform duration-300 group-hover:translate-x-1">
                      {invariant.title}
                    </h3>
                    <p className="text-muted-foreground">{invariant.description}</p>
                  </div>
                </div>
              </div>
            ))}
          </div>
        </div>
      </div>
    </section>
  );
}
