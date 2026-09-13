package escrow

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/YashIIT0909/MACH402/agent/internal/mirror"
)

// The ABI decoding in this package is hand-written against a struct layout, and
// the mirror node's exact response shape is the one thing a unit test with
// hand-built fixtures cannot prove. This reads a real session from a real
// deployment.
//
// Skipped unless pointed at one, following the same rule as the sshca and
// runner tests: exercise the real dependency when it is available rather than
// prove a mock agrees with itself.
//
//	CLEARGATE_ESCROW_CONTRACT=0x... CLEARGATE_ESCROW_SESSION=0x... go test ./internal/escrow/
func TestLiveSessionDecodes(t *testing.T) {
	contract := os.Getenv("CLEARGATE_ESCROW_CONTRACT")
	sessionID := os.Getenv("CLEARGATE_ESCROW_SESSION")
	if contract == "" || sessionID == "" {
		t.Skip("set CLEARGATE_ESCROW_CONTRACT and CLEARGATE_ESCROW_SESSION to run against a real deployment")
	}

	mirrorURL := os.Getenv("CLEARGATE_MIRROR_URL")
	if mirrorURL == "" {
		mirrorURL = mirror.DefaultURL
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	verifier := NewVerifier(mirror.New(mirrorURL, 20*time.Second), contract)
	session, err := verifier.Session(ctx, sessionID)
	if err != nil {
		t.Fatalf("read session: %v", err)
	}

	// A session that exists has a renter and a deposit; a zeroed struct here
	// would mean the decode silently produced nothing rather than failing.
	if session.Renter == "" || session.Renter == "0x0000000000000000000000000000000000000000" {
		t.Errorf("decoded no renter: %+v", session)
	}
	if session.Deposited.Sign() == 0 {
		t.Errorf("decoded a zero deposit: %+v", session)
	}
	if session.PricePerSecond.Sign() == 0 {
		t.Errorf("decoded a zero price: %+v", session)
	}
	if session.Duration.Sign() == 0 {
		t.Errorf("decoded a zero duration: %+v", session)
	}

	t.Logf("renter=%s provider=%s price=%s/s duration=%ss deposited=%s settled=%v",
		session.Renter, session.Provider, session.PricePerSecond,
		session.Duration, session.Deposited, session.Settled)
}

// ResolveProviderAddress must read the alias from the mirror node rather than
// deriving a long-zero address, because a contract cannot pay the long-zero
// form of an account that has an alias. This checks it returns something that
// actually looks like an alias for a real account.
func TestLiveResolveProviderAddress(t *testing.T) {
	payTo := os.Getenv("CLEARGATE_PAY_TO")
	if payTo == "" {
		t.Skip("set CLEARGATE_PAY_TO to a real account to run this")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	address, err := ResolveProviderAddress(ctx, mirror.New("", 20*time.Second), payTo)
	if err != nil {
		t.Fatalf("resolve %s: %v", payTo, err)
	}
	if len(address) != 42 {
		t.Fatalf("resolved %q, which is not an EVM address", address)
	}
	// The long-zero form starts with a long run of zeros; an alias does not.
	// Returning long-zero here would be the bug this function exists to avoid.
	if address[:26] == "0x000000000000000000000000" {
		t.Errorf("resolved the long-zero address %s, which a contract cannot pay", address)
	}
	t.Logf("%s -> %s", payTo, address)
}
