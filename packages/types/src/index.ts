/**
 * ClearGate shared types — the cross-component contract.
 *
 * The x402 wire types are re-exported from `@x402/core/types` rather than
 * redeclared, so a facilitator or SDK change surfaces as a compile error
 * instead of a silent shape mismatch at runtime. See CLAUDE.md: changing the
 * 402 challenge shape is a cross-team break.
 */

export type {
  Network,
  PaymentPayload,
  PaymentRequired,
  PaymentRequirements,
  ResourceInfo,
  SettleResponse,
  SupportedKind,
  SupportedResponse,
  VerifyRequest,
  VerifyResponse,
  SettleRequest,
} from "@x402/core/types";

export * from "./x402.js";
export * from "./node.js";
export * from "./job.js";
export * from "./lease.js";
