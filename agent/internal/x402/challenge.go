package x402

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
)

// ChallengeConfig is everything a node needs to price one resource.
type ChallengeConfig struct {
	// PayTo is the provider's Hedera account. Payments go renter -> node,
	// direct; the registry never touches funds (CLAUDE.md invariant 3).
	PayTo string
	// Amount is in the asset's smallest unit, as a string. Tinybars for HBAR.
	Amount string
	// Asset is "0.0.0" for HBAR, or an HTS token id.
	Asset string
	// Network is a CAIP-2 identifier such as "hedera:testnet".
	Network string
	// MaxTimeoutSeconds bounds how long a signed payload stays valid. Keep it
	// short; settlement must happen well inside this window.
	MaxTimeoutSeconds int
	// Description, ServiceName and MimeType describe the resource being sold.
	Description string
	ServiceName string
	MimeType    string
}

// BuildChallenge assembles the 402 body for a request.
//
// extra.feePayer comes from the facilitator's /supported and is never
// hardcoded. resourceURL must be the absolute URL the client requested — the
// client echoes it back and the facilitator checks the payload against it.
func BuildChallenge(ctx context.Context, fac *Facilitator, cfg ChallengeConfig, resourceURL, reason string) (*PaymentRequired, error) {
	kind, err := fac.Kind(ctx, SchemeExact, cfg.Network)
	if err != nil {
		return nil, err
	}
	feePayer, ok := kind.FeePayer()
	if !ok {
		return nil, fmt.Errorf("facilitator advertises %s/%s without extra.feePayer", SchemeExact, cfg.Network)
	}

	return &PaymentRequired{
		X402Version: Version,
		Error:       reason,
		Resource: ResourceInfo{
			URL:         resourceURL,
			Description: cfg.Description,
			MimeType:    cfg.MimeType,
			ServiceName: cfg.ServiceName,
		},
		Accepts: []PaymentRequirements{{
			Scheme:            SchemeExact,
			Network:           cfg.Network,
			Amount:            cfg.Amount,
			Asset:             cfg.Asset,
			PayTo:             cfg.PayTo,
			MaxTimeoutSeconds: cfg.MaxTimeoutSeconds,
			Extra:             map[string]any{"feePayer": feePayer},
		}},
	}, nil
}

// EncodeChallenge base64-encodes a challenge for the PAYMENT-REQUIRED header.
func EncodeChallenge(challenge *PaymentRequired) (string, error) {
	raw, err := json.Marshal(challenge)
	if err != nil {
		return "", fmt.Errorf("encode challenge: %w", err)
	}
	return base64.StdEncoding.EncodeToString(raw), nil
}

// WriteChallenge sends a 402 the way an x402 v2 client expects it: the
// authoritative challenge base64-encoded in the PAYMENT-REQUIRED header, with a
// readable JSON echo in the body for browsers and for debugging by hand.
func WriteChallenge(w http.ResponseWriter, challenge *PaymentRequired) error {
	encoded, err := EncodeChallenge(challenge)
	if err != nil {
		return err
	}

	w.Header().Set(HeaderPaymentRequired, encoded)
	w.Header().Set("Cache-Control", CacheControlPaymentRequired)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusPaymentRequired)

	// Body is informational; the header is what the SDK reads.
	if err := json.NewEncoder(w).Encode(challenge); err != nil {
		return fmt.Errorf("write challenge body: %w", err)
	}
	return nil
}

// DecodePaymentHeader parses the client's payment payload out of a request.
// It reads the v2 header first and falls back to the v1 name so older clients
// keep working (CLAUDE.md invariant 7: old versions keep working).
func DecodePaymentHeader(r *http.Request) (*PaymentPayload, bool, error) {
	encoded := r.Header.Get(HeaderPaymentSignature)
	if encoded == "" {
		encoded = r.Header.Get(HeaderPaymentSignatureV1)
	}
	if encoded == "" {
		return nil, false, nil
	}

	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, true, fmt.Errorf("payment header is not valid base64: %w", err)
	}

	var payload PaymentPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, true, fmt.Errorf("payment header is not a valid payment payload: %w", err)
	}
	return &payload, true, nil
}

// WriteSettlement attaches a settlement receipt to a successful response.
func WriteSettlement(w http.ResponseWriter, settlement *SettleResponse) error {
	raw, err := json.Marshal(settlement)
	if err != nil {
		return fmt.Errorf("encode settlement: %w", err)
	}
	encoded := base64.StdEncoding.EncodeToString(raw)
	w.Header().Set(HeaderPaymentResponse, encoded)
	// Settlement metadata is per-payer; shared caches must not store it.
	w.Header().Set("Cache-Control", "private, no-store")
	return nil
}
