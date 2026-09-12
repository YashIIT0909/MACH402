package mirror

import (
	"fmt"
	"strings"
)

// normalizeTransactionID converts a Hedera transaction id to the form the
// mirror node's REST path expects.
//
// The SDK and every log line in this project render a transaction id as
// `0.0.1234@1700000000.000000123`. The mirror node's URL path wants
// `0.0.1234-1700000000-000000123` — dashes, not `@` and `.`, and the nanos
// zero-padded to nine digits. Passing the familiar form through unchanged gets
// a 400 with a message about the format, which is an unhelpful thing for a
// renter to see when their money is already in the contract.
//
// Anything already in the dashed form, and anything that is an Ethereum
// transaction hash, is passed through untouched — the same endpoint accepts
// both.
func normalizeTransactionID(id string) (string, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", fmt.Errorf("empty transaction id")
	}
	// An Ethereum-style hash, or an id already in the mirror's own format.
	if strings.HasPrefix(id, "0x") || !strings.Contains(id, "@") {
		return id, nil
	}

	account, timestamp, found := strings.Cut(id, "@")
	if !found || account == "" {
		return "", fmt.Errorf("not a Hedera transaction id: %q", id)
	}

	seconds, nanos, found := strings.Cut(timestamp, ".")
	if !found {
		return "", fmt.Errorf("transaction id %q has no nanosecond part", id)
	}
	if len(nanos) > 9 {
		return "", fmt.Errorf("transaction id %q has more than nine digits of nanoseconds", id)
	}
	// Left-padded: the mirror node matches on the literal string, so
	// "000000123" and "123" are different paths and only one of them exists.
	nanos = strings.Repeat("0", 9-len(nanos)) + nanos

	return fmt.Sprintf("%s-%s-%s", account, seconds, nanos), nil
}
