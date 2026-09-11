"use client";

import { useState } from "react";
import { Check, Copy } from "lucide-react";

import { Input } from "@/components/ui/input";

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
 * installer clones ClearGate if there is no checkout already at
 * $CLEARGATE_DIR. A provider does not need to git-clone this repo by hand.
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
    <div className="grid gap-px bg-foreground/10 lg:grid-cols-2">
      <div className="bg-background p-8 lg:p-12">
        <span className="mb-8 block font-mono text-xs tracking-widest text-muted-foreground uppercase">
          Your details
        </span>

        <Field label="Hedera account to be paid into" hint="An account id, never a key.">
          <Input
            value={payTo}
            onChange={(event) => setPayTo(event.target.value)}
            placeholder="0.0.1234"
          />
        </Field>

        <Field
          label="Price per job, in tinybars"
          hint="100000 = 0.001 HBAR. A whole number, always — amounts are never floats."
        >
          <Input value={price} onChange={(event) => setPrice(event.target.value)} />
        </Field>

        <Field
          label="Public URL renters reach you on"
          hint="This is what the registry hands out. It has to be reachable from outside your machine."
        >
          <Input value={publicUrl} onChange={(event) => setPublicUrl(event.target.value)} />
        </Field>

        <label className="group flex cursor-pointer items-start gap-3 border border-foreground/10 p-4 transition-colors hover:border-foreground/25">
          <input
            type="checkbox"
            checked={gpu}
            onChange={(event) => setGpu(event.target.checked)}
            className="mt-0.5 h-4 w-4 shrink-0 accent-[var(--accent)]"
          />
          <span className="text-sm text-muted-foreground">
            This machine has an NVIDIA GPU with the container toolkit installed. The node re-checks
            this itself and falls back to CPU if the card cannot be passed through.
          </span>
        </label>

        <label className="group mt-4 flex cursor-pointer items-start gap-3 border border-foreground/10 p-4 transition-colors hover:border-foreground/25">
          <input
            type="checkbox"
            checked={leases}
            onChange={(event) => setLeases(event.target.checked)}
            className="mt-0.5 h-4 w-4 shrink-0 accent-[var(--accent)]"
          />
          <span className="text-sm text-muted-foreground">
            Also rent out interactive sessions — a shell and a Jupyter server, by the minute.
            Separate from the box above on purpose.
          </span>
        </label>

        {leases ? (
          <div className="mt-4 border border-accent/30 bg-accent/[0.04] p-4">
            <p className="mb-4 text-sm text-muted-foreground">
              Renters get a root shell in a container on this machine and connect over SSH or from
              Colab. Their code and data never leave their own machine, and nothing of yours is
              exposed: every Linux capability is dropped but the few <Code>sshd</Code> needs, it
              cannot gain privileges, it gets a throwaway filesystem, and it reaches the network
              only through a proxy that allows package and model registries and refuses the rest.
              Access is by certificate, signed on your machine, valid only for the minutes paid for.
            </p>
            <p className="mb-5 text-sm text-muted-foreground">
              Root inside a container is still not nothing. The installer points you at
              Docker&apos;s user-namespace remapping, which maps that root to an unprivileged user
              on your host — turn it on before renting to strangers.
            </p>
            <Field
              label="Price per minute of interactive time, in tinybars"
              hint="200000 = 0.002 HBAR a minute. A whole number, as ever."
            >
              <Input
                value={leasePrice}
                onChange={(event) => setLeasePrice(event.target.value)}
              />
            </Field>
          </div>
        ) : null}
      </div>

      <div className="bg-background p-8 lg:p-12">
        <span className="mb-8 block font-mono text-xs tracking-widest text-muted-foreground uppercase">
          Run this in your terminal
        </span>

        <div className="border border-foreground/10">
          <div className="flex items-center justify-between gap-4 border-b border-foreground/10 px-5 py-3">
            <span className="font-mono text-xs tracking-widest text-muted-foreground uppercase">
              install command
            </span>
            <button
              onClick={() => void copy()}
              className="inline-flex items-center gap-2 rounded-full bg-primary px-4 py-1.5 font-mono text-[0.625rem] tracking-widest text-primary-foreground uppercase transition-colors hover:bg-primary/90"
            >
              {copied ? (
                <>
                  Copied <Check className="h-3 w-3" />
                </>
              ) : (
                <>
                  Copy <Copy className="h-3 w-3" />
                </>
              )}
            </button>
          </div>
          <pre className="overflow-x-auto bg-foreground/[0.02] p-5 font-mono text-sm leading-relaxed text-foreground/85">
            {command}
          </pre>
        </div>

        <p className="mt-6 text-sm text-muted-foreground">
          Leave <code className="font-mono text-foreground">REGISTRY_URL</code> out and the node is
          simply unlisted — renters who know its URL can still pay it normally.
        </p>
      </div>
    </div>
  );
}

function Code({ children }: { children: React.ReactNode }) {
  return <code className="font-mono text-[0.9em] text-foreground">{children}</code>;
}

function Field({
  label,
  hint,
  children,
}: {
  label: string;
  hint: string;
  children: React.ReactNode;
}) {
  return (
    <label className="mb-6 block">
      <span className="mb-2 block font-mono text-xs tracking-widest text-muted-foreground uppercase">
        {label}
      </span>
      {children}
      <span className="mt-2 block text-sm text-muted-foreground">{hint}</span>
    </label>
  );
}
