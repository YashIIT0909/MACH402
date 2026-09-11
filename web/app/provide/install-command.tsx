"use client";

import { useState } from "react";

// Where the installer itself lives. Fetched fresh on every run rather than
// vendored, so a provider always gets the current script without needing a
// checkout of their own first.
const INSTALL_SCRIPT_URL =
  "https://raw.githubusercontent.com/YashIIT0909/ClearGate/main/scripts/install.sh";

/**
 * Builds the provider's install command.
 *
 * Nothing is submitted anywhere: the registry learns about a node when that
 * node first heartbeats, not when someone fills in this form. So there is no
 * account to create, and no way to list a machine you do not control.
 *
 * The command is self-contained — curl fetches the installer, and the
 * installer itself clones ClearGate if there is no checkout already sitting
 * at $CLEARGATE_DIR. A provider does not need git-clone this repo by hand
 * first.
 */
export function InstallCommand({ registryUrl }: { registryUrl: string }) {
  const [payTo, setPayTo] = useState("");
  const [price, setPrice] = useState("100000");
  const [publicUrl, setPublicUrl] = useState("http://localhost:8402");
  const [gpu, setGpu] = useState(false);
  // Deliberately its own checkbox, and off by default. Renting out batch
  // compute and handing someone a shell are different decisions, and this one
  // must never ride along with the GPU box being ticked.
  const [leases, setLeases] = useState(false);
  const [leasePrice, setLeasePrice] = useState("200000");
  const [copied, setCopied] = useState(false);

  const command = [
    `PAY_TO=${payTo === "" ? "0.0.YOUR_ACCOUNT" : payTo}`,
    `PRICE_TINYBARS=${price === "" ? "100000" : price}`,
    `PUBLIC_URL=${publicUrl}`,
    `REGISTRY_URL=${registryUrl}`,
    ...(gpu ? ["GPU=1"] : []),
    ...(leases
      ? ["LEASES=1", `LEASE_PRICE_TINYBARS_PER_MINUTE=${leasePrice === "" ? "200000" : leasePrice}`]
      : []),
    `bash -c "$(curl -fsSL ${INSTALL_SCRIPT_URL})"`,
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

      <label>
        <input
          type="checkbox"
          checked={leases}
          onChange={(event) => setLeases(event.target.checked)}
        />{" "}
        Also rent out interactive sessions — a shell and a Jupyter server, by the minute
      </label>

      {leases ? (
        <>
          <p>
            Renters get a root shell in a container on this machine and connect over SSH or from
            Colab. Their code and data never leave their own machine, and nothing of yours is
            exposed: the container has every Linux capability dropped but the few{" "}
            <code>sshd</code> needs, cannot gain privileges, gets its own throwaway filesystem, and
            reaches the network only through a proxy that allows package and model registries and
            refuses everything else. Access is by certificate, signed on your machine, valid only
            for the minutes that were paid for.
          </p>
          <p>
            Root inside a container is still not nothing. The installer will point you at Docker&apos;s
            user-namespace remapping, which maps that root to an unprivileged user on your host —
            turn it on before you rent to strangers.
          </p>
          <label>
            Price per minute of interactive time, in tinybars
            <input
              type="text"
              value={leasePrice}
              onChange={(event) => setLeasePrice(event.target.value)}
            />
          </label>
        </>
      ) : null}

      <h2>Run this in your terminal</h2>
      <pre>{command}</pre>
      <button onClick={() => void copy()}>{copied ? "Copied" : "Copy command"}</button>
    </>
  );
}
