/**
 * Environment loading for the smoke test. The private key is read here and
 * nowhere else in this package; it is never logged, written to disk, or sent
 * anywhere but the local Hedera signer.
 */
import { config as loadDotenv } from "dotenv";
import { fileURLToPath } from "node:url";
import { dirname, resolve } from "node:path";

const here = dirname(fileURLToPath(import.meta.url));
loadDotenv({ path: resolve(here, "../../.env"), quiet: true });

function required(name: string): string {
  const value = process.env[name];
  if (value === undefined || value.trim() === "") {
    throw new Error(
      `${name} is not set. Copy .env.example to .env and fill it in — ` +
        `get a funded testnet account at https://portal.hedera.com`,
    );
  }
  return value.trim();
}

function optional(name: string, fallback: string): string {
  const value = process.env[name];
  return value === undefined || value.trim() === "" ? fallback : value.trim();
}

export const FACILITATOR_URL = optional(
  "FACILITATOR_URL",
  "https://api.testnet.blocky402.com",
);

export const SMOKE_PORT = Number(optional("SMOKE_PORT", "4402"));

/** Renter credentials. Only `client.ts` calls this. */
export function renterCredentials(): {
  accountId: string;
  privateKey: string;
  keyType: "ecdsa" | "ed25519";
} {
  const keyType = optional("HEDERA_KEY_TYPE", "ecdsa").toLowerCase();
  if (keyType !== "ecdsa" && keyType !== "ed25519") {
    throw new Error(`HEDERA_KEY_TYPE must be "ecdsa" or "ed25519", got "${keyType}"`);
  }
  return {
    accountId: required("HEDERA_ACCOUNT_ID"),
    privateKey: required("HEDERA_PRIVATE_KEY"),
    keyType,
  };
}

/** Provider account the reference server is paid into. */
export function payToAccountId(): string {
  return required("PAY_TO_ACCOUNT_ID");
}
