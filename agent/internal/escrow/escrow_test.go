package escrow

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/YashIIT0909/ClearGate/agent/internal/mirror"
)

// The four selectors are constants in abi.go because computing them would mean
// linking Keccak-256 into a binary that deliberately carries no crypto. That
// makes them the kind of constant that rots silently when a contract signature
// changes, so this pins them to a fixture regenerated from the Solidity source
// by `make escrow-selectors`.
func TestSelectorsMatchABI(t *testing.T) {
	raw, err := os.ReadFile("testdata/selectors.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	var fixture map[string]string
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatalf("decode fixture: %v", err)
	}

	for signature, want := range map[string]string{
		"openSession(bytes32,address,uint256,uint256)": selectorOpenSession,
		"topUp(bytes32,uint256)":                       selectorTopUp,
		"getSession(bytes32)":                          selectorGetSession,
		"settle(bytes32)":                              selectorSettle,
	} {
		got, ok := fixture[signature]
		if !ok {
			t.Errorf("fixture has no entry for %s; regenerate with `make escrow-selectors`", signature)
			continue
		}
		if got != want {
			t.Errorf("selector for %s is %s in the contract but %s in abi.go", signature, got, want)
		}
	}
}

// Rounding up is not cosmetic. The contract multiplies the per-second price by
// elapsed seconds, so a price rounded DOWN would quietly pay the provider less
// than their configured per-minute rate, with no error raised anywhere.
func TestPricePerSecondRoundsUp(t *testing.T) {
	cases := []struct {
		perMinute string
		want      int64
		why       string
	}{
		{"200000", 3334, "200000/60 is 3333.33; rounding down would undercharge"},
		{"120000", 2000, "divides evenly"},
		{"60", 1, "the smallest price that still divides to a whole tinybar"},
		{"1", 1, "a sub-60 price must not round to zero: the contract rejects a zero price"},
		{"59", 1, "same, just under the boundary"},
	}

	for _, tc := range cases {
		got, err := PricePerSecond(tc.perMinute)
		if err != nil {
			t.Fatalf("PricePerSecond(%q): %v", tc.perMinute, err)
		}
		if got.Int64() != tc.want {
			t.Errorf("PricePerSecond(%q) = %s, want %d (%s)", tc.perMinute, got, tc.want, tc.why)
		}
	}
}

func TestPricePerSecondRejectsNonsense(t *testing.T) {
	for _, bad := range []string{"", "0", "-100", "1.5", "abc"} {
		if _, err := PricePerSecond(bad); err == nil {
			t.Errorf("PricePerSecond(%q) should have failed", bad)
		}
	}
}

// The whole point of reading the contract's own storage rather than the
// renter's call arguments is that storage cannot be forged. This checks the
// decode is right, since everything downstream trusts it.
func TestDecodeSession(t *testing.T) {
	encoded := encodeWords(
		padAddress("cb41addf66bc21045fec301dde313e7f336ad942"),
		padAddress("f70ccc8f7f8771f8dced650fb5ce8279045e3e23"),
		padUint(3334),
		padUint(1757600000),
		padUint(600),
		padUint(2000400),
		padUint(0),
	)

	session, err := decodeSession(encoded)
	if err != nil {
		t.Fatalf("decodeSession: %v", err)
	}

	if !sameAddress(session.Renter, "0xcb41addf66bc21045fec301dde313e7f336ad942") {
		t.Errorf("renter = %s", session.Renter)
	}
	if !sameAddress(session.Provider, "0xF70CCC8F7F8771F8DCED650FB5CE8279045E3E23") {
		t.Errorf("provider = %s", session.Provider)
	}
	if session.PricePerSecond.Int64() != 3334 {
		t.Errorf("price = %s", session.PricePerSecond)
	}
	if session.Duration.Int64() != 600 {
		t.Errorf("duration = %s", session.Duration)
	}
	if session.Settled {
		t.Error("session should not be settled")
	}
	if got, want := session.ExpiresAt(), int64(1757600600); got != want {
		t.Errorf("ExpiresAt = %d, want %d", got, want)
	}
}

// Hedera's mirror has returned the struct both bare and behind a tuple offset.
// Decoding must survive either rather than rejecting a valid session.
func TestDecodeSessionAcceptsOffsetPrefixedTuple(t *testing.T) {
	body := encodeWords(
		padAddress("cb41addf66bc21045fec301dde313e7f336ad942"),
		padAddress("f70ccc8f7f8771f8dced650fb5ce8279045e3e23"),
		padUint(1), padUint(2), padUint(3), padUint(4), padUint(1),
	)
	withOffset := append(mustHex(padUint(32)), body...)

	session, err := decodeSession(withOffset)
	if err != nil {
		t.Fatalf("decodeSession with offset: %v", err)
	}
	if !session.Settled {
		t.Error("settled flag lost when an offset word is present")
	}
}

// Addresses reach this node from its own config, from the mirror node and from
// call data, each with different EIP-55 casing. Comparing them literally would
// reject matching addresses.
func TestSameAddressIgnoresCasingAndPrefix(t *testing.T) {
	if !sameAddress("0xF70CCc8f7F8771F8dceD650fb5Ce8279045E3E23", "f70ccc8f7f8771f8dced650fb5ce8279045e3e23") {
		t.Error("casing or the 0x prefix should not change the comparison")
	}
	if sameAddress("", "") {
		t.Error("two empty addresses must not compare equal; that would make an unset provider match")
	}
	if sameAddress("0xaaaa", "0xbbbb") {
		t.Error("different addresses compared equal")
	}
}

// A deposit that exists and does not match the quote is a Rejection, which the
// retry loop must treat as final — retrying it would just make the renter wait
// for an answer that will not change.
func TestAwaitDepositStopsOnRejection(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if strings.HasPrefix(r.URL.Path, "/api/v1/contracts/results/") {
			writeJSON(w, map[string]any{
				"result":       "CONTRACT_REVERT_EXECUTED",
				"address":      "0xd583d91742f8888f5b106c3f3c854eacd7db7143",
				"contract_id":  "0.0.1",
				"function_parameters": "0x" + selectorOpenSession,
			})
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	verifier := NewVerifier(mirror.New(server.URL, 2*time.Second), "0xd583d91742f8888f5b106c3f3c854eacd7db7143")
	_, err := verifier.AwaitDeposit(context.Background(), "0.0.1@1.0", Quote{
		SessionID:       "0x" + strings.Repeat("11", 32),
		ProviderAddress: "0xf70ccc8f7f8771f8dced650fb5ce8279045e3e23",
		PricePerSecond:  big.NewInt(3334),
		MinSeconds:      300,
	})

	if err == nil {
		t.Fatal("a reverted deposit should be rejected")
	}
	var rejection *Rejection
	if !asRejection(err, &rejection) {
		t.Fatalf("want a Rejection, got %T: %v", err, err)
	}
	if calls != 1 {
		t.Errorf("a rejection was retried %d times; it should be final", calls)
	}
}

// A transaction the mirror has not ingested yet is the normal state for the
// first few seconds. Treating it as a rejection would fail good payments.
func TestAwaitDepositRetriesWhileNotYetVisible(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		http.NotFound(w, nil)
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	verifier := NewVerifier(mirror.New(server.URL, time.Second), "0xabc")
	_, err := verifier.AwaitDeposit(ctx, "0.0.1@1.0", Quote{
		SessionID:      "0x" + strings.Repeat("11", 32),
		PricePerSecond: big.NewInt(1),
	})
	if err == nil {
		t.Fatal("should eventually give up")
	}
	if calls < 2 {
		t.Errorf("gave up after %d attempts; a lagging mirror should be retried", calls)
	}
}

// --- helpers ---

func asRejection(err error, target **Rejection) bool {
	for err != nil {
		if r, ok := err.(*Rejection); ok {
			*target = r
			return true
		}
		unwrapper, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = unwrapper.Unwrap()
	}
	return false
}

func writeJSON(w http.ResponseWriter, body any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(body)
}

func padUint(v int64) string  { return fmt.Sprintf("%064x", v) }
func padAddress(a string) string { return strings.Repeat("0", 24) + a }

func mustHex(s string) []byte {
	decoded, err := hex.DecodeString(s)
	if err != nil {
		panic(err)
	}
	return decoded
}

func encodeWords(words ...string) []byte {
	return mustHex(strings.Join(words, ""))
}
