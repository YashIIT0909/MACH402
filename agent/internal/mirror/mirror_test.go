package mirror

import "testing"

// The SDK renders a transaction id one way and the mirror node's REST path
// requires another. Getting this wrong produces a 400 at exactly the moment a
// renter's deposit is already in the contract, so it is worth pinning.
func TestNormalizeTransactionID(t *testing.T) {
	cases := []struct {
		in   string
		want string
		why  string
	}{
		{
			in:   "0.0.10446559@1789175626.422789314",
			want: "0.0.10446559-1789175626-422789314",
			why:  "the form the SDK and every log line produce",
		},
		{
			in:   "0.0.1234@1700000000.123",
			want: "0.0.1234-1700000000-000000123",
			why:  "nanos are left-padded to nine digits; the mirror matches the literal string",
		},
		{
			in:   "0.0.1234-1700000000-000000123",
			want: "0.0.1234-1700000000-000000123",
			why:  "already normalized, and must survive a second pass unchanged",
		},
		{
			in:   "0xabc123",
			want: "0xabc123",
			why:  "an Ethereum hash is valid at the same endpoint and must pass through",
		},
	}

	for _, tc := range cases {
		got, err := normalizeTransactionID(tc.in)
		if err != nil {
			t.Errorf("normalizeTransactionID(%q): %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("normalizeTransactionID(%q) = %q, want %q (%s)", tc.in, got, tc.want, tc.why)
		}
	}
}

func TestNormalizeTransactionIDRejectsNonsense(t *testing.T) {
	for _, bad := range []string{"", "   ", "0.0.1234@", "0.0.1234@1700000000.1234567890"} {
		if _, err := normalizeTransactionID(bad); err == nil {
			t.Errorf("normalizeTransactionID(%q) should have failed", bad)
		}
	}
}
