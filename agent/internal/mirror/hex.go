package mirror

import (
	"encoding/hex"
	"fmt"
	"strings"
)

// ensure0x normalises a hex string to the 0x form the mirror node expects.
func ensure0x(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "0x") || strings.HasPrefix(s, "0X") {
		return s
	}
	return "0x" + s
}

// decodeHex parses a 0x-prefixed hex string, tolerating an odd-length body.
//
// The mirror node is not consistent about padding: a return value of a single
// zero byte can come back as "0x0". Rejecting that as malformed would turn a
// perfectly good "this is zero" answer into an error.
func decodeHex(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(strings.TrimPrefix(s, "0x"), "0X")
	if s == "" {
		return nil, nil
	}
	if len(s)%2 == 1 {
		s = "0" + s
	}

	decoded, err := hex.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("decode hex: %w", err)
	}
	return decoded, nil
}
