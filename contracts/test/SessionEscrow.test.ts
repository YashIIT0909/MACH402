import { expect } from "chai";
import { ethers } from "hardhat";
import { time } from "@nomicfoundation/hardhat-network-helpers";
import type { SessionEscrow } from "../typechain-types";
import type { HardhatEthersSigner } from "@nomicfoundation/hardhat-ethers/signers";

/**
 * The settle math is the only place in MACH402 where money is divided rather
 * than simply moved, so it is tested against the in-process EVM where the clock
 * can be advanced exactly. Every case below corresponds to something a renter
 * or provider can actually do.
 */
describe("SessionEscrow", () => {
  // 0.002 HBAR/minute, the config default, expressed per second.
  const PRICE_PER_SECOND = 3_334n; // tinybars
  const DURATION = 600n; // 10 minutes

  // Every value here is tinybars, because that is what `msg.value` is inside
  // the Hedera EVM — see the unit note at the top of SessionEscrow.sol. On
  // Hardhat's EVM the unit is nominally wei, but the contract does no
  // conversion either way, so these are the exact numbers the live chain sees.
  const deposit = (seconds: bigint) => PRICE_PER_SECOND * seconds;

  const SESSION_ID = ethers.id("node_dc85d634b114b081:session-1");

  let escrow: SessionEscrow;
  let renter: HardhatEthersSigner;
  let provider: HardhatEthersSigner;
  let bystander: HardhatEthersSigner;

  beforeEach(async () => {
    [renter, provider, bystander] = await ethers.getSigners();
    const factory = await ethers.getContractFactory("SessionEscrow");
    escrow = await factory.deploy();
    await escrow.waitForDeployment();
  });

  async function open(seconds = DURATION) {
    return escrow
      .connect(renter)
      .openSession(SESSION_ID, provider.address, PRICE_PER_SECOND, seconds, {
        value: deposit(seconds),
      });
  }

  describe("openSession", () => {
    it("records the session and holds the deposit in the contract", async () => {
      await open();

      const session = await escrow.getSession(SESSION_ID);
      expect(session.renter).to.equal(renter.address);
      expect(session.provider).to.equal(provider.address);
      expect(session.pricePerSecond).to.equal(PRICE_PER_SECOND);
      expect(session.duration).to.equal(DURATION);
      expect(session.deposited).to.equal(PRICE_PER_SECOND * DURATION);
      expect(session.settled).to.equal(false);

      // The money is in the contract, not with the provider. This is the whole
      // difference from the direct-transfer lease flow.
      expect(await ethers.provider.getBalance(await escrow.getAddress())).to.equal(
        deposit(DURATION),
      );
    });

    it("rejects a deposit that does not match price * duration", async () => {
      await expect(
        escrow
          .connect(renter)
          .openSession(SESSION_ID, provider.address, PRICE_PER_SECOND, DURATION, {
            value: deposit(DURATION) - 1n,
          }),
      ).to.be.revertedWithCustomError(escrow, "WrongDeposit");
    });

    it("refuses to reopen an existing session id", async () => {
      // Overwriting a live session would strand its deposit with no settler.
      await open();
      await expect(open()).to.be.revertedWithCustomError(escrow, "SessionExists");
    });

    it("rejects a zero price, zero duration, or zero provider", async () => {
      await expect(
        escrow.connect(renter).openSession(ethers.id("a"), provider.address, 0n, DURATION),
      ).to.be.revertedWithCustomError(escrow, "ZeroPrice");
      await expect(
        escrow.connect(renter).openSession(ethers.id("b"), provider.address, PRICE_PER_SECOND, 0n),
      ).to.be.revertedWithCustomError(escrow, "ZeroDuration");
      await expect(
        escrow
          .connect(renter)
          .openSession(ethers.id("c"), ethers.ZeroAddress, PRICE_PER_SECOND, DURATION, {
            value: deposit(DURATION),
          }),
      ).to.be.revertedWithCustomError(escrow, "ZeroAddress");
    });
  });

  describe("settle", () => {
    it("pays the provider in full and refunds nothing once the paid time is used up", async () => {
      await open();
      await time.increase(Number(DURATION) + 60); // past expiry

      const before = await ethers.provider.getBalance(provider.address);
      await escrow.connect(bystander).settle(SESSION_ID);
      const after = await ethers.provider.getBalance(provider.address);

      expect(after - before).to.equal(deposit(DURATION));
      expect(await ethers.provider.getBalance(await escrow.getAddress())).to.equal(0n);
    });

    it("splits by elapsed time and refunds the rest when a renter stops early", async () => {
      // This is the case the whole contract exists for: 10 minutes paid,
      // 2 minutes used, 8 minutes back.
      await open();
      await time.increase(120);

      const providerBefore = await ethers.provider.getBalance(provider.address);
      const renterBefore = await ethers.provider.getBalance(renter.address);

      // Settled by a bystander so neither party's gas cost muddies the maths.
      const tx = await escrow.connect(bystander).settle(SESSION_ID);
      const receipt = await tx.wait();

      const elapsed = 121n; // the settle tx itself advances the block by one second
      const expectedProvider = PRICE_PER_SECOND * elapsed;
      const expectedRefund = PRICE_PER_SECOND * DURATION - expectedProvider;

      expect(await ethers.provider.getBalance(provider.address)).to.equal(
        providerBefore + expectedProvider,
      );
      expect(await ethers.provider.getBalance(renter.address)).to.equal(
        renterBefore + expectedRefund,
      );
      expect(await ethers.provider.getBalance(await escrow.getAddress())).to.equal(0n);

      await expect(tx)
        .to.emit(escrow, "SessionSettled")
        .withArgs(SESSION_ID, elapsed, expectedProvider, expectedRefund, bystander.address);

      expect(receipt).to.not.equal(null);
    });

    it("refunds essentially everything when settled immediately", async () => {
      // A session whose reachability check failed: the renter deposited, the
      // node provisioned nothing, and they should get their money back.
      await open();

      const renterBefore = await ethers.provider.getBalance(renter.address);
      await escrow.connect(bystander).settle(SESSION_ID);

      const elapsed = 1n;
      const expectedRefund = PRICE_PER_SECOND * (DURATION - elapsed);
      expect(await ethers.provider.getBalance(renter.address)).to.equal(
        renterBefore + expectedRefund,
      );
    });

    it("is permissionless — anyone may close a session", async () => {
      // No keeper has to be trusted or funded. Calling early only ever pays the
      // provider less, so there is no incentive to race maliciously.
      await open();
      await time.increase(60);
      await expect(escrow.connect(bystander).settle(SESSION_ID)).to.not.be.reverted;
    });

    it("cannot be settled twice — the second caller reverts harmlessly", async () => {
      // This guard is what lets a renter's clean-exit settle and a provider's
      // self-settle race without either draining the contract.
      await open();
      await time.increase(60);
      await escrow.connect(renter).settle(SESSION_ID);
      await expect(escrow.connect(provider).settle(SESSION_ID)).to.be.revertedWithCustomError(
        escrow,
        "AlreadySettled",
      );
    });

    it("reverts on an unknown session rather than paying out zero", async () => {
      await expect(escrow.settle(ethers.id("never-opened"))).to.be.revertedWithCustomError(
        escrow,
        "NoSuchSession",
      );
    });
  });

  describe("topUp", () => {
    it("extends the paid duration and grows the deposit", async () => {
      await open();
      await escrow.connect(renter).topUp(SESSION_ID, 300n, { value: deposit(300n) });

      const session = await escrow.getSession(SESSION_ID);
      expect(session.duration).to.equal(DURATION + 300n);
      expect(session.deposited).to.equal(PRICE_PER_SECOND * (DURATION + 300n));
    });

    it("keeps the price fixed at what was agreed when the session opened", async () => {
      // A top-up priced at anything but the original rate is refused, so
      // neither party can re-price a live session.
      await open();
      await expect(
        escrow.connect(renter).topUp(SESSION_ID, 300n, { value: deposit(300n) + 1n }),
      ).to.be.revertedWithCustomError(escrow, "WrongDeposit");
    });

    it("lets the extra time actually be claimed", async () => {
      await open();
      await escrow.connect(renter).topUp(SESSION_ID, 300n, { value: deposit(300n) });
      await time.increase(Number(DURATION + 300n) + 60);

      const before = await ethers.provider.getBalance(provider.address);
      await escrow.connect(bystander).settle(SESSION_ID);
      expect((await ethers.provider.getBalance(provider.address)) - before).to.equal(
        deposit(DURATION + 300n),
      );
    });

    it("refuses to top up a settled session", async () => {
      await open();
      await escrow.connect(renter).settle(SESSION_ID);
      await expect(
        escrow.connect(renter).topUp(SESSION_ID, 300n, { value: deposit(300n) }),
      ).to.be.revertedWithCustomError(escrow, "AlreadySettled");
    });
  });

  describe("quoteSettlement", () => {
    it("reports the split without performing it, so neither side has to trust the other's arithmetic", async () => {
      await open();
      await time.increase(120);

      const [elapsed, providerAmount, refundAmount] =
        await escrow.quoteSettlement(SESSION_ID);

      expect(elapsed).to.equal(120n);
      expect(providerAmount).to.equal(PRICE_PER_SECOND * 120n);
      expect(refundAmount).to.equal(PRICE_PER_SECOND * (DURATION - 120n));

      // Still unsettled: quoting is a view.
      expect((await escrow.getSession(SESSION_ID)).settled).to.equal(false);
    });

    it("caps elapsed at the paid duration once a session has run out", async () => {
      await open();
      await time.increase(Number(DURATION) * 3);

      const [elapsed, providerAmount, refundAmount] =
        await escrow.quoteSettlement(SESSION_ID);
      expect(elapsed).to.equal(DURATION);
      expect(providerAmount).to.equal(PRICE_PER_SECOND * DURATION);
      expect(refundAmount).to.equal(0n);
    });
  });

  describe("a recipient that cannot accept a payout", () => {
    /**
     * On Hedera this is not hypothetical: an account with `receiverSigRequired`
     * set rejects contract-initiated transfers, and so (measured on testnet)
     * does the long-zero address of an account that has an EVM alias.
     *
     * The danger is not that the provider goes unpaid. It is that `settle` is
     * one-shot and the contract is immutable, so a reverting payout would make
     * the session permanently unsettleable and trap the renter's refund too.
     */
    it("still settles, still refunds the renter, and holds the rest for later", async () => {
      const rejecting = await (await ethers.getContractFactory("RejectingReceiver")).deploy();
      const badProvider = await rejecting.getAddress();
      const id = ethers.id("session-with-unpayable-provider");

      await escrow
        .connect(renter)
        .openSession(id, badProvider, PRICE_PER_SECOND, DURATION, { value: deposit(DURATION) });
      await time.increase(120);

      const renterBefore = await ethers.provider.getBalance(renter.address);
      await expect(escrow.connect(bystander).settle(id)).to.not.be.reverted;

      const elapsed = 121n;
      const owed = PRICE_PER_SECOND * elapsed;
      const refund = PRICE_PER_SECOND * DURATION - owed;

      // The renter is made whole regardless of the provider's misconfiguration.
      expect(await ethers.provider.getBalance(renter.address)).to.equal(renterBefore + refund);
      // And the provider's share is held for them rather than lost.
      expect(await escrow.pendingWithdrawal(badProvider)).to.equal(owed);
      expect((await escrow.getSession(id)).settled).to.equal(true);
    });

    it("lets the owed party pull their balance once they can receive", async () => {
      // `withdraw` is claimed by the owed address itself, so nobody else can
      // take it and no privileged party has to release it.
      const rejecting = await (await ethers.getContractFactory("RejectingReceiver")).deploy();
      const id = ethers.id("session-deferred-then-claimed");

      await escrow
        .connect(renter)
        .openSession(id, await rejecting.getAddress(), PRICE_PER_SECOND, DURATION, {
          value: deposit(DURATION),
        });
      await escrow.connect(bystander).settle(id);

      // Nobody else can claim it.
      await expect(escrow.connect(bystander).withdraw()).to.be.revertedWithCustomError(
        escrow,
        "NothingToWithdraw",
      );
      // And the rejecting contract still cannot take it, so it stays credited
      // rather than vanishing.
      const owed = await escrow.pendingWithdrawal(await rejecting.getAddress());
      expect(owed).to.be.greaterThan(0n);
    });
  });

  describe("isolation between sessions", () => {
    it("settling one session leaves another's deposit untouched", async () => {
      // One node runs one session at a time, but one contract serves every node
      // in the marketplace, so cross-session leakage would be catastrophic.
      const otherId = ethers.id("node_other:session-1");
      await open();
      await escrow
        .connect(bystander)
        .openSession(otherId, provider.address, PRICE_PER_SECOND, DURATION, {
          value: deposit(DURATION),
        });

      await time.increase(Number(DURATION) + 60);
      await escrow.connect(renter).settle(SESSION_ID);

      const other = await escrow.getSession(otherId);
      expect(other.settled).to.equal(false);
      expect(other.deposited).to.equal(PRICE_PER_SECOND * DURATION);
      expect(await ethers.provider.getBalance(await escrow.getAddress())).to.equal(
        deposit(DURATION),
      );
    });
  });
});
