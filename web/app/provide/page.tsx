import { REGISTRY_URL } from "@/lib/registry";
import { PageHero } from "@/components/landing/page-hero";
import { CONTAINER } from "@/components/landing/layout";
import { Eyebrow } from "@/components/landing/primitives";

import { InstallCommand } from "./install-command";

// The command embeds the registry's address, so it has to be read when the page
// is served rather than baked in at build time.
export const dynamic = "force-dynamic";

const requirements = [
  {
    number: "01",
    title: "Docker, and your user in the docker group",
    body: "The daemon talks to Docker over its unix socket to create the sandbox each job runs in. Setup preflights this, so a permissions problem surfaces now rather than during someone's paid job.",
  },
  {
    number: "02",
    title: "Go 1.25 and git to build the binary",
    body: "The node ships as one static binary with no Hedera SDK linked, because the merchant side of x402 needs only JSON and HTTP calls to a facilitator. The installer clones the repo itself if you have no checkout.",
  },
  {
    number: "03",
    title: "A Hedera testnet account to be paid into",
    body: null,
  },
  {
    number: "04",
    title: "The NVIDIA Container Toolkit, for an actual GPU",
    body: "Without it the node still runs, in CPU-fallback mode, and says so in its listing. A card the daemon cannot pass through is a card you cannot sell, so it is never advertised as one.",
  },
];

export default function ProvidePage() {
  return (
    <>
      <PageHero
        eyebrow="For providers"
        title={
          <>
            List your GPU.
            <br />
            Keep your keys.
          </>
        }
      >
        Fill this in and run the command it produces on the machine with the card — nothing to
        clone first, it fetches what it needs. It builds the node daemon, checks Docker and the
        GPU, starts announcing itself, then drops you into a live dashboard. Renters pay your node
        directly.
      </PageHero>

      <section className="py-16 lg:py-24">
        <div className={CONTAINER}>
          <InstallCommand registryUrl={REGISTRY_URL} />
        </div>
      </section>

      <section className="border-t border-foreground/10 py-16 lg:py-24">
        <div className={CONTAINER}>
          <Eyebrow className="mb-6">Before you run it</Eyebrow>
          <h2 className="mb-12 type-title">
            What the machine
            <br />
            <span className="text-muted-foreground">needs.</span>
          </h2>

          <ol className="border-t border-foreground/10">
            {requirements.map((requirement) => (
              <li
                key={requirement.number}
                className="grid grid-cols-[40px_1fr] gap-4 border-b border-foreground/10 py-7 lg:grid-cols-[64px_1fr] lg:gap-6"
              >
                <span className="pt-1 font-mono text-sm text-accent">
                  {requirement.number}
                </span>
                <div>
                  <h3 className="mb-2 type-heading">{requirement.title}</h3>
                  <p className="max-w-2xl text-muted-foreground">
                    {requirement.body ?? (
                      <>
                        An account <em>id</em> only — never a private key. Get one at{" "}
                        <a
                          href="https://portal.hedera.com"
                          className="text-foreground underline-offset-4 hover:text-accent hover:underline"
                        >
                          portal.hedera.com
                        </a>
                        .
                      </>
                    )}
                  </p>
                </div>
              </li>
            ))}
          </ol>
        </div>
      </section>

    </>
  );
}
