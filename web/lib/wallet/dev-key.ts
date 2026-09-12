"use client";

import { PrivateKey } from "@hiero-ledger/sdk";
import type { WalletSigner } from "@cleargate/client/browser";
import type { WalletConnector } from "./types";

/**
 * A stand-in wallet that signs with a key baked into the page bundle.
 *
 * This exists so the browser rent flow can be exercised end to end before a
 * WalletConnect project id has been registered: it proves CORS, the 402, the
 * payload, settlement, the tunnel link and the countdown, leaving the wallet
 * prompt as the only unproven step.
 *
 * It is off unless switched on, and it should stay off. Two guards, both
 * deliberate:
 *
 *  - `NEXT_PUBLIC_DEV_WALLET` must be "1". Absent, this connector reports
 *    itself unavailable and nothing can reach it.
 *  - The key comes from `NEXT_PUBLIC_DEV_WALLET_KEY`, and anything with the
 *    `NEXT_PUBLIC_` prefix is compiled into the JavaScript every visitor
 *    downloads. That is not a leak to be fixed later; it is what this
 *    connector is. Use a throwaway testnet account holding a few HBAR and
 *    nothing else, and never point it at an account that matters.
 *
 * CLAUDE.md's rule is that only the renter side holds a key and it comes from
 * the environment. That still holds here — the renter is the one running this
 * page — but "the environment" being a public bundle is a real weakening, which
 * is why the UI labels it rather than letting it pass for a wallet.
 */
const ENABLED = process.env["NEXT_PUBLIC_DEV_WALLET"] === "1";
const ACCOUNT_ID = process.env["NEXT_PUBLIC_DEV_WALLET_ACCOUNT_ID"] ?? "";
const PRIVATE_KEY = process.env["NEXT_PUBLIC_DEV_WALLET_KEY"] ?? "";
const KEY_TYPE = (process.env["NEXT_PUBLIC_DEV_WALLET_KEY_TYPE"] ?? "ecdsa").toLowerCase();

export function devKeyConnector(): WalletConnector {
  const configured = ENABLED && ACCOUNT_ID !== "" && PRIVATE_KEY !== "";

  return {
    id: "dev-key",
    label: "Use dev key (testnet)",
    available: configured,
    unavailableReason: ENABLED
      ? "NEXT_PUBLIC_DEV_WALLET is on but NEXT_PUBLIC_DEV_WALLET_ACCOUNT_ID or NEXT_PUBLIC_DEV_WALLET_KEY is missing."
      : undefined,
    usesLocalKey: true,

    async connect(): Promise<WalletSigner> {
      if (!configured) {
        throw new Error("the dev key connector is not configured");
      }

      const key =
        KEY_TYPE === "ed25519"
          ? PrivateKey.fromStringED25519(PRIVATE_KEY)
          : PrivateKey.fromStringECDSA(PRIVATE_KEY);

      return {
        accountId: ACCOUNT_ID,
        // Sign, never submit — the same contract a real wallet honours. The
        // facilitator is what puts this on consensus.
        signTransaction: async (transaction) => transaction.sign(key),
      };
    },

    async disconnect(): Promise<void> {
      // Nothing to tear down: the key is read fresh on every connect and no
      // session is held open.
    },
  };
}
