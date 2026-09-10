import Link from "next/link";

export default function HomePage() {
  return (
    <>
      <h1>ClearGate</h1>
      <p>
        A GPU rental marketplace settled with x402 payments on Hedera testnet. Providers run a daemon
        on an idle GPU; renters pay that machine directly, per job, and get a container run on it.
      </p>

      <h2>Have a GPU</h2>
      <p>
        <Link href="/provide">Get your install command</Link> — run it on the machine with the card,
        and it appears in the list below within a few seconds.
      </p>

      <h2>Need a GPU</h2>
      <p>
        <Link href="/nodes">Browse listed nodes</Link>, then rent one with the{" "}
        <code>cleargate</code> CLI. Payment goes renter to node, direct: this site never touches the
        money.
      </p>
    </>
  );
}
