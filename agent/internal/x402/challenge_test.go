package x402

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"
)

// TestChallengeMatchesReferenceServer pins our challenge to the bytes the
// official @x402/express + @x402/hedera resource server emits. The fixture was
// captured from smoke/src/server.ts. If this test fails, the official TS client
// will refuse to pay this node — fix the agent, not the fixture.
func TestChallengeMatchesReferenceServer(t *testing.T) {
	raw, err := os.ReadFile("testdata/golden_challenge.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	var golden PaymentRequired
	if err := json.Unmarshal(raw, &golden); err != nil {
		t.Fatalf("decode fixture: %v", err)
	}

	ours := &PaymentRequired{
		X402Version: Version,
		Error:       golden.Error,
		Resource: ResourceInfo{
			URL:         golden.Resource.URL,
			Description: golden.Resource.Description,
			MimeType:    golden.Resource.MimeType,
			ServiceName: golden.Resource.ServiceName,
		},
		Accepts: []PaymentRequirements{{
			Scheme:            SchemeExact,
			Network:           HederaTestnet,
			Amount:            "100000",
			Asset:             HBARAssetID,
			PayTo:             "0.0.8011510",
			MaxTimeoutSeconds: 300,
			Extra:             map[string]any{"feePayer": "0.0.7162784"},
		}},
	}

	if !reflect.DeepEqual(*ours, golden) {
		ourJSON, _ := json.MarshalIndent(ours, "", "  ")
		goldenJSON, _ := json.MarshalIndent(golden, "", "  ")
		t.Fatalf("challenge diverged from the reference server\nours:\n%s\n\ngolden:\n%s", ourJSON, goldenJSON)
	}

	// Round-tripping our struct must also produce a body the reference client
	// can decode, so check the marshalled form parses back identically.
	encoded, err := json.Marshal(ours)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var roundTripped PaymentRequired
	if err := json.Unmarshal(encoded, &roundTripped); err != nil {
		t.Fatalf("round trip: %v", err)
	}
	if !reflect.DeepEqual(roundTripped, golden) {
		t.Fatalf("round trip diverged:\n%s", encoded)
	}
}

func TestEncodeChallengeIsBase64JSON(t *testing.T) {
	challenge := &PaymentRequired{
		X402Version: Version,
		Resource:    ResourceInfo{URL: "http://node.example/v1/sessions"},
		Accepts: []PaymentRequirements{{
			Scheme:            SchemeExact,
			Network:           HederaTestnet,
			Amount:            "100000",
			Asset:             HBARAssetID,
			PayTo:             "0.0.1234",
			MaxTimeoutSeconds: 300,
			Extra:             map[string]any{"feePayer": "0.0.5678"},
		}},
	}

	encoded, err := EncodeChallenge(challenge)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}

	decoded, err := decodeChallengeForTest(encoded)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if decoded.Accepts[0].Amount != "100000" {
		t.Fatalf("amount mangled: %q", decoded.Accepts[0].Amount)
	}
	if decoded.Accepts[0].Extra["feePayer"] != "0.0.5678" {
		t.Fatalf("feePayer mangled: %v", decoded.Accepts[0].Extra["feePayer"])
	}
}
