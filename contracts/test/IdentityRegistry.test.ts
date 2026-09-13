import { expect } from "chai";
import { ethers } from "hardhat";
import type { IdentityRegistry } from "../typechain-types";
import type { HardhatEthersSigner } from "@nomicfoundation/hardhat-ethers/signers";

/**
 * The point of putting identity on-chain rather than in our Postgres is that
 * MACH402 cannot mint, move or revoke one. Most of these tests exist to prove
 * that property rather than to check the happy path.
 */
describe("IdentityRegistry", () => {
  const DOMAIN = "node.example.com";
  const OTHER_DOMAIN = "other.example.com";

  let registry: IdentityRegistry;
  let providerA: HardhatEthersSigner;
  let providerB: HardhatEthersSigner;

  beforeEach(async () => {
    [providerA, providerB] = await ethers.getSigners();
    const factory = await ethers.getContractFactory("IdentityRegistry");
    registry = await factory.deploy();
    await registry.waitForDeployment();
  });

  it("issues ids starting at 1, so 0 stays usable as 'not registered'", async () => {
    // config.yaml carries `identity.agent_id: 0` for an unregistered node, so
    // a real id of 0 would be indistinguishable from never having registered.
    expect(await registry.agentIdOf(providerA.address)).to.equal(0n);

    await registry.connect(providerA).newAgent(DOMAIN, providerA.address);
    expect(await registry.agentIdOf(providerA.address)).to.equal(1n);

    await registry.connect(providerB).newAgent(OTHER_DOMAIN, providerB.address);
    expect(await registry.agentIdOf(providerB.address)).to.equal(2n);
    expect(await registry.agentCount()).to.equal(2n);
  });

  it("records the domain and address, and emits the registration", async () => {
    await expect(registry.connect(providerA).newAgent(DOMAIN, providerA.address))
      .to.emit(registry, "AgentRegistered")
      .withArgs(1n, DOMAIN, providerA.address);

    const agent = await registry.getAgent(1n);
    expect(agent.agentId).to.equal(1n);
    expect(agent.agentDomain).to.equal(DOMAIN);
    expect(agent.agentAddress).to.equal(providerA.address);
  });

  it("refuses to register an identity on someone else's behalf", async () => {
    // This is the load-bearing check: it is what stops MACH402 — or anyone —
    // from minting identities for providers and then speaking for them.
    await expect(
      registry.connect(providerA).newAgent(DOMAIN, providerB.address),
    ).to.be.revertedWithCustomError(registry, "NotAgentAddress");
  });

  it("refuses to let two providers claim one hostname", async () => {
    // The domain is where the agent card is served, so a duplicate would let
    // one provider answer for another's identity.
    await registry.connect(providerA).newAgent(DOMAIN, providerA.address);
    await expect(
      registry.connect(providerB).newAgent(DOMAIN, providerB.address),
    ).to.be.revertedWithCustomError(registry, "DomainTaken");
  });

  it("refuses a second identity for one address", async () => {
    // Registering twice would orphan the first id. The node checks agentIdOf
    // before registering, and this is the backstop if it does not.
    await registry.connect(providerA).newAgent(DOMAIN, providerA.address);
    await expect(
      registry.connect(providerA).newAgent(OTHER_DOMAIN, providerA.address),
    ).to.be.revertedWithCustomError(registry, "AddressTaken");
  });

  it("rejects an empty domain", async () => {
    await expect(
      registry.connect(providerA).newAgent("", providerA.address),
    ).to.be.revertedWithCustomError(registry, "EmptyDomain");
  });

  describe("resolution", () => {
    beforeEach(async () => {
      await registry.connect(providerA).newAgent(DOMAIN, providerA.address);
    });

    it("resolves by id, by domain and by address to the same record", async () => {
      const byId = await registry.getAgent(1n);
      const byDomain = await registry.resolveByDomain(DOMAIN);
      const byAddress = await registry.resolveByAddress(providerA.address);

      for (const found of [byId, byDomain, byAddress]) {
        expect(found.agentId).to.equal(1n);
        expect(found.agentDomain).to.equal(DOMAIN);
        expect(found.agentAddress).to.equal(providerA.address);
      }
    });

    it("reverts rather than returning a zeroed record for an unknown agent", async () => {
      await expect(registry.getAgent(99n)).to.be.revertedWithCustomError(registry, "NoSuchAgent");
      await expect(registry.resolveByDomain("nope.example")).to.be.revertedWithCustomError(
        registry,
        "NoSuchAgent",
      );
      await expect(registry.resolveByAddress(providerB.address)).to.be.revertedWithCustomError(
        registry,
        "NoSuchAgent",
      );
    });

    it("agentIdOf answers 0 instead of reverting, so `register` can ask safely", async () => {
      expect(await registry.agentIdOf(providerB.address)).to.equal(0n);
    });
  });

  describe("updateAgent", () => {
    beforeEach(async () => {
      await registry.connect(providerA).newAgent(DOMAIN, providerA.address);
    });

    it("lets a provider re-home without losing their identity", async () => {
      await expect(registry.connect(providerA).updateAgent(1n, OTHER_DOMAIN, providerA.address))
        .to.emit(registry, "AgentUpdated")
        .withArgs(1n, OTHER_DOMAIN, providerA.address);

      expect((await registry.resolveByDomain(OTHER_DOMAIN)).agentId).to.equal(1n);
      // The old hostname is released, not left pointing at them.
      await expect(registry.resolveByDomain(DOMAIN)).to.be.revertedWithCustomError(
        registry,
        "NoSuchAgent",
      );
    });

    it("lets a provider rotate their operator key and keep their id", async () => {
      await registry.connect(providerA).updateAgent(1n, DOMAIN, providerB.address);

      expect(await registry.agentIdOf(providerB.address)).to.equal(1n);
      expect(await registry.agentIdOf(providerA.address)).to.equal(0n);
      // And the new key is now the only one that can update it.
      await expect(
        registry.connect(providerA).updateAgent(1n, OTHER_DOMAIN, providerA.address),
      ).to.be.revertedWithCustomError(registry, "NotAgentOwner");
    });

    it("refuses an update from anyone but the current agent address", async () => {
      await expect(
        registry.connect(providerB).updateAgent(1n, OTHER_DOMAIN, providerB.address),
      ).to.be.revertedWithCustomError(registry, "NotAgentOwner");
    });

    it("refuses to move onto a hostname another provider already holds", async () => {
      await registry.connect(providerB).newAgent(OTHER_DOMAIN, providerB.address);
      await expect(
        registry.connect(providerA).updateAgent(1n, OTHER_DOMAIN, providerA.address),
      ).to.be.revertedWithCustomError(registry, "DomainTaken");
    });

    it("is a no-op-safe rewrite when nothing actually changes", async () => {
      // `register` re-run after a config loss may call this with identical
      // values; it must not trip the uniqueness guards against the record's own
      // existing domain and address.
      await expect(registry.connect(providerA).updateAgent(1n, DOMAIN, providerA.address)).to.not
        .be.reverted;
      expect((await registry.getAgent(1n)).agentDomain).to.equal(DOMAIN);
    });
  });
});
