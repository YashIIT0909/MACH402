// Package escrow verifies that a renter really deposited for a session.
//
// It is the escrow path's answer to the facilitator's /verify. In the direct
// x402 flow the node hands a signed transfer to the facilitator and is told
// whether it is good; here the renter has already submitted their own contract
// call, so there is nothing to hand anyone — the node reads Hedera's public
// mirror node and checks the result itself.
//
// Nothing in this package signs, holds a key, or links a Hedera SDK. Verifying
// a payment turns out to need none of those, which is the property that lets
// escrow mode exist without breaking CLAUDE.md invariant 1.
//
// The discipline from the session path carries over unchanged: the node
// checks the on-chain record against the quote IT issued, never against what
// the renter says they sent. A renter controls the transaction id they present
// and every argument inside it; the only thing they cannot forge is what the
// contract actually stored.
//
// PARKED. Nothing on the default session path calls this any more: sessions are
// now bought with an ordinary x402 chunk payment and metered down by the node,
// with the refund-owed figure published continuously to the provider's audit
// topic. This package, `client/src/escrow.ts` and `contracts/SessionEscrow.sol`
// are kept together as the harder-guarantee fallback a future
// `payment_mode: escrow-vault` could reactivate — the guarantee it gives is
// genuinely stronger (the provider never holds the renter's money at all) and
// deleting it would foreclose that for no benefit. `PricePerSecond` below is
// still live: it is the one piece of arithmetic both modes share.
package escrow

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"time"

	"github.com/YashIIT0909/ClearGate/agent/internal/mirror"
)

// Verifier reads sessions and deposits from the chain.
type Verifier struct {
	mirror   *mirror.Client
	contract string
}

func NewVerifier(m *mirror.Client, contractAddress string) *Verifier {
	return &Verifier{mirror: m, contract: contractAddress}
}

// Contract is the escrow contract this verifier reads.
func (v *Verifier) Contract() string { return v.contract }

// Quote is what the node offered, and what the deposit has to match.
//
// Every field is rebuilt from the node's own config and its own minted session
// id — none of it comes from the renter's request.
type Quote struct {
	SessionID       string
	ProviderAddress string
	PricePerSecond  *big.Int
	MinSeconds      int64
}

// ErrNotYetVisible means the deposit has not reached the mirror node yet.
//
// Distinct from a rejection because it is the ordinary case in the first few
// seconds after a transaction: the caller waits and asks again rather than
// telling the renter their payment was bad.
var ErrNotYetVisible = errors.New("deposit not yet visible on the mirror node")

// Rejection is a verified-and-refused deposit: it exists on chain and does not
// match the quote. The reason is safe to show a renter — every part of it is
// already public — and saying which field disagreed is what lets them fix it.
type Rejection struct {
	Reason string
}

func (r *Rejection) Error() string { return r.Reason }

func rejectf(format string, args ...any) error {
	return &Rejection{Reason: fmt.Sprintf(format, args...)}
}

// Session reads a session's current on-chain state.
func (v *Verifier) Session(ctx context.Context, sessionID string) (*Session, error) {
	callData, err := encodeGetSession(sessionID)
	if err != nil {
		return nil, fmt.Errorf("encode getSession: %w", err)
	}

	raw, err := v.mirror.Call(ctx, v.contract, callData)
	if err != nil {
		return nil, fmt.Errorf("read session %s: %w", sessionID, err)
	}

	session, err := decodeSession(raw)
	if err != nil {
		return nil, fmt.Errorf("decode session %s: %w", sessionID, err)
	}
	return session, nil
}

// VerifyDeposit confirms a renter funded the session the node quoted.
//
// Checks, in order of what they protect against:
//
//  1. the transaction succeeded — a reverted call moved no money;
//  2. it called THIS escrow contract, not a look-alike the renter deployed;
//  3. it called openSession, not some other function that also costs gas;
//  4. the on-chain session's provider is this node's pay_to address, so a
//     renter cannot fund someone else's session and present it here;
//  5. the price matches what was quoted, so the deposit cannot be made at a
//     rate the node never offered;
//  6. enough time was bought to meet the node's minimum;
//  7. the session is not already settled, so a closed session cannot be
//     replayed to obtain a second container.
//
// Points 4-7 read the contract's own storage rather than the transaction's
// arguments, so they hold regardless of what the renter encoded.
func (v *Verifier) VerifyDeposit(ctx context.Context, txID string, quote Quote) (*Session, error) {
	result, err := v.mirror.ContractResultByTransaction(ctx, txID)
	if err != nil {
		if errors.Is(err, mirror.ErrNotFound) {
			return nil, ErrNotYetVisible
		}
		return nil, fmt.Errorf("read deposit %s: %w", txID, err)
	}

	if result.Result != "SUCCESS" {
		return nil, rejectf("the deposit transaction did not succeed (%s%s)",
			result.Result, detail(result.ErrorMessage))
	}

	if !sameAddress(result.Address, v.contract) && result.ContractID != v.contract {
		return nil, rejectf("that transaction called %s, not this node's escrow contract %s",
			result.Address, v.contract)
	}

	selector, _, err := callArguments(result.FunctionParameters)
	if err != nil {
		return nil, rejectf("could not read the deposit's call data: %v", err)
	}
	if selector != selectorOpenSession {
		return nil, rejectf("that transaction is not an openSession call")
	}

	session, err := v.Session(ctx, quote.SessionID)
	if err != nil {
		return nil, err
	}
	// A session that was never opened decodes as a zeroed struct rather than an
	// error, so an unopened id looks like a renter of address zero.
	if session.Deposited.Sign() == 0 {
		return nil, ErrNotYetVisible
	}

	if !sameAddress(session.Provider, quote.ProviderAddress) {
		return nil, rejectf("that session pays %s, not this node's account %s",
			session.Provider, quote.ProviderAddress)
	}
	if session.PricePerSecond.Cmp(quote.PricePerSecond) != 0 {
		return nil, rejectf("that session is priced at %s tinybars/second; this node quoted %s",
			session.PricePerSecond, quote.PricePerSecond)
	}
	if session.Duration.Int64() < quote.MinSeconds {
		return nil, rejectf("that session buys %d seconds; this node's minimum is %d",
			session.Duration.Int64(), quote.MinSeconds)
	}
	if session.Settled {
		return nil, rejectf("that session has already been settled")
	}

	return session, nil
}

// AwaitDeposit is VerifyDeposit with a bounded retry for mirror-node lag.
//
// Ingestion takes a few seconds on testnet, so a single lookup would reject
// perfectly good payments for being early. Unlike the direct x402 flow there is
// no facilitator deadline to race — the deposit is already final on consensus —
// so the only cost of waiting is a slower response.
//
// A Rejection stops the loop immediately: a deposit that exists and does not
// match will not start matching.
func (v *Verifier) AwaitDeposit(ctx context.Context, txID string, quote Quote) (*Session, error) {
	delay := 500 * time.Millisecond
	const attempts = 6

	var lastErr error
	for attempt := 0; attempt < attempts; attempt++ {
		session, err := v.VerifyDeposit(ctx, txID, quote)
		if err == nil {
			return session, nil
		}

		var rejection *Rejection
		if errors.As(err, &rejection) {
			return nil, err
		}
		lastErr = err

		if attempt == attempts-1 {
			break
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(delay):
		}
		if delay < 4*time.Second {
			delay *= 2
		}
	}

	return nil, fmt.Errorf("deposit %s never became visible: %w", txID, lastErr)
}

// AwaitTopUp waits for the chain to show more paid time than the node has.
//
// The same bounded retry AwaitDeposit uses, and needed for the same reason: a
// renter's top-up is final on consensus before they tell the node about it, but
// the mirror node takes a few seconds to catch up. Reading once and refusing
// would reject a payment that has already happened — and would do it at the
// worst possible moment, with the renter's session about to freeze.
//
// `afterSeconds` is the paid duration the node currently believes in; the loop
// ends when the contract reports more than that.
func (v *Verifier) AwaitTopUp(ctx context.Context, sessionID string, afterSeconds int64) (*Session, error) {
	delay := 500 * time.Millisecond
	const attempts = 6

	var last *Session
	for attempt := 0; attempt < attempts; attempt++ {
		session, err := v.Session(ctx, sessionID)
		if err != nil {
			return nil, err
		}
		if session.Settled {
			return session, rejectf("that session has already been settled")
		}
		if session.Duration.Int64() > afterSeconds {
			return session, nil
		}
		last = session

		if attempt == attempts-1 {
			break
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(delay):
		}
		if delay < 4*time.Second {
			delay *= 2
		}
	}

	return last, ErrNoAdditionalTime
}

// ErrNoAdditionalTime means the chain still shows the same paid duration.
//
// Distinct from a rejection: the top-up may simply not have been made yet, which
// is a thing the renter can fix, rather than a deposit that is wrong.
var ErrNoAdditionalTime = errors.New("no additional paid time is visible on-chain")

func detail(message string) string {
	if message == "" {
		return ""
	}
	return ": " + message
}
