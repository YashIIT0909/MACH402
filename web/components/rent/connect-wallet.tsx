"use client";

import { Button } from "@/components/ui/button";
import { useWallet } from "@/components/wallet/wallet-provider";

/**
 * The connect step.
 *
 * Unavailable connectors are listed rather than hidden, with the reason. A
 * missing WalletConnect project id is an operator mistake and hiding the button
 * turns it into "the site is broken" — the sort of failure that costs an hour
 * before anyone thinks to check an environment variable.
 */
export function ConnectWallet() {
  const { state, connectors, connect, disconnect } = useWallet();

  if (state.status === "connected") {
    const connector = connectors.find((candidate) => candidate.id === state.connectorId);

    return (
      <div className="border border-foreground/10 p-4">
        <div className="flex items-center justify-between gap-4">
          <div className="min-w-0">
            <div className="type-label text-muted-foreground">Paying from</div>
            <div className="truncate font-mono">{state.accountId}</div>
          </div>
          <Button variant="quiet" size="sm" onClick={() => void disconnect()}>
            Disconnect
          </Button>
        </div>

        {connector?.usesLocalKey === true ? (
          <p className="mt-3 border-t border-foreground/10 pt-3 text-xs text-muted-foreground">
            <strong className="text-foreground">Dev key.</strong> This page is signing with a
            private key compiled into its own JavaScript, which every visitor can read. Fine for a
            testnet account holding a few HBAR; never point it at anything else.
          </p>
        ) : null}
      </div>
    );
  }

  return (
    <div className="space-y-3">
      {connectors.map((connector) => (
        <div key={connector.id}>
          <Button
            variant={connector.usesLocalKey ? "outline" : "default"}
            size="lg"
            className="w-full"
            disabled={!connector.available || state.status === "connecting"}
            onClick={() => void connect(connector.id)}
          >
            {state.status === "connecting" && state.connectorId === connector.id
              ? "Waiting for the wallet…"
              : connector.label}
          </Button>
          {connector.unavailableReason !== undefined ? (
            <p className="mt-2 font-mono text-xs text-muted-foreground">
              {connector.unavailableReason}
            </p>
          ) : null}
        </div>
      ))}

      {state.status === "error" ? (
        <p className="font-mono text-xs break-words text-destructive">{state.message}</p>
      ) : null}
    </div>
  );
}
