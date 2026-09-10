"use client";

import { useState } from "react";

/**
 * Builds the provider's install command.
 *
 * Nothing is submitted anywhere: the registry learns about a node when that
 * node first heartbeats, not when someone fills in this form. So there is no
 * account to create, and no way to list a machine you do not control.
 */
export function InstallCommand({ registryUrl }: { registryUrl: string }) {
  const [payTo, setPayTo] = useState("");
  const [price, setPrice] = useState("100000");
  const [publicUrl, setPublicUrl] = useState("http://localhost:8402");
  const [gpu, setGpu] = useState(false);
  const [copied, setCopied] = useState(false);

  const command = [
    `PAY_TO=${payTo === "" ? "0.0.YOUR_ACCOUNT" : payTo}`,
    `PRICE_TINYBARS=${price === "" ? "100000" : price}`,
    `PUBLIC_URL=${publicUrl}`,
    `REGISTRY_URL=${registryUrl}`,
    ...(gpu ? ["GPU=1"] : []),
    "./scripts/install.sh",
  ].join(" \\\n  ");

  async function copy() {
    await navigator.clipboard.writeText(command);
    setCopied(true);
    setTimeout(() => setCopied(false), 1500);
  }

  return (
    <>
      <label>
        Hedera account to be paid into
        <input
          type="text"
          value={payTo}
          onChange={(event) => setPayTo(event.target.value)}
          placeholder="0.0.1234"
        />
      </label>

      <label>
        Price per job, in tinybars (100000 = 0.001 HBAR)
        <input type="text" value={price} onChange={(event) => setPrice(event.target.value)} />
      </label>

      <label>
        Public URL renters will use to reach this machine
        <input
          type="text"
          value={publicUrl}
          onChange={(event) => setPublicUrl(event.target.value)}
        />
      </label>

      <label>
        <input type="checkbox" checked={gpu} onChange={(event) => setGpu(event.target.checked)} />{" "}
        This machine has an NVIDIA GPU with the container toolkit installed
      </label>

      <h2>Run this in your ClearGate checkout</h2>
      <pre>{command}</pre>
      <button onClick={() => void copy()}>{copied ? "Copied" : "Copy command"}</button>
    </>
  );
}
