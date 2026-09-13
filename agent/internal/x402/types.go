// Package x402 implements the merchant half of the x402 payment protocol.
//
// The merchant side needs only JSON construction and HTTP calls to a
// facilitator: it builds a challenge, forwards a client's signed payload for
// verification, and asks the facilitator to submit it. It never signs anything.
// There is deliberately no Hedera SDK in this package, and no private key
// anywhere in this binary — see CLAUDE.md invariant 1.
package x402

// Version is the x402 protocol version MACH402 speaks.
const Version = 2

// HTTP header names. x402 v2 renamed these; the X-PAYMENT pair is v1 and is
// accepted on input only, so older clients keep working.
const (
	HeaderPaymentSignature   = "PAYMENT-SIGNATURE"
	HeaderPaymentRequired    = "PAYMENT-REQUIRED"
	HeaderPaymentResponse    = "PAYMENT-RESPONSE"
	HeaderPaymentSignatureV1 = "X-PAYMENT"
	HeaderPaymentResponseV1  = "X-PAYMENT-RESPONSE"
)

// A 402 is a single-use challenge and must never be cached.
const CacheControlPaymentRequired = "no-store"

// Scheme and network identifiers.
const (
	SchemeExact     = "exact"
	HederaTestnet   = "hedera:testnet"
	HederaMainnet   = "hedera:mainnet"
	HBARAssetID     = "0.0.0"
	TinybarsPerHBAR = 100_000_000
)

// ResourceInfo describes what is being sold. Required on a v2 challenge.
type ResourceInfo struct {
	URL         string   `json:"url"`
	Description string   `json:"description,omitempty"`
	MimeType    string   `json:"mimeType,omitempty"`
	ServiceName string   `json:"serviceName,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	IconURL     string   `json:"iconUrl,omitempty"`
}

// PaymentRequirements is one way a client may pay for a resource.
//
// Amount is a decimal string in the asset's smallest unit — tinybars for HBAR.
// It is never parsed into a float anywhere in MACH402.
type PaymentRequirements struct {
	Scheme            string         `json:"scheme"`
	Network           string         `json:"network"`
	Amount            string         `json:"amount"`
	Asset             string         `json:"asset"`
	PayTo             string         `json:"payTo"`
	MaxTimeoutSeconds int            `json:"maxTimeoutSeconds"`
	Extra             map[string]any `json:"extra"`
}

// PaymentRequired is the 402 challenge body, base64-encoded into the
// PAYMENT-REQUIRED response header.
type PaymentRequired struct {
	X402Version int                   `json:"x402Version"`
	Error       string                `json:"error,omitempty"`
	Resource    ResourceInfo          `json:"resource"`
	Accepts     []PaymentRequirements `json:"accepts"`
	Extensions  map[string]any        `json:"extensions,omitempty"`
}

// PaymentPayload is what the client sends back in PAYMENT-SIGNATURE: the
// requirements it chose, plus the scheme-specific signed material. For Hedera
// exact, Payload is {"transaction": "<base64 partially-signed transfer>"}.
type PaymentPayload struct {
	X402Version int                 `json:"x402Version"`
	Resource    *ResourceInfo       `json:"resource,omitempty"`
	Accepted    PaymentRequirements `json:"accepted"`
	Payload     map[string]any      `json:"payload"`
	Extensions  map[string]any      `json:"extensions,omitempty"`
}

// VerifyRequest and SettleRequest share a shape.
type VerifyRequest struct {
	X402Version         int                 `json:"x402Version"`
	PaymentPayload      PaymentPayload      `json:"paymentPayload"`
	PaymentRequirements PaymentRequirements `json:"paymentRequirements"`
}

// SettleRequest is posted to the facilitator to actually move the money.
type SettleRequest = VerifyRequest

// VerifyResponse says whether the payload would settle. Work must not start
// until IsValid is true.
type VerifyResponse struct {
	IsValid        bool           `json:"isValid"`
	InvalidReason  string         `json:"invalidReason,omitempty"`
	InvalidMessage string         `json:"invalidMessage,omitempty"`
	Payer          string         `json:"payer,omitempty"`
	Extra          map[string]any `json:"extra,omitempty"`
}

// SettleResponse carries the on-chain result. Transaction is the Hedera
// transaction id, formatted 0.0.<feePayer>@<seconds>.<nanos>.
type SettleResponse struct {
	Success      bool           `json:"success"`
	ErrorReason  string         `json:"errorReason,omitempty"`
	ErrorMessage string         `json:"errorMessage,omitempty"`
	Payer        string         `json:"payer,omitempty"`
	Transaction  string         `json:"transaction"`
	Network      string         `json:"network"`
	Amount       string         `json:"amount,omitempty"`
	Extra        map[string]any `json:"extra,omitempty"`
}

// SupportedKind is one scheme/network pair a facilitator handles.
type SupportedKind struct {
	X402Version int            `json:"x402Version"`
	Scheme      string         `json:"scheme"`
	Network     string         `json:"network"`
	Extra       map[string]any `json:"extra,omitempty"`
}

// SupportedResponse is the body of GET /supported.
type SupportedResponse struct {
	Kinds      []SupportedKind     `json:"kinds"`
	Extensions []string            `json:"extensions"`
	Signers    map[string][]string `json:"signers"`
}

// FeePayer returns the facilitator's advertised Hedera fee-payer for a kind.
func (k SupportedKind) FeePayer() (string, bool) {
	raw, ok := k.Extra["feePayer"]
	if !ok {
		return "", false
	}
	feePayer, ok := raw.(string)
	if !ok || feePayer == "" {
		return "", false
	}
	return feePayer, true
}
