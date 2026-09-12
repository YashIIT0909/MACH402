"use client";

import { createContext, useCallback, useContext, useMemo, useState, type ReactNode } from "react";

import { devKeyConnector } from "@/lib/wallet/dev-key";
import { walletConnectConnector } from "@/lib/wallet/walletconnect";
import type { WalletConnector, WalletState } from "@/lib/wallet/types";

type WalletContextValue = {
  state: WalletState;
  connectors: WalletConnector[];
  connect: (connectorId: string) => Promise<void>;
  disconnect: () => Promise<void>;
};

const WalletContext = createContext<WalletContextValue | null>(null);

export function WalletProvider({ children }: { children: ReactNode }) {
  const [state, setState] = useState<WalletState>({ status: "disconnected" });

  // Built once. Both connectors hold a live session or a parsed key once
  // connected, so rebuilding them on render would silently drop the connection.
  const connectors = useMemo(() => [walletConnectConnector(), devKeyConnector()], []);

  const connect = useCallback(
    async (connectorId: string) => {
      const connector = connectors.find((candidate) => candidate.id === connectorId);
      if (connector === undefined) {
        setState({ status: "error", message: `no connector called ${connectorId}` });
        return;
      }

      setState({ status: "connecting", connectorId });
      try {
        const signer = await connector.connect();
        setState({
          status: "connected",
          connectorId,
          accountId: signer.accountId,
          signer,
        });
      } catch (error) {
        setState({
          status: "error",
          message: error instanceof Error ? error.message : String(error),
        });
      }
    },
    [connectors],
  );

  const disconnect = useCallback(async () => {
    for (const connector of connectors) {
      await connector.disconnect().catch(() => {
        // Disconnecting is best effort by design — see the connectors.
      });
    }
    setState({ status: "disconnected" });
  }, [connectors]);

  const value = useMemo(
    () => ({ state, connectors, connect, disconnect }),
    [state, connectors, connect, disconnect],
  );

  return <WalletContext.Provider value={value}>{children}</WalletContext.Provider>;
}

export function useWallet(): WalletContextValue {
  const value = useContext(WalletContext);
  if (value === null) {
    throw new Error("useWallet must be used inside a WalletProvider");
  }
  return value;
}
