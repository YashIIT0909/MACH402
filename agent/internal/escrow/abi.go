package escrow

import (
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"strings"
)

// Function selectors for the calls this node reads or builds.
//
// Each is the first four bytes of keccak256 over the canonical signature in the
// comment. They are constants rather than computed because deriving them would
// mean linking a Keccak implementation into the agent for four values that only
// change when the contract's ABI changes.
//
// That makes them exactly the kind of constant that rots silently, so they are
// not trusted on faith: `make escrow-selectors` regenerates
// testdata/selectors.json from the compiled contract, and TestSelectorsMatchABI
// fails if these drift from it.
const (
	// openSession(bytes32,address,uint256,uint256)
	selectorOpenSession = "5ea02ad0"
	// topUp(bytes32,uint256)
	selectorTopUp = "b67644b9"
	// getSession(bytes32)
	selectorGetSession = "39b240bd"
	// settle(bytes32)
	selectorSettle = "987757dd"
)

const wordSize = 32

var errShortData = errors.New("abi: data ended early")

// Session is the on-chain record, decoded.
//
// Tinybars throughout, matching the contract: inside the Hedera EVM `msg.value`
// is denominated in tinybars, not weibars, so no conversion happens anywhere
// between the contract's storage and this struct.
type Session struct {
	Renter         string   // EVM address, 0x-prefixed
	Provider       string   // EVM address, 0x-prefixed
	PricePerSecond *big.Int // tinybars per second
	StartTime      *big.Int // unix seconds
	Duration       *big.Int // seconds paid for
	Deposited      *big.Int // tinybars
	Settled        bool
}

// ExpiresAt is when the paid time runs out, as a unix timestamp.
func (s *Session) ExpiresAt() int64 {
	return s.StartTime.Int64() + s.Duration.Int64()
}

// encodeGetSession builds the call data for reading a session.
func encodeGetSession(sessionID string) (string, error) {
	id, err := decodeBytes32(sessionID)
	if err != nil {
		return "", err
	}
	return selectorGetSession + hex.EncodeToString(id), nil
}

// decodeSession reads the struct `getSession` returns.
//
// A struct of fixed-size fields is ABI-encoded as one tuple behind a single
// offset word, so the payload is: [offset][renter][provider][price][start]
// [duration][deposited][settled]. Every field is one 32-byte word, which is why
// this needs no general-purpose ABI decoder.
func decodeSession(data []byte) (*Session, error) {
	// Tolerate both the offset-prefixed encoding and a bare tuple, since
	// different Hedera mirror versions have returned each.
	words := len(data) / wordSize
	start := 0
	if words == 8 {
		start = 1
	} else if words != 7 {
		return nil, fmt.Errorf("abi: expected 7 or 8 words, got %d", words)
	}

	renter, err := wordAsAddress(data, start)
	if err != nil {
		return nil, err
	}
	provider, err := wordAsAddress(data, start+1)
	if err != nil {
		return nil, err
	}
	price, err := wordAsInt(data, start+2)
	if err != nil {
		return nil, err
	}
	startTime, err := wordAsInt(data, start+3)
	if err != nil {
		return nil, err
	}
	duration, err := wordAsInt(data, start+4)
	if err != nil {
		return nil, err
	}
	deposited, err := wordAsInt(data, start+5)
	if err != nil {
		return nil, err
	}
	settled, err := wordAsInt(data, start+6)
	if err != nil {
		return nil, err
	}

	return &Session{
		Renter:         renter,
		Provider:       provider,
		PricePerSecond: price,
		StartTime:      startTime,
		Duration:       duration,
		Deposited:      deposited,
		Settled:        settled.Sign() != 0,
	}, nil
}

// callArguments strips the 4-byte selector from call data and returns the rest.
func callArguments(callData string) (selector string, args []byte, err error) {
	raw, err := hexBytes(callData)
	if err != nil {
		return "", nil, err
	}
	if len(raw) < 4 {
		return "", nil, errShortData
	}
	return hex.EncodeToString(raw[:4]), raw[4:], nil
}

func word(data []byte, index int) ([]byte, error) {
	from := index * wordSize
	if from+wordSize > len(data) {
		return nil, errShortData
	}
	return data[from : from+wordSize], nil
}

func wordAsInt(data []byte, index int) (*big.Int, error) {
	w, err := word(data, index)
	if err != nil {
		return nil, err
	}
	return new(big.Int).SetBytes(w), nil
}

// wordAsAddress reads the low 20 bytes of a word as an EVM address.
func wordAsAddress(data []byte, index int) (string, error) {
	w, err := word(data, index)
	if err != nil {
		return "", err
	}
	return "0x" + hex.EncodeToString(w[12:]), nil
}

func hexBytes(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(strings.TrimPrefix(s, "0x"), "0X")
	if len(s)%2 == 1 {
		s = "0" + s
	}
	return hex.DecodeString(s)
}

// decodeBytes32 validates a session id and returns its 32 raw bytes.
func decodeBytes32(s string) ([]byte, error) {
	raw, err := hexBytes(s)
	if err != nil {
		return nil, fmt.Errorf("not hex: %w", err)
	}
	if len(raw) != 32 {
		return nil, fmt.Errorf("expected 32 bytes, got %d", len(raw))
	}
	return raw, nil
}

// sameAddress compares EVM addresses without caring about case or 0x prefix.
//
// Addresses arrive from three places — the node's own config resolution, the
// mirror node, and the renter's call data — and each has its own opinion about
// EIP-55 checksum casing. Comparing them as raw strings would reject perfectly
// matching addresses.
func sameAddress(a, b string) bool {
	norm := func(s string) string {
		return strings.ToLower(strings.TrimPrefix(strings.TrimPrefix(strings.TrimSpace(s), "0x"), "0X"))
	}
	na, nb := norm(a), norm(b)
	return na != "" && na == nb
}
