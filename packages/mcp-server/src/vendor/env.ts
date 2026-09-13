/**
 * Renter-side environment. This is the only side of ClearGate that holds a key.
 *
 * The key is read from the environment, never from a config file, never written
 * to disk and never logged — see CLAUDE.md, "Secrets". This is a vendored copy
 * of client/src/env.ts: this package is deliberately standalone (no
 * @mach402/* workspace dependency), so its own copy lives here rather than
 * being imported across packages.
 */
import { config as loadDotenv } from "dotenv";
import { existsSync } from "node:fs";
import { resolve } from "node:path";

let loaded = false;

/** Loads .env from the current directory, once. */
export function loadEnv(): void {
  if (loaded) return;
  loaded = true;
  const candidate = resolve(process.cwd(), ".env");
  if (existsSync(candidate)) {
    loadDotenv({ path: candidate, quiet: true });
    return;
  }
  loadDotenv({ quiet: true });
}

export type RenterCredentials = {
  accountId: string;
  privateKey: string;
  keyType: "ecdsa" | "ed25519";
};

export function renterCredentials(): RenterCredentials {
  loadEnv();

  const accountId = process.env["HEDERA_ACCOUNT_ID"]?.trim();
  const privateKey = process.env["HEDERA_PRIVATE_KEY"]?.trim();
  const keyType = (process.env["HEDERA_KEY_TYPE"] ?? "ecdsa").trim().toLowerCase();

  if (!accountId || !privateKey) {
    throw new Error(
      "HEDERA_ACCOUNT_ID and HEDERA_PRIVATE_KEY must be set to pay for compute.\n" +
        "Copy .env.example to .env and fill it in, or set them directly in your MCP host's " +
        "server config — fund a testnet account at https://portal.hedera.com",
    );
  }
  if (keyType !== "ecdsa" && keyType !== "ed25519") {
    throw new Error(`HEDERA_KEY_TYPE must be "ecdsa" or "ed25519", got "${keyType}"`);
  }

  return { accountId, privateKey, keyType };
}
