package x402

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// supportedTTL bounds how stale a cached /supported answer may get. The
// facilitator's fee payer can change; caching it avoids a round trip on every
// 402 while still picking up a rotation within the TTL.
const supportedTTL = 5 * time.Minute

// Facilitator is the client for the x402 facilitator that co-signs as fee payer
// and submits to Hedera. It is not ours and must not be reimplemented.
type Facilitator struct {
	baseURL string
	http    *http.Client

	mu       sync.Mutex
	cached   *SupportedResponse
	cachedAt time.Time
}

// NewFacilitator returns a client for the facilitator at baseURL.
func NewFacilitator(baseURL string, timeout time.Duration) *Facilitator {
	return &Facilitator{
		baseURL: strings.TrimRight(baseURL, "/"),
		http:    &http.Client{Timeout: timeout},
	}
}

// URL is the facilitator this client talks to.
func (f *Facilitator) URL() string { return f.baseURL }

// Supported returns the facilitator's capabilities, cached for supportedTTL.
func (f *Facilitator) Supported(ctx context.Context) (*SupportedResponse, error) {
	f.mu.Lock()
	if f.cached != nil && time.Since(f.cachedAt) < supportedTTL {
		cached := f.cached
		f.mu.Unlock()
		return cached, nil
	}
	f.mu.Unlock()

	var body SupportedResponse
	if err := f.get(ctx, "/supported", &body); err != nil {
		// Serve a stale answer rather than failing a paying customer: a
		// momentarily unreachable facilitator should not take the node down.
		f.mu.Lock()
		defer f.mu.Unlock()
		if f.cached != nil {
			return f.cached, nil
		}
		return nil, err
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	f.cached = &body
	f.cachedAt = time.Now()
	return f.cached, nil
}

// Kind finds the supported kind for a scheme/network pair.
//
// The fee payer in its Extra is what goes into a challenge's extra.feePayer. It
// is read from here on every challenge and never hardcoded — a mismatch makes
// the client SDK throw before it signs (CLAUDE.md invariant 5).
func (f *Facilitator) Kind(ctx context.Context, scheme, network string) (SupportedKind, error) {
	supported, err := f.Supported(ctx)
	if err != nil {
		return SupportedKind{}, fmt.Errorf("facilitator /supported: %w", err)
	}
	for _, kind := range supported.Kinds {
		if kind.Scheme == scheme && kind.Network == network {
			return kind, nil
		}
	}
	seen := make([]string, 0, len(supported.Kinds))
	for _, kind := range supported.Kinds {
		seen = append(seen, kind.Scheme+"/"+kind.Network)
	}
	return SupportedKind{}, fmt.Errorf(
		"facilitator %s does not support %s/%s (advertises: %s)",
		f.baseURL, scheme, network, strings.Join(seen, ", "),
	)
}

// Verify asks the facilitator whether a payload would settle. Work must never
// start before this returns IsValid (CLAUDE.md invariant 4).
func (f *Facilitator) Verify(ctx context.Context, req VerifyRequest) (*VerifyResponse, error) {
	var out VerifyResponse
	if err := f.post(ctx, "/verify", req, &out); err != nil {
		return nil, fmt.Errorf("facilitator /verify: %w", err)
	}
	return &out, nil
}

// Settle submits the payment to Hedera. Call it immediately after starting
// work, never after finishing it: the signed payload expires at the
// requirements' MaxTimeoutSeconds.
func (f *Facilitator) Settle(ctx context.Context, req SettleRequest) (*SettleResponse, error) {
	var out SettleResponse
	if err := f.post(ctx, "/settle", req, &out); err != nil {
		return nil, fmt.Errorf("facilitator /settle: %w", err)
	}
	return &out, nil
}

func (f *Facilitator) get(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, f.baseURL+path, nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	return f.do(req, out)
}

func (f *Facilitator) post(ctx context.Context, path string, in, out any) error {
	body, err := json.Marshal(in)
	if err != nil {
		return fmt.Errorf("encode request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, f.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	return f.do(req, out)
}

func (f *Facilitator) do(req *http.Request, out any) error {
	req.Header.Set("Accept", "application/json")

	resp, err := f.http.Do(req)
	if err != nil {
		return fmt.Errorf("%s %s: %w", req.Method, req.URL.Path, err)
	}
	defer resp.Body.Close()

	// Cap the read so a misbehaving facilitator cannot exhaust node memory.
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("%s %s returned %d: %s", req.Method, req.URL.Path, resp.StatusCode, truncate(string(body), 400))
	}

	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("decode response (%s): %w", truncate(string(body), 200), err)
	}
	return nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
