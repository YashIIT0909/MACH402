import "@nomicfoundation/hardhat-toolbox";
import { config as loadEnv } from "dotenv";
import { resolve } from "node:path";
import type { HardhatUserConfig } from "hardhat/config";

// Credentials live in the repo-root .env, the same file the renter CLI and the
// smoke test read. There is no contracts-specific .env to keep in sync.
loadEnv({ path: resolve(__dirname, "../.env") });

/**
 * Hedera's JSON-RPC relay speaks Ethereum JSON-RPC over the Hedera network, so
 * stock Hardhat tooling works against it unmodified. Deploys go through the
 * relay; everything in `test/` runs on Hardhat's in-process EVM instead, which
 * is why the test suite needs no network, no HBAR and no credentials.
 */
const HEDERA_TESTNET_RPC = process.env["HEDERA_RPC_URL"] ?? "https://testnet.hashio.io/api";
const HEDERA_TESTNET_CHAIN_ID = 296;

// Hardhat wants a raw 0x-prefixed secp256k1 key. Hedera portal ECDSA keys are
// sometimes handed out DER-encoded; the last 64 hex characters are the raw key
// either way, so normalise rather than making the provider figure it out.
function evmPrivateKeys(): string[] {
  const raw = process.env["HEDERA_PRIVATE_KEY"]?.trim();
  if (!raw) return [];
  const hex = raw.replace(/^0x/, "");
  const key = hex.length > 64 ? hex.slice(-64) : hex;
  return /^[0-9a-fA-F]{64}$/.test(key) ? [`0x${key}`] : [];
}

const config: HardhatUserConfig = {
  solidity: {
    version: "0.8.24",
    settings: {
      optimizer: { enabled: true, runs: 200 },
      // Hedera's EVM tracks Cancun on testnet; pinning it keeps the bytecode
      // from depending on whatever Hardhat's default happens to be this month.
      evmVersion: "cancun",
    },
  },
  networks: {
    hederaTestnet: {
      url: HEDERA_TESTNET_RPC,
      chainId: HEDERA_TESTNET_CHAIN_ID,
      accounts: evmPrivateKeys(),
      // The relay is slower to respond than a local node and Hedera's
      // consensus finality is ~2s; the stock 40s timeout trips spuriously.
      timeout: 120_000,
    },
  },
  paths: {
    sources: "contracts",
    tests: "test",
    cache: "cache",
    artifacts: "artifacts",
  },
};

export default config;
