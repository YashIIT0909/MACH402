import { REGISTRY_URL } from "@/lib/registry";

import { InstallCommand } from "./install-command";

// The command embeds the registry's address, so it has to be read when the page
// is served rather than baked in at build time.
export const dynamic = "force-dynamic";

export default function ProvidePage() {
  return (
    <>
      <h1>List your GPU</h1>
      <p>
        Fill this in and run the command it produces on the machine with the card. It builds the node
        daemon, checks Docker and the GPU, and starts announcing itself to the registry. Renters then
        pay your node directly — no key of yours ever leaves that machine, because the node never
        holds one.
      </p>

      <InstallCommand registryUrl={REGISTRY_URL} />

      <h2>What you need first</h2>
      <p>
        Docker running and your user in the <code>docker</code> group, Go 1.25 to build the binary,
        and a Hedera testnet account id from{" "}
        <a href="https://portal.hedera.com">portal.hedera.com</a> to be paid into. For an actual GPU
        you also need the NVIDIA Container Toolkit — without it the node still runs, in CPU-fallback
        mode, and says so in its listing.
      </p>
    </>
  );
}
