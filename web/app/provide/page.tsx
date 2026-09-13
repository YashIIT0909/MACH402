import { REGISTRY_URL } from "@/lib/registry";
import { PageHero } from "@/components/landing/page-hero";
import { CONTAINER } from "@/components/landing/layout";

import { InstallCommand } from "./install-command";
import { SetupGuide } from "./setup-guide";

// The command embeds the registry's address, so it has to be read when the page
// is served rather than baked in at build time.
export const dynamic = "force-dynamic";

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
        One command, run on the machine with the GPU, fetches everything it needs: it builds the
        node, checks Docker and the GPU, lists the node, and opens a live dashboard. Renters pay
        the node directly.
      </PageHero>

      <section className="py-16 lg:py-24">
        <div className={CONTAINER}>
          <InstallCommand registryUrl={REGISTRY_URL} />
          <SetupGuide />
        </div>
      </section>
    </>
  );
}
