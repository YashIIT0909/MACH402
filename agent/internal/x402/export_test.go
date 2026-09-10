package x402

import (
	"encoding/base64"
	"encoding/json"
)

func decodeChallengeForTest(encoded string) (*PaymentRequired, error) {
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, err
	}
	var challenge PaymentRequired
	if err := json.Unmarshal(raw, &challenge); err != nil {
		return nil, err
	}
	return &challenge, nil
}
