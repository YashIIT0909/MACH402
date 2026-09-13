"use client";

import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
  type ReactNode,
} from "react";

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

/*
 * Which connector was last used, so a reload can reconnect the same way instead
 * of showing a renter with a live session the connect button again.
 *
 * Only the connector's id is kept. No key, no account, no WalletConnect
 * session — the wallet library owns its own persistence and this is just the
 * note of which one to ask.
 */
const CONNECTOR_KEY = "mach402.wallet.v1";

function rememberConnector(id: string): void {
  try {
    window.localStorage.setItem(CONNECTOR_KEY, id);
  } catch {
    // Storage can be blocked outright. The connection still works for this
    // page load; only the reconnect-on-reload is lost.
  }
}

function forgetConnector(): void {
  try {
    window.localStorage.removeItem(CONNECTOR_KEY);
  } catch {
    // Nothing to do.
  }
}

function lastConnector(): string | null {
  try {
    return window.localStorage.getItem(CONNECTOR_KEY);
  } catch {
    return null;
  }
}

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
        rememberConnector(connectorId);
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
    forgetConnector();
    setState({ status: "disconnected" });
  }, [connectors]);

  /*
   * Reconnect on load, silently.
   *
   * A renter who reloads mid-session still has credit burning on a node, and
   * the page cannot top it up or stop it without a signer — so the connection
   * has to come back on its own. `restore` never prompts: if the wallet no
   * longer knows this origin it returns null and the connect button is shown,
   * which is the honest outcome rather than a modal nobody asked for.
   */
  const restored = useRef(false);
  useEffect(() => {
    if (restored.current) return;
    restored.current = true;

    const connectorId = lastConnector();
    if (connectorId === null) return;

    const connector = connectors.find((candidate) => candidate.id === connectorId);
    if (connector === undefined || !connector.available) {
      forgetConnector();
      return;
    }

    let cancelled = false;
    setState({ status: "connecting", connectorId });

    void connector
      .restore()
      .then((signer) => {
        if (cancelled) return;
        if (signer === null) {
          forgetConnector();
          setState({ status: "disconnected" });
          return;
        }
        setState({
          status: "connected",
          connectorId,
          accountId: signer.accountId,
          signer,
        });
      })
      .catch(() => {
        if (cancelled) return;
        // A failed restore is not an error worth showing: the renter did not
        // ask for anything yet, and the connect button says what to do next.
        forgetConnector();
        setState({ status: "disconnected" });
      });

    return () => {
      cancelled = true;
    };
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
