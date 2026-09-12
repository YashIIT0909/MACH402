// Package mirror reads Hedera's public mirror node over HTTP.
//
// This is how the node verifies an escrow deposit without holding a key or
// linking a Hedera SDK, and it is why escrow mode does not violate CLAUDE.md
// invariant 1. The facilitator plays the same role for transfers: somebody the
// node trusts to say "this payment is real". Here that somebody is Hedera's own
// public mirror, and the answer is a plain JSON GET that anyone can repeat.
//
// Deliberately not built on internal/fetch. That package exists to distrust
// renter-supplied URLs — it blocks loopback and private ranges and re-checks
// every redirect — which is exactly wrong for a known-good endpoint the operator
// configured. The shape here follows internal/x402's facilitator client instead:
// bounded timeout, capped read, errors wrapped with the path.
package mirror

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// DefaultURL is Hedera's public testnet mirror node.
const DefaultURL = "https://testnet.mirrornode.hedera.com"

// maxBody caps a mirror response. Contract results carry call data and logs,
// so this is looser than the facilitator's cap but still bounded.
const maxBody = 4 << 20

// ErrNotFound is returned for a 404. It is a distinct error because "not
// ingested yet" is the normal case immediately after a transaction lands, and
// callers retry on it rather than failing.
var ErrNotFound = errors.New("mirror node: not found")

type Client struct {
	baseURL string
	http    *http.Client
}

func New(baseURL string, timeout time.Duration) *Client {
	if baseURL == "" {
		baseURL = DefaultURL
	}
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		http:    &http.Client{Timeout: timeout},
	}
}

// URL reports the mirror node this client reads, for logs and diagnostics.
func (c *Client) URL() string { return c.baseURL }

// Account is the subset of a mirror account record this node cares about.
type Account struct {
	AccountID string `json:"account"`
	// EVMAddress is the address a contract can actually pay.
	//
	// It must be read, never computed. The obvious derivation — the "long-zero"
	// form, 0x plus the zero-padded account number — is accepted by the EVM as
	// an address but a contract's HBAR transfer to it FAILS for any account
	// that has an alias, which is every account the Hedera portal issues.
	// Measured on testnet, not inferred.
	EVMAddress string `json:"evm_address"`
	// ReceiverSigRequired accounts cannot be paid by a contract without their
	// own signature, which no contract can supply. Setup refuses to enable
	// escrow mode for a pay_to with this set.
	ReceiverSigRequired bool `json:"receiver_sig_required"`
	Balance             struct {
		Balance int64 `json:"balance"` // tinybars
	} `json:"balance"`
}

// Account looks up an account by `0.0.x` id or by EVM address.
func (c *Client) Account(ctx context.Context, idOrAddress string) (*Account, error) {
	var out Account
	if err := c.get(ctx, "/api/v1/accounts/"+url.PathEscape(idOrAddress), &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ContractResult is the outcome of one contract call, as the mirror reports it.
type ContractResult struct {
	// Result is "SUCCESS" for a call that did what it said. Anything else means
	// the transaction reached consensus but the contract rejected it — which is
	// a failed payment, not a failed lookup.
	Result string `json:"result"`
	// ContractID is the contract that was called, as `0.0.x`.
	ContractID string `json:"contract_id"`
	// Address is that same contract as an EVM address.
	Address string `json:"address"`
	// FunctionParameters is the hex call data, selector included. This is what
	// proves WHICH function ran and with what arguments — the node checks it
	// rather than trusting the renter's claim about their own transaction.
	FunctionParameters string `json:"function_parameters"`
	// From is the caller's EVM address: the renter who paid.
	From string `json:"from"`
	// Amount is the value sent, in tinybars.
	Amount int64 `json:"amount"`
	// ErrorMessage carries the revert data when Result is not SUCCESS.
	ErrorMessage string `json:"error_message"`
	Timestamp    string `json:"timestamp"`
	Hash         string `json:"hash"`
}

// ContractResultByTransaction fetches the result of a contract call.
//
// The id may be a Hedera transaction id (`0.0.x@seconds.nanos`) or an Ethereum
// transaction hash; a renter's client may naturally hold either depending on
// how it submitted. The Hedera form is rewritten into the dashed shape the
// mirror node's path requires — see normalizeTransactionID.
func (c *Client) ContractResultByTransaction(ctx context.Context, txID string) (*ContractResult, error) {
	normalized, err := normalizeTransactionID(txID)
	if err != nil {
		return nil, err
	}

	var out ContractResult
	if err := c.get(ctx, "/api/v1/contracts/results/"+url.PathEscape(normalized), &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// callRequest is the body of the mirror node's eth_call equivalent.
type callRequest struct {
	Data  string `json:"data"`
	To    string `json:"to"`
	Gas   int    `json:"gas,omitempty"`
	Block string `json:"block,omitempty"`
}

type callResponse struct {
	Result string `json:"result"`
}

// Call executes a read-only contract call and returns the raw return data.
//
// This is the mirror node's `/contracts/call`, Hedera's eth_call: it runs the
// contract's code against current state and returns what it would return,
// without a transaction, without gas, and without a key. It is what lets the
// node read a session's on-chain record directly rather than reconstructing it
// from the deposit transaction's arguments — so what the node checks is the
// state the contract actually holds, not what the renter said they sent.
func (c *Client) Call(ctx context.Context, contract, callData string) ([]byte, error) {
	body := callRequest{
		Data:  ensure0x(callData),
		To:    ensure0x(contract),
		Block: "latest",
	}

	var out callResponse
	if err := c.post(ctx, "/api/v1/contracts/call", body, &out); err != nil {
		return nil, err
	}
	return decodeHex(out.Result)
}

func (c *Client) get(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	return c.do(req, out)
}

func (c *Client) post(ctx context.Context, path string, in, out any) error {
	encoded, err := json.Marshal(in)
	if err != nil {
		return fmt.Errorf("encode request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, strings.NewReader(string(encoded)))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	return c.do(req, out)
}

func (c *Client) do(req *http.Request, out any) error {
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("%s %s: %w", req.Method, req.URL.Path, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode == http.StatusNotFound {
		return fmt.Errorf("%s: %w", req.URL.Path, ErrNotFound)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("%s %s returned %d: %s",
			req.Method, req.URL.Path, resp.StatusCode, truncate(string(body), 400))
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
