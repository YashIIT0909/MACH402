"use client";

import Link from "next/link";
import { ArrowUpRight } from "lucide-react";

import { AnimatedWave } from "./ascii-canvas";
import { CONTAINER } from "./layout";

const footerLinks: Record<
  string,
  { name: string; href: string; external?: boolean; badge?: string }[]
> = {
  Marketplace: [
    { name: "Browse nodes", href: "/nodes" },
    { name: "List your GPU", href: "/provide" },
    { name: "How it works", href: "/#how-it-works" },
    { name: "Pricing", href: "/#pricing" },
  ],
  Developers: [
    { name: "The CLI", href: "/#developers" },
    { name: "Agent API", href: "/#developers" },
    { name: "Sandbox rules", href: "/#security" },
    { name: "The stack", href: "/#stack" },
  ],
  Protocol: [
    { name: "x402 on Hedera", href: "https://docs.hedera.com/solutions/ai/x402", external: true },
    { name: "Blocky402 facilitator", href: "https://blocky402.com/docs/", external: true },
    { name: "HashScan explorer", href: "https://hashscan.io/testnet", external: true },
    { name: "Testnet portal", href: "https://portal.hedera.com", external: true },
  ],
  Network: [
    { name: "Hedera testnet", href: "https://hashscan.io/testnet", external: true, badge: "Only" },
    { name: "Mirror node", href: "https://testnet.mirrornode.hedera.com", external: true },
    { name: "Status", href: "https://status.hedera.com", external: true },
  ],
};

export function FooterSection() {
  return (
    <footer className="relative border-t border-foreground/10">
      <div className="pointer-events-none absolute inset-0 h-64 overflow-hidden opacity-20">
        <AnimatedWave />
      </div>

      <div className={`relative z-10 ${CONTAINER}`}>
        <div className="py-16 lg:py-24">
          <div className="grid grid-cols-2 gap-12 md:grid-cols-6 lg:gap-8">
            <div className="col-span-2">
              <Link href="/" className="mb-6 inline-flex items-center gap-2">
                <span className="font-display text-2xl">ClearGate</span>
                <span className="font-mono text-xs tracking-widest text-muted-foreground uppercase">
                  testnet
                </span>
              </Link>

              <p className="mb-8 max-w-xs leading-relaxed text-muted-foreground">
                Rent an idle GPU by the job, settled with x402 payments on Hedera. Renters pay the
                node directly.
              </p>

              <div className="flex gap-6">
                <a
                  href="https://github.com"
                  className="group flex items-center gap-1 text-sm text-muted-foreground transition-colors hover:text-foreground"
                >
                  GitHub
                  <ArrowUpRight className="h-3 w-3 -translate-x-1 opacity-0 transition-all group-hover:translate-x-0 group-hover:opacity-100" />
                </a>
                <a
                  href="https://hashscan.io/testnet"
                  className="group flex items-center gap-1 text-sm text-muted-foreground transition-colors hover:text-foreground"
                >
                  HashScan
                  <ArrowUpRight className="h-3 w-3 -translate-x-1 opacity-0 transition-all group-hover:translate-x-0 group-hover:opacity-100" />
                </a>
              </div>
            </div>

            {Object.entries(footerLinks).map(([title, links]) => (
              <div key={title}>
                <h3 className="mb-6 text-sm font-medium">{title}</h3>
                <ul className="space-y-4">
                  {links.map((link) => (
                    <li key={link.name}>
                      {link.external ? (
                        <a
                          href={link.href}
                          className="inline-flex items-center gap-2 text-sm text-muted-foreground transition-colors hover:text-foreground"
                        >
                          {link.name}
                          {link.badge ? <Badge>{link.badge}</Badge> : null}
                        </a>
                      ) : (
                        <Link
                          href={link.href}
                          className="inline-flex items-center gap-2 text-sm text-muted-foreground transition-colors hover:text-foreground"
                        >
                          {link.name}
                          {link.badge ? <Badge>{link.badge}</Badge> : null}
                        </Link>
                      )}
                    </li>
                  ))}
                </ul>
              </div>
            ))}
          </div>
        </div>

        <div className="flex flex-col items-center justify-between gap-4 border-t border-foreground/10 py-8 md:flex-row">
          {/* The load-bearing sentence on this whole site. */}
          <p className="text-sm text-muted-foreground">
            Payments settle renter → node. This site never touches the money.
          </p>

          <div className="flex flex-col items-center gap-2 font-mono text-sm text-muted-foreground md:items-end">
            <span className="flex items-center gap-2">
              <span className="h-2 w-2 rounded-full bg-accent" />
              Hedera testnet · not mainnet money
            </span>
            {/*
             * A licence condition, not a courtesy: the hero model is CC-BY-4.0,
             * which requires visible credit naming the work, its author and the
             * licence. Do not remove this without removing the model.
             */}
            <span className="text-xs">
              GPU model{" "}
              <a
                href="https://sketchfab.com/3d-models/geforce-rtx-3080-graphics-card-8b947ee1bf7a4e3d8ffa1c24893ac160"
                className="transition-colors hover:text-foreground"
              >
                “GeForce RTX 3080”
              </a>{" "}
              by{" "}
              <a
                href="https://sketchfab.com/samuelsurovic"
                className="transition-colors hover:text-foreground"
              >
                _surovic_
              </a>
              ,{" "}
              <a
                href="https://creativecommons.org/licenses/by/4.0/"
                className="transition-colors hover:text-foreground"
              >
                CC BY 4.0
              </a>
            </span>
          </div>
        </div>
      </div>
    </footer>
  );
}

function Badge({ children }: { children: React.ReactNode }) {
  return (
    <span className="rounded-full bg-accent px-2 py-0.5 text-xs text-accent-foreground">
      {children}
    </span>
  );
}
