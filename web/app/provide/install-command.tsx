"use client";

import { useState } from "react";
import { Check, Copy } from "lucide-react";

import { Input } from "@/components/ui/input";

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
      </div>

      <div className="bg-background p-8 lg:p-12">
        <span className="mb-8 block font-mono text-xs tracking-widest text-muted-foreground uppercase">
          Run in your ClearGate checkout
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
