import Link from "next/link";
import { ChevronDown } from "lucide-react";

/*
 * The provider setup guide, folded away.
 *
 * Most people copying the command already have Docker and a GPU working, so this
 * stays closed until asked for. It is a native <details>, so it opens without
 * JavaScript and is keyboard-operable for free. Anything that would take more
 * than a line to explain links to the tool's own install docs instead.
 */

type Item = { title: string; body: React.ReactNode };

const PREREQUISITES: Item[] = [
  {
    title: "Docker",
    body: (
      <>
        <External href="https://docs.docker.com/engine/install/">Install Docker Engine</External>,
        then{" "}
        <External href="https://docs.docker.com/engine/install/linux-postinstall/">
          add your user to the docker group
        </External>
        .
      </>
    ),
  },
  {
    title: "An NVIDIA GPU",
    body: (
      <>
        With the{" "}
        <External href="https://docs.nvidia.com/datacenter/cloud-native/container-toolkit/latest/install-guide.html">
          NVIDIA Container Toolkit
        </External>
        . Without it the node still runs, CPU-only.
      </>
    ),
  },
  {
    title: "Go, git and pnpm",
    body: (
      <>
        <External href="https://go.dev/doc/install">Go 1.25+</External>,{" "}
        <External href="https://git-scm.com/downloads">git</External> and{" "}
        <External href="https://pnpm.io/installation">pnpm</External> — the installer builds the
        node with them.
      </>
    ),
  },
  {
    title: "A Hedera testnet account",
    body: (
      <>
        Create one at <External href="https://portal.hedera.com">portal.hedera.com</External>. Its
        account id is what you are paid into.
      </>
    ),
  },
];

const FIRST_RUN: Item[] = [
  {
    title: "Run the command",
    body: "Fill in your details above, copy the command and run it on the machine with the GPU.",
  },
  {
    title: "Fund the operator key",
    body: (
      <>
        Setup prints a new address for the node&apos;s own key, which pays refunds. Send it a few
        testnet HBAR from the <External href="https://portal.hedera.com">portal</External>; setup
        continues once it lands.
      </>
    ),
  },
  {
    title: "Check your listing",
    body: (
      <>
        The node opens its dashboard and appears on{" "}
        <Link href="/nodes" className="text-foreground underline-offset-4 hover:text-accent hover:underline">
          the nodes page
        </Link>{" "}
        within seconds. Press <code className="font-mono text-foreground">q</code> to stop.
      </>
    ),
  },
  {
    title: "Recommended",
    body: (
      <>
        Turn on{" "}
        <External href="https://docs.docker.com/engine/security/userns-remap/">
          Docker user-namespace remapping
        </External>{" "}
        before renting to strangers.
      </>
    ),
  },
];

export function SetupGuide() {
  return (
    <details className="group mt-8 border border-foreground/10 bg-background">
      <summary className="flex cursor-pointer list-none items-center justify-between gap-4 px-8 py-5 transition-colors ease-brand dur-base hover:bg-foreground/[0.02] lg:px-12 [&::-webkit-details-marker]:hidden">
        <span className="type-label text-foreground">Setup guide</span>
        <ChevronDown className="h-4 w-4 shrink-0 text-muted-foreground transition-transform ease-brand dur-base group-open:rotate-180" />
      </summary>

      <div className="grid gap-px border-t border-foreground/10 bg-foreground/10 lg:grid-cols-2">
        <Column label="Before you start" items={PREREQUISITES} />
        <Column label="First run" items={FIRST_RUN} />
      </div>
    </details>
  );
}

function Column({ label, items }: { label: string; items: Item[] }) {
  return (
    <div className="bg-background p-8 lg:p-12">
      <span className="mb-6 block type-label text-muted-foreground">{label}</span>
      <ol className="space-y-5">
        {items.map((item, index) => (
          <li key={item.title} className="grid grid-cols-[1.75rem_minmax(0,1fr)] gap-3">
            <span className="font-mono text-sm text-accent">{String(index + 1).padStart(2, "0")}</span>
            <div>
              <div className="mb-1 text-foreground">{item.title}</div>
              <p className="text-sm text-muted-foreground">{item.body}</p>
            </div>
          </li>
        ))}
      </ol>
    </div>
  );
}

function External({ href, children }: { href: string; children: React.ReactNode }) {
  return (
    <a
      href={href}
      target="_blank"
      rel="noreferrer"
      className="text-foreground underline underline-offset-4 decoration-foreground/30 hover:text-accent hover:decoration-accent"
    >
      {children}
    </a>
  );
}
