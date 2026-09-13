import type { WalletSigner } from "@cleargate/client/browser";

/**
 * How the page gets hold of something that can sign a Hedera transaction.
 *
 * An interface rather than a direct dependency on one wallet library, for two
 * reasons. The obvious one is that HashPack is not the only Hedera wallet. The
 * load-bearing one is that the real connector needs a WalletConnect project id,
 * which is an account somebody has to register — so a deployment that does not
 * have one yet still needs a way to exercise this whole flow.
 *
 * What a connector must NOT do is submit. The facilitator co-signs as fee payer
 * and submits; a transaction already on consensus cannot be settled again.
 * `WalletSigner.signTransaction` is sign-only and the adapter in
 * `@cleargate/client/browser` checks that the fee payer survived it.
 */
export type WalletConnector = {
  /** Stable key, also what gets persisted so a reload reconnects the same way. */
  readonly id: "walletconnect" | "dev-key";
  /** Shown on the connect button. */
  readonly label: string;
  /**
   * True when this connector can actually be used in this deployment. The
   * WalletConnect one is false without a project id; the dev one is false
   * unless it was explicitly switched on.
   */
  readonly available: boolean;
  /** Why it is unavailable, shown to the operator rather than swallowed. */
  readonly unavailableReason?: string;
  /**
   * True when this signs with a key the page itself can see. Renders a warning
   * in the UI — a renter should never be unsure which of these they are using.
   */
  readonly usesLocalKey: boolean;

  connect(): Promise<WalletSigner>;
  /**
   * Reconnects a session that already exists, without prompting.
   *
   * Called on page load for whichever connector was last used. It must never
   * open a modal or ask the wallet for anything the renter has not already
   * approved — a page that popped a wallet dialog on every reload would be
   * worse than the forgetting it fixes. Returns null when there is nothing to
   * restore, which is the ordinary case for a first visit.
   */
  restore(): Promise<WalletSigner | null>;
  disconnect(): Promise<void>;
};

export type WalletState =
  | { status: "disconnected" }
  | { status: "connecting"; connectorId: string }
  | { status: "connected"; connectorId: string; accountId: string; signer: WalletSigner }
  | { status: "error"; message: string };
