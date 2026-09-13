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

        <div className="border border-foreground/10 p-4">
          <span className="block text-sm text-muted-foreground">
            Needs an NVIDIA GPU with the NVIDIA Container Toolkit installed. The GPU is always turned
            on; the node re-checks the card itself and falls back to CPU if it cannot be passed through.
          </span>
        </div>

        <div className="mt-4 border border-accent/30 bg-accent/[0.04] p-4">
          <span className="mb-3 block type-label text-muted-foreground">What renters get</span>
          <p className="mb-4 text-sm text-muted-foreground">
            Renters get a root shell in a container on this machine, through Jupyter in their
            browser. Their code and data never leave their own machine, and nothing of yours is
            exposed: every Linux capability is dropped but the few <Code>sshd</Code> needs, it
            cannot gain privileges, it gets a throwaway filesystem, and it reaches the network
            only through a proxy that allows package and model registries and refuses the rest.
            Access is by certificate, signed on your machine, valid only while credit is paid for.
          </p>
          <p className="mb-4 text-sm text-muted-foreground">
            Root inside a container is still not nothing. The installer points you at
            Docker&apos;s user-namespace remapping, which maps that root to an unprivileged user
            on your host — turn it on before renting to strangers.
          </p>
          <p className="mb-5 text-sm text-muted-foreground">
            Renters pay in chunks of credit that burn by the second, and whatever they do not use
            is refunded automatically — from a Hedera operator key the installer creates on this
            machine, separate from the account above. Setup waits while you send it a few testnet
            HBAR, enough to cover a refund. Your earnings still land in your own account, which
            never signs anything.
          </p>

          <span className="mb-2 block type-label text-muted-foreground">
            How renters reach the session
          </span>
          <p className="mb-2 text-sm text-muted-foreground">
            Through a Cloudflare quick tunnel — no Cloudflare account, and a fresh random hostname
            per session, so the link dies with it. Jupyter only: a quick tunnel carries no SSH.
          </p>
          <p className="text-sm text-muted-foreground">
            Needs <Code>cloudflared</Code>, <Code>pnpm</Code> and <Code>ssh-keygen</Code>; the
            installer fetches <Code>cloudflared</Code> itself. Without a working tunnel the node
            refuses to sell a session rather than take payment for something unreachable.
          </p>
        </div>
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

        <p className="mt-6 text-sm text-muted-foreground">
          Leave <Code>REGISTRY_URL</Code> out and the node is simply unlisted — renters who know its
          URL can still pay it normally.
        </p>

        <div className="mt-8 border-t border-foreground/10 pt-8">
          <span className="mb-4 block type-label text-muted-foreground">Then what</span>
          <ol className="space-y-4 text-sm text-muted-foreground">
            <Step n="1">
              The node starts announcing itself every 30 seconds. It appears on{" "}
              <a href="/nodes" className="text-foreground underline-offset-4 hover:text-accent hover:underline">
                the nodes page
              </a>{" "}
              within a few seconds of starting — no account, no approval.
            </Step>
            <Step n="2">
              Renters click through to your listing and pay you directly, from their own wallet.
              Nothing routes through us.
            </Step>
            <Step n="3">
              Earnings land in your account as each payment settles, and every one is appended to{" "}
              <Code>receipts.jsonl</Code> beside your config, so you can audit them without
              trusting this website.
            </Step>
            <Step n="4">
              Press <Code>q</Code> in the dashboard to stop. The node tells the registry it is
              going offline so it stops being advertised immediately, rather than looking available
              for another minute and a half.
            </Step>
          </ol>
        </div>
      </div>
    </div>
  );
}

function Step({ n, children }: { n: string; children: React.ReactNode }) {
  return (
    <li className="grid grid-cols-[24px_1fr] gap-3">
      <span className="font-mono text-accent">{n}</span>
      <span>{children}</span>
    </li>
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
      <span className="mb-2 block type-label text-muted-foreground">{label}</span>
      {children}
      <span className="mt-2 block text-sm text-muted-foreground">{hint}</span>
    </label>
  );
}
