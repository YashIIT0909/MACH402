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
          <Eyebrow className="mb-6">Two products</Eyebrow>
          <h2 className="mb-12 font-display text-4xl tracking-tight lg:text-5xl">
            Two ways to sell,
            <br />
            <span className="text-muted-foreground">and they are different asks.</span>
          </h2>

          <div className="grid gap-px bg-foreground/10 md:grid-cols-2">
            <div className="bg-background p-8 lg:p-10">
              <span className="mb-4 block font-mono text-xs tracking-widest text-muted-foreground uppercase">
                Batch jobs · on by default
              </span>
              <p className="text-muted-foreground">
                A renter sends a script, it runs in a container with no network at all, and they
                get the output back. Nothing of yours is reachable and nothing they send can phone
                home.
              </p>
            </div>

            <div className="bg-background p-8 lg:p-10">
              <span className="mb-4 block font-mono text-xs tracking-widest text-accent uppercase">
                Interactive leases · opt in
              </span>
              <p className="mb-4 text-muted-foreground">
                This inverts it. Nothing is uploaded — a renter&apos;s code and data stay on their
                own machine — and instead they get a shell and a Jupyter server in a container on
                yours, for the minutes they paid for. That is a bigger thing to agree to, so it is
                its own checkbox and its own price, never something that arrives with the GPU box.
              </p>
              <p className="text-muted-foreground">
                What a lease does not expose: your filesystem, your other containers, your Docker
                socket, or your network. The container gets a throwaway workspace wiped when the
                lease ends, and its only route off your machine is a proxy that allows package and
                model registries and denies everything else. Nobody gets in without a certificate
                your machine signed, and those expire when the paid time does — the signing key
                never leaves your box, the same way no Hedera key of yours ever does.
              </p>
            </div>
          </div>
        </div>
      </section>

      <section className="border-t border-foreground/10 py-16 lg:py-24">
        <div className={CONTAINER}>
          <Eyebrow className="mb-6">Before you run it</Eyebrow>
          <h2 className="mb-12 font-display text-4xl tracking-tight lg:text-5xl">
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
                <span className="pt-1 font-mono text-sm text-muted-foreground">
                  {requirement.number}
                </span>
                <div>
                  <h3 className="mb-2 text-lg font-medium">{requirement.title}</h3>
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

      <section className="pb-24 lg:pb-32">
        <div className={CONTAINER}>
          <div className="border border-foreground/10 bg-foreground/[0.02] p-8 lg:p-12">
            <span className="mb-4 block font-mono text-xs tracking-widest text-accent uppercase">
              The node holds no key
            </span>
            <p className="mb-4 max-w-2xl text-lg text-muted-foreground">
              Nothing on this page is submitted anywhere. The registry learns about your node when
              that node first heartbeats, not when someone fills in a form — so there is no account
              to create, and no way to list a machine you do not control.
            </p>
            <p className="max-w-2xl text-lg text-muted-foreground">
              Your daemon receives payment; it never signs. Every settlement is written to an
              append-only <code className="font-mono text-foreground">receipts.jsonl</code> on your
              own disk, so you can audit earnings without trusting this website.
            </p>
          </div>
        </div>
      </section>
    </>
  );
}
