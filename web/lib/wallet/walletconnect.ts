"use client";

import { LedgerId } from "@hiero-ledger/sdk";
import {
  DAppConnector,
  HederaChainId,
  HederaJsonRpcMethod,
} from "@hashgraph/hedera-wallet-connect";
import type { WalletSigner } from "@cleargate/client/browser";
import type { WalletConnector } from "./types";

/**
 * HashPack and friends, over WalletConnect.
 *
 * Only one JSON-RPC method is requested: `hedera_signTransaction`. Not
 * `hedera_signAndExecuteTransaction`, which is the one that would look more
 * convenient and would break the payment — the facilitator co-signs as fee
 * payer and submits, so a transaction the wallet already put on consensus can
 * never be settled through x402. Asking for the narrower permission also means
 * the wallet's own prompt tells the renter the truth about what it is doing.
 */
const PROJECT_ID = process.env["NEXT_PUBLIC_WALLETCONNECT_PROJECT_ID"] ?? "";

export function walletConnectConnector(): WalletConnector {
  let connector: DAppConnector | null = null;

  return {
    id: "walletconnect",
    label: "Connect wallet",
    available: PROJECT_ID !== "",
    unavailableReason:
      PROJECT_ID === ""
        ? "NEXT_PUBLIC_WALLETCONNECT_PROJECT_ID is not set. Register a free project at cloud.reown.com and put the id in web/.env.local."
        : undefined,
    usesLocalKey: false,

    async connect(): Promise<WalletSigner> {
      if (PROJECT_ID === "") {
        throw new Error("no WalletConnect project id is configured");
      }

      connector = new DAppConnector(
        {
          name: "MACH402",
          description: "Rent a GPU by the second, paid with x402 on Hedera",
          url: window.location.origin,
          icons: [`${window.location.origin}/favicon.ico`],
        },
        LedgerId.TESTNET,
        PROJECT_ID,
        [HederaJsonRpcMethod.SignTransaction],
        [],
        [HederaChainId.Testnet],
      );

      await connector.init();
      await connector.openModal();

      const signer = connector.signers[0];
      if (signer === undefined) {
        throw new Error("the wallet connected but exposed no account");
      }

      return {
        accountId: signer.getAccountId().toString(),
        // The SDK's own Signer interface, which is exactly the sign-without-
        // submit shape `@cleargate/client/browser` needs. Nothing is adapted
        // here beyond the name.
        signTransaction: (transaction) => signer.signTransaction(transaction),
      };
    },

    async disconnect(): Promise<void> {
      await connector?.disconnectAll().catch(() => {
        // A session the wallet already dropped is not an error worth showing:
        // the renter asked to disconnect and they are disconnected either way.
      });
      connector = null;
    },
  };
}
