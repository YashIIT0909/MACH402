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
        Fill this in and run the command it produces on the machine with the card — no need to clone
        anything first, it fetches what it needs. It builds the node daemon, checks Docker and the
        GPU, and starts announcing itself to the registry, then drops you into a live dashboard.
        Renters then pay your node directly — no key of yours ever leaves that machine, because the
        node never holds one.
      </p>

      <InstallCommand registryUrl={REGISTRY_URL} />

      <h2>Two ways to sell, and they are different asks</h2>
      <p>
        Batch jobs are the default: a renter sends a script, it runs in a container with no network
        at all, and they get the output back. Nothing of yours is reachable and nothing they send
        can phone home.
      </p>
      <p>
        Interactive leases are the opt-in above, and they invert that. Nothing is uploaded — a
        renter&apos;s code and data stay on their own machine — and instead they get a shell and a
        Jupyter server in a container on yours, for the minutes they paid for. That is a bigger
        thing to agree to, so it is its own checkbox and its own price, never something that comes
        along with ticking the GPU box.
      </p>
      <p>
        What a lease does not expose: your filesystem, your other containers, your Docker socket, or
        your network. The container gets a throwaway workspace that is wiped when the lease ends,
        and its only route off your machine is a proxy that allows package and model registries and
        denies everything else. Nobody gets in without a certificate your machine signed, and those
        certificates expire when the paid time does — the signing key never leaves your box, the
        same way no key of yours ever does.
      </p>

      <h2>What you need first</h2>
      <p>
        Docker running and your user in the <code>docker</code> group, Go 1.25 and git to build the
        binary, and a Hedera testnet account id from{" "}
        <a href="https://portal.hedera.com">portal.hedera.com</a> to be paid into. For an actual GPU
        you also need the NVIDIA Container Toolkit — without it the node still runs, in CPU-fallback
        mode, and says so in its listing.
      </p>
      <p>
        If you ticked interactive leases, you also need <code>ssh-keygen</code> (any openssh client
        package) and <code>cloudflared</code>, which the installer fetches for you. The first
        install then builds the lease image, which takes a while and only happens once.
      </p>
    </>
  );
}
