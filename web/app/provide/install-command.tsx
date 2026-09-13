"use client";

import { useState } from "react";
import { Check, Copy } from "lucide-react";

import { Button } from "@/components/ui/button";
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
 * One command, run on the machine with the card: the one-liner clones and
 * builds for the provider. There is nothing to choose but a price: every node
 * sells metered sessions, with the GPU on and refunds paid automatically.
 * `GPU=1`, `LEASES=1`, `SESSIONS=1` and `SELF_SETTLE=1` stay in the command
 * although this repo's installer no longer reads them — the command fetches the
 * installer from GitHub, and a copy published before that change still needs
 * them spelled out.
 */
export function InstallCommand({ registryUrl }: { registryUrl: string }) {
  const [payTo, setPayTo] = useState("");
  const [publicUrl, setPublicUrl] = useState("http://localhost:8402");
  const [leasePrice, setLeasePrice] = useState("200000");
  const [copied, setCopied] = useState(false);

  const account = payTo === "" ? "0.0.YOUR_ACCOUNT" : payTo;
  const minutePrice = leasePrice === "" ? "200000" : leasePrice;

  const command = [
    `PAY_TO=${account}`,
    `PUBLIC_URL=${publicUrl}`,
    `REGISTRY_URL=${registryUrl}`,
    `LEASE_PRICE_TINYBARS_PER_MINUTE=${minutePrice}`,
    "GPU=1",
    "LEASES=1",
    "SESSIONS=1",
    "SELF_SETTLE=1",
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
        <span className="mb-8 block type-label text-muted-foreground">Your details</span>

        <Field label="Hedera account to be paid into" hint="An account id, never a key.">
          <Input
            value={payTo}
            onChange={(event) => setPayTo(event.target.value)}
            placeholder="0.0.1234"
          />
        </Field>

        <Field
          label="Price per minute of a session, in tinybars"
          hint="200000 = 0.002 HBAR a minute, charged by the second. A whole number, always — amounts are never floats."
        >
          <Input value={leasePrice} onChange={(event) => setLeasePrice(event.target.value)} />
        </Field>

        <Field
          label="Public URL renters reach you on"
          hint="This is what the registry hands out. It has to be reachable from outside your machine — unless you are testing both sides on this one, where localhost is right."
        >
          <Input value={publicUrl} onChange={(event) => setPublicUrl(event.target.value)} />
        </Field>
      </div>

      <div className="bg-background p-8 lg:p-12">
        <span className="mb-3 block type-label text-muted-foreground">
          Run this on the machine with the GPU
        </span>
        <p className="mb-8 text-sm text-muted-foreground">
          One command. It clones ClearGate, builds the node, checks Docker and the GPU, and starts it.
        </p>

        <div className="border border-foreground/10">
          <div className="flex items-center justify-between gap-4 border-b border-foreground/10 px-5 py-3">
            <span className="type-label text-muted-foreground">
              install command
            </span>
            <Button
              variant="accent"
              size="sm"
              onClick={() => void copy()}
              className="type-label h-7 px-4"
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
            </Button>
          </div>
          <pre className="overflow-x-auto bg-foreground/[0.02] p-5 font-mono text-sm leading-relaxed text-foreground/85">
            {command}
          </pre>
        </div>
      </div>
    </div>
  );
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
    <label className="mb-6 block last:mb-0">
      <span className="mb-2 block type-label text-muted-foreground">{label}</span>
      {children}
      <span className="mt-2 block text-sm text-muted-foreground">{hint}</span>
    </label>
  );
}
