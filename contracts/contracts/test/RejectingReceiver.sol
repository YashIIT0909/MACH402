// SPDX-License-Identifier: MIT
pragma solidity ^0.8.24;

/**
 * A recipient that refuses HBAR, for testing SessionEscrow's payout fallback.
 *
 * It stands in for the real-world cases that cannot be reproduced on Hardhat's
 * EVM: a Hedera account with `receiverSigRequired` set, or the long-zero form of
 * an account that has an EVM alias. Both reject a contract-initiated transfer
 * the same way this does.
 */
contract RejectingReceiver {
    receive() external payable {
        revert("no thanks");
    }
}
