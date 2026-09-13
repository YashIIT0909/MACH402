package escrow

import (
	"context"
	"errors"
	"fmt"
	"math/big"

	"github.com/YashIIT0909/MACH402/agent/internal/mirror"
)

// PricePerSecond converts a per-minute lease price into the per-second figure
// the contract settles with.
//
// Rounds UP, and the direction matters. A price that does not divide by 60
// rounded down would have the node quote less per second than it means to
// charge per minute, and the contract — not the node — does the final
// multiplication, so the node would simply be paid less than its configured
// rate with no error anywhere.
//
// math/big rather than int64 for the same reason leasePrice uses it: tinybar
// amounts are strings end to end in MACH402 and a price that overflows is a
// bug that silently undercharges.
func PricePerSecond(tinybarsPerMinute string) (*big.Int, error) {
	perMinute, ok := new(big.Int).SetString(tinybarsPerMinute, 10)
	if !ok {
		return nil, fmt.Errorf("not an integer tinybar price: %q", tinybarsPerMinute)
	}
	if perMinute.Sign() <= 0 {
		return nil, errors.New("lease price must be positive")
	}

	perSecond, remainder := new(big.Int).QuoRem(perMinute, big.NewInt(60), new(big.Int))
	if remainder.Sign() != 0 {
		perSecond.Add(perSecond, big.NewInt(1))
	}
	// A per-minute price under 60 tinybars would round to zero, and the contract
	// rejects a zero price outright.
	if perSecond.Sign() == 0 {
		perSecond.SetInt64(1)
	}
	return perSecond, nil
}

// Deposit is what a session of the given length costs, in tinybars.
func Deposit(pricePerSecond *big.Int, seconds int64) *big.Int {
	return new(big.Int).Mul(pricePerSecond, big.NewInt(seconds))
}

// ResolveProviderAddress finds the EVM address the escrow contract can pay
// this node's pay_to account at.
//
// It must be read from the mirror node, never derived. The obvious derivation
// is the "long-zero" address — 0x followed by the zero-padded account number —
// and a contract's HBAR transfer to that form FAILS for any account that has an
// EVM alias, which is every account the Hedera portal issues. Measured on
// testnet: paying the alias succeeded, paying the long-zero form of the same
// account reverted.
//
// Also checks receiverSigRequired here rather than at payout time. An account
// with that flag cannot be paid by a contract, and finding out at setup is a
// configuration error a provider can fix; finding out when a session settles
// would strand real money.
func ResolveProviderAddress(ctx context.Context, m *mirror.Client, payTo string) (string, error) {
	account, err := m.Account(ctx, payTo)
	if err != nil {
		if errors.Is(err, mirror.ErrNotFound) {
			return "", fmt.Errorf("pay_to account %s does not exist on this network", payTo)
		}
		return "", fmt.Errorf("look up pay_to %s: %w", payTo, err)
	}

	if account.EVMAddress == "" {
		return "", fmt.Errorf(
			"pay_to account %s has no EVM address, so the escrow contract cannot pay it; "+
				"use an ECDSA account for escrow mode", payTo)
	}
	if account.ReceiverSigRequired {
		return "", fmt.Errorf(
			"pay_to account %s has receiverSigRequired set, so a contract cannot pay it; "+
				"clear that flag or use a different pay_to for escrow mode", payTo)
	}

	return account.EVMAddress, nil
}
