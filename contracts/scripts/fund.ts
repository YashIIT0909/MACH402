import { ethers } from "hardhat";

/**
 * Sends HBAR to an EVM address, creating the Hedera account behind it.
 *
 * A provider normally does this from the portal or a faucet; this exists so the
 * end-to-end test runbook can fund a freshly generated operator key without a
 * manual step.
 */
async function main() {
  const to = process.env["FUND_ADDRESS"];
  const hbar = process.env["FUND_HBAR"] ?? "20";
  if (!to) throw new Error("set FUND_ADDRESS");

  const [sender] = await ethers.getSigners();
  const tx = await sender!.sendTransaction({ to, value: ethers.parseEther(hbar) });
  await tx.wait();
  console.log(`sent ${hbar} HBAR to ${to}`);
  console.log(`balance now ${ethers.formatEther(await ethers.provider.getBalance(to))} HBAR`);
}

main().catch((error) => {
  console.error(error);
  process.exitCode = 1;
});
