// SPDX-License-Identifier: MIT
pragma solidity ^0.8.24;

/**
 * SessionEscrow — trustless, time-metered payment for one rented GPU session.
 *
 * Why this exists: ClearGate's lease flow settles a lump sum the moment a lease
 * starts, so stopping early forfeits the remainder. Refunding that without a
 * contract would mean the provider's node holding a key on the renter's behalf,
 * which is exactly the custodial model the project rejects. Here the renter's
 * deposit sits in this contract's own balance and the code — not the node, not
 * the registry, not us — decides the split.
 *
 * Three properties are load-bearing:
 *
 *  1. `settle` is PERMISSIONLESS. Anyone may call it. That is safe because
 *     calling it early only ever produces a SMALLER `elapsed`, which pays the
 *     provider less and refunds the renter more. A provider therefore gains
 *     nothing by rushing it, and a renter has every incentive to call it the
 *     moment they stop. Nobody needs to be trusted to run a keeper.
 *
 *  2. `settle` is guarded by a one-shot flag, so late and early callers can
 *     race harmlessly: the first one in wins and the second reverts.
 *
 *  3. There is no owner, no pause, no upgrade path. This contract is
 *     immutable by construction. A bug ships permanently and is fixed by
 *     deploying a new one and repointing `escrow_contract_id`. That is the
 *     deliberate trade: no privileged party can reach a renter's deposit,
 *     including us.
 *
 * `provider` and `pricePerSecond` come from the caller rather than from a
 * registry lookup, which is the same trust shape x402 already uses: the node's
 * quote states them, the renter's client echoes them into the deposit, and the
 * node independently checks the on-chain record against its own quote before it
 * provisions anything. This contract does not know what a "provider" is; it
 * only moves money between two addresses.
 */
contract SessionEscrow {
    /**
     * A note on units, because it is surprising and getting it wrong is a
     * revert on every deposit:
     *
     * Inside the Hedera EVM, `msg.value` is denominated in TINYBARS, not in
     * weibars. A JSON-RPC caller still sends weibars — the relay divides by
     * 10^10 when it builds the ContractCall — and a caller using the Hedera SDK
     * directly sets tinybars. Either way, by the time Solidity sees it, the
     * number is tinybars. Measured, not assumed: sending 1 HBAR (10^18 weibars)
     * over the relay arrives here as 100000000.
     *
     * So this contract does no unit conversion at all. Every amount below —
     * `pricePerSecond`, `deposited`, `msg.value`, and the values sent back
     * out — is tinybars, which is also the unit every price in ClearGate is
     * already quoted in.
     */
    struct Session {
        address renter;
        address provider;
        uint256 pricePerSecond; // tinybars per second, fixed at open
        uint256 startTime;      // block.timestamp at openSession
        uint256 duration;       // seconds paid for; grows on topUp
        uint256 deposited;      // total tinybars deposited so far
        bool    settled;
    }

    mapping(bytes32 => Session) private _sessions;

    event SessionOpened(
        bytes32 indexed sessionId,
        address indexed renter,
        address indexed provider,
        uint256 pricePerSecond,
        uint256 duration,
        uint256 deposited,
        uint256 startTime
    );

    event SessionToppedUp(
        bytes32 indexed sessionId,
        uint256 addedDuration,
        uint256 newDuration,
        uint256 newDeposited
    );

    event SessionSettled(
        bytes32 indexed sessionId,
        uint256 elapsed,
        uint256 providerAmount,
        uint256 refundAmount,
        address settledBy
    );

    /**
     * Owed but not delivered: a direct payout to this address failed, so the
     * amount is held for them to pull with `withdraw`. See `_pay`.
     */
    mapping(address => uint256) public pendingWithdrawal;

    event PayoutDeferred(address indexed to, uint256 tinybars);
    event Withdrawn(address indexed to, uint256 tinybars);

    error SessionExists();
    error NoSuchSession();
    error AlreadySettled();
    error ZeroAddress();
    error ZeroPrice();
    error ZeroDuration();
    /** msg.value did not equal pricePerSecond * duration, to the tinybar. */
    error WrongDeposit(uint256 expectedTinybars, uint256 sentTinybars);
    error NothingToWithdraw();
    error WithdrawFailed();

    /**
     * Opens a session. `msg.value` must be exactly pricePerSecond * duration.
     *
     * `sessionId` is minted by the provider's node at quote time, so a renter
     * cannot silently reuse one; reopening an existing id reverts rather than
     * overwriting a live session and stranding its deposit.
     */
    function openSession(
        bytes32 sessionId,
        address provider,
        uint256 pricePerSecond,
        uint256 duration
    ) external payable {
        if (_sessions[sessionId].renter != address(0)) revert SessionExists();
        if (provider == address(0)) revert ZeroAddress();
        if (pricePerSecond == 0) revert ZeroPrice();
        if (duration == 0) revert ZeroDuration();

        uint256 paid = msg.value;
        uint256 owed = pricePerSecond * duration;
        if (paid != owed) revert WrongDeposit(owed, paid);

        _sessions[sessionId] = Session({
            renter: msg.sender,
            provider: provider,
            pricePerSecond: pricePerSecond,
            startTime: block.timestamp,
            duration: duration,
            deposited: paid,
            settled: false
        });

        emit SessionOpened(
            sessionId, msg.sender, provider, pricePerSecond, duration, paid, block.timestamp
        );
    }

    /**
     * Buys more time on a live session. `msg.value` must be exactly
     * pricePerSecond * addedDuration, at the price fixed when the session
     * opened — a session's rate cannot be changed out from under either party.
     *
     * Anyone may top up, not only the renter. A session is a thing being paid
     * for, not an account, and refusing a third party's payment would buy no
     * safety: the refund still goes to the original renter either way.
     */
    function topUp(bytes32 sessionId, uint256 addedDuration) external payable {
        Session storage s = _sessions[sessionId];
        if (s.renter == address(0)) revert NoSuchSession();
        if (s.settled) revert AlreadySettled();
        if (addedDuration == 0) revert ZeroDuration();

        uint256 paid = msg.value;
        uint256 owed = s.pricePerSecond * addedDuration;
        if (paid != owed) revert WrongDeposit(owed, paid);

        s.duration += addedDuration;
        s.deposited += paid;

        emit SessionToppedUp(sessionId, addedDuration, s.duration, s.deposited);
    }

    /**
     * Ends a session and splits the deposit by elapsed wall-clock time.
     *
     * Permissionless and callable exactly once. `elapsed` is capped at the paid
     * duration, so calling after expiry pays the provider the full deposit with
     * nothing to refund — which is what makes "renter never topped up, session
     * ran out" work with no special-case code. It is the same function, just
     * called later.
     */
    function settle(bytes32 sessionId) external {
        Session storage s = _sessions[sessionId];
        if (s.renter == address(0)) revert NoSuchSession();
        if (s.settled) revert AlreadySettled();

        // Effects before interactions: the one-shot flag is what makes racing
        // callers harmless, so it must be set before any value leaves.
        s.settled = true;

        uint256 elapsed = block.timestamp - s.startTime;
        if (elapsed > s.duration) elapsed = s.duration;

        uint256 providerAmount = s.pricePerSecond * elapsed;
        // Cannot exceed `deposited`: elapsed <= duration and deposited was
        // checked to equal pricePerSecond * duration on every payment in.
        uint256 refundAmount = s.deposited - providerAmount;

        if (providerAmount > 0) _pay(s.provider, providerAmount);
        if (refundAmount > 0) _pay(s.renter, refundAmount);

        emit SessionSettled(sessionId, elapsed, providerAmount, refundAmount, msg.sender);
    }

    /** Reads a session. Returns a zeroed struct for an unknown id. */
    function getSession(bytes32 sessionId) external view returns (Session memory) {
        return _sessions[sessionId];
    }

    /**
     * What `settle` would pay right now, without settling. The node and the
     * renter's CLI both show this so neither party has to trust the other's
     * arithmetic about time already used.
     */
    function quoteSettlement(bytes32 sessionId)
        external
        view
        returns (uint256 elapsed, uint256 providerAmount, uint256 refundAmount)
    {
        Session storage s = _sessions[sessionId];
        if (s.renter == address(0)) revert NoSuchSession();

        elapsed = block.timestamp - s.startTime;
        if (elapsed > s.duration) elapsed = s.duration;
        providerAmount = s.pricePerSecond * elapsed;
        refundAmount = s.deposited - providerAmount;
    }

    /**
     * Lets an address pull a payout that could not be pushed to it.
     *
     * The counterpart to `_pay`'s fallback. Whoever is owed calls this
     * themselves once their account can receive — after clearing
     * `receiverSigRequired`, say — and nobody else can claim it for them.
     */
    function withdraw() external {
        uint256 amount = pendingWithdrawal[msg.sender];
        if (amount == 0) revert NothingToWithdraw();

        pendingWithdrawal[msg.sender] = 0;
        (bool ok, ) = payable(msg.sender).call{value: amount}("");
        if (!ok) {
            pendingWithdrawal[msg.sender] = amount;
            revert WithdrawFailed();
        }

        emit Withdrawn(msg.sender, amount);
    }

    /**
     * Pays out in tinybars, and never reverts.
     *
     * This deliberately does NOT `require(success)`. An address that cannot
     * accept a contract-initiated transfer is a real possibility on Hedera —
     * an account with `receiverSigRequired` set, or (measured on testnet) the
     * long-zero form of an account that has an EVM alias. If a failed payout
     * reverted the whole call, one misconfigured provider address would make
     * `settle` permanently impossible and strand the RENTER'S REFUND along with
     * the provider's earnings, with no way out of it — the contract is
     * immutable and `settle` is one-shot.
     *
     * So a failed push becomes a pull instead: the amount is credited to the
     * recipient, `settle` completes, and the other party is paid normally.
     * Nobody's money is ever trapped by someone else's account settings.
     */
    function _pay(address to, uint256 tinybars) private {
        (bool ok, ) = payable(to).call{value: tinybars}("");
        if (!ok) {
            pendingWithdrawal[to] += tinybars;
            emit PayoutDeferred(to, tinybars);
        }
    }
}
