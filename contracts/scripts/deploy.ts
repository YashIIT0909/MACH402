import { ethers, network } from "hardhat";
import { mkdirSync, writeFileSync } from "node:fs";
import { resolve } from "node:path";

/**
 * Deploys SessionEscrow and IdentityRegistry, and writes the addresses where
 * the rest of the repo can read them.
 *
 * Both contracts are immutable and have no constructor arguments and no owner,
 * so a deployment is fully described by its address — there is no post-deploy
 * configuration step to forget.
 */
async function main(): Promise<void> {
  const [deployer] = await ethers.getSigners();
  if (!deployer) {
    throw new Error(
      "no signer: set HEDERA_PRIVATE_KEY in ClearGate/.env to a funded testnet ECDSA key",
    );
  }

  const balance = await ethers.provider.getBalance(deployer.address);
  console.log(`network   ${network.name}`);
  console.log(`deployer  ${deployer.address}`);
  console.log(`balance   ${ethers.formatEther(balance)} HBAR`);
  if (balance === 0n) {
    throw new Error("deployer has no balance; fund it at https://portal.hedera.com");
  }
  console.log("");

  const escrowFactory = await ethers.getContractFactory("SessionEscrow");
  const escrow = await escrowFactory.deploy();
  await escrow.waitForDeployment();
  const escrowAddress = await escrow.getAddress();
  console.log(`SessionEscrow     ${escrowAddress}`);

  const registryFactory = await ethers.getContractFactory("IdentityRegistry");
  const registry = await registryFactory.deploy();
  await registry.waitForDeployment();
  const registryAddress = await registry.getAddress();
  console.log(`IdentityRegistry  ${registryAddress}`);

  const record = {
    network: network.name,
    chainId: Number((await ethers.provider.getNetwork()).chainId),
    deployedAt: new Date().toISOString(),
    deployer: deployer.address,
    contracts: {
      SessionEscrow: escrowAddress,
      IdentityRegistry: registryAddress,
    },
  };

  const outDir = resolve(__dirname, "../deployments");
  mkdirSync(outDir, { recursive: true });
  const outFile = resolve(outDir, `${network.name}.json`);
  writeFileSync(outFile, `${JSON.stringify(record, null, 2)}\n`);

  console.log("");
  console.log(`written   ${outFile}`);
  console.log("");
  console.log("HashScan:");
  console.log(`  https://hashscan.io/testnet/contract/${escrowAddress}`);
  console.log(`  https://hashscan.io/testnet/contract/${registryAddress}`);
  console.log("");
  console.log("Put these in the node's config.yaml:");
  console.log("  leases:");
  console.log("    payment_mode: escrow");
  console.log(`    escrow_contract_id: "${escrowAddress}"`);
  console.log("  identity:");
  console.log(`    registry_contract_id: "${registryAddress}"`);
}

main().catch((error) => {
  console.error(error);
  process.exitCode = 1;
});
