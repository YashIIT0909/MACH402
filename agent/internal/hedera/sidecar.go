// Package hedera drives the node's signing sidecar.
//
// CLAUDE.md invariant 1 says the agent never holds a private key and never
// imports a Hedera SDK, and this package does not change that: it runs a
// separate program, `cleargate-hedera`, and reads JSON back. The key file is
// named in config and opened by that child process; nothing in the Go binary
// ever reads it, and no Hedera library is linked.
//
// This is the same move the codebase already makes twice. internal/sshca shells
// out to ssh-keygen rather than reimplementing OpenSSH key formats, and
// internal/tunnel supervises a cloudflared process rather than speaking
// Cloudflare's protocol. Signing a Hedera transaction is the third thing the
// node needs done and has no business doing itself.
//
// The sidecar's contract is narrow on purpose: exactly one JSON object on
// stdout, diagnostics on stderr, exit code carries success. That is what makes
// it safe to parse from here.
package hedera

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// Default timeouts. Publishing waits on Hedera consensus, which is a couple of
// seconds; registration additionally waits on a contract call.
const (
	defaultTimeout  = 45 * time.Second
	registerTimeout = 90 * time.Second
)

// Sidecar invokes the `cleargate-hedera` command.
type Sidecar struct {
	command string
	keyPath string
	network string
	mirror  string
}

func New(command, keyPath, network, mirrorURL string) *Sidecar {
	return &Sidecar{command: command, keyPath: keyPath, network: network, mirror: mirrorURL}
}

// Available reports whether the sidecar can actually be found.
//
// Checked at setup and at startup rather than at the first settlement, because
// "cleargate-hedera is not installed" is a thing a provider can fix in a minute
// and a terrible thing to discover while a renter is waiting.
func (s *Sidecar) Available() error {
	if _, err := exec.LookPath(s.command); err != nil {
		return fmt.Errorf(
			"%s not found on PATH; install it with `pnpm install` in the ClearGate checkout, "+
				"or point hedera.sidecar at it: %w", s.command, err)
	}
	return nil
}

// KeyInfo is what `keygen` and `account-info` report.
type KeyInfo struct {
	EVMAddress      string `json:"evm_address"`
	AccountID       string `json:"account_id"`
	BalanceTinybars string `json:"balance_tinybars"`
	Funded          bool   `json:"funded"`
}

// EnsureKey creates the node's operator key if it is absent, and describes it.
//
// Idempotent, exactly like sshca.Ensure: regenerating would strand both the
// funded account and the ERC-8004 identity bound to the old key.
func (s *Sidecar) EnsureKey(ctx context.Context) (*KeyInfo, error) {
	var out KeyInfo
	if err := s.run(ctx, defaultTimeout, nil, &out, "keygen", "--out", s.keyPath); err != nil {
		return nil, err
	}
	return &out, nil
}

// AccountInfo looks up an account through the sidecar's mirror-node client.
type AccountInfo struct {
	Found               bool   `json:"found"`
	AccountID           string `json:"account_id"`
	EVMAddress          string `json:"evm_address"`
	BalanceTinybars     string `json:"balance_tinybars"`
	ReceiverSigRequired bool   `json:"receiver_sig_required"`
}

func (s *Sidecar) AccountInfo(ctx context.Context, account string) (*AccountInfo, error) {
	args := []string{"account-info"}
	if account != "" {
		args = append(args, "--account", account)
	}

	var out AccountInfo
	if err := s.run(ctx, defaultTimeout, nil, &out, args...); err != nil {
		return nil, err
	}
	return &out, nil
}

// CreateTopic creates this provider's audit topic. One time, at setup.
func (s *Sidecar) CreateTopic(ctx context.Context, memo string) (string, error) {
	var out struct {
		TopicID string `json:"topic_id"`
	}
	if err := s.run(ctx, defaultTimeout, nil, &out, "hcs-create-topic", "--memo", memo); err != nil {
		return "", err
	}
	if out.TopicID == "" {
		return "", errors.New("sidecar created a topic but reported no topic id")
	}
	return out.TopicID, nil
}

// PublishMessage submits one audit message to a topic.
//
// The message goes over stdin rather than as an argument. A settlement record
// names a payer, a payee and an amount, and argv is readable by any local user
// through the process table.
func (s *Sidecar) PublishMessage(ctx context.Context, topicID string, message []byte) (string, error) {
	var out struct {
		SequenceNumber string `json:"sequence_number"`
	}
	err := s.run(ctx, defaultTimeout, message, &out, "hcs-publish", "--topic", topicID)
	if err != nil {
		return "", err
	}
	return out.SequenceNumber, nil
}

// Registration is the result of an ERC-8004 identity registration.
type Registration struct {
	AgentID      string `json:"agent_id"`
	AgentAddress string `json:"agent_address"`
	Transaction  string `json:"transaction"`
	// Created distinguishes a freshly minted identity from one that already
	// existed, so `register` can tell a provider which happened.
	Created bool `json:"created"`
}

// RegisterIdentity registers this node as an agent, or adopts its existing id.
func (s *Sidecar) RegisterIdentity(ctx context.Context, registry, domain string) (*Registration, error) {
	var out Registration
	err := s.run(ctx, registerTimeout, nil, &out,
		"identity-register", "--registry", registry, "--domain", domain)
	if err != nil {
		return nil, err
	}
	if out.AgentID == "" || out.AgentID == "0" {
		return nil, errors.New("sidecar returned no agent id")
	}
	return &out, nil
}

// SettleSession closes an escrow session on-chain.
func (s *Sidecar) SettleSession(ctx context.Context, contract, sessionID string) (string, error) {
	var out struct {
		Transaction string `json:"transaction"`
	}
	err := s.run(ctx, registerTimeout, nil, &out,
		"escrow-settle", "--contract", contract, "--session", sessionID)
	if err != nil {
		return "", err
	}
	return out.Transaction, nil
}

// sidecarError carries what the child process said when it failed.
type sidecarError struct {
	command string
	stderr  string
	err     error
}

func (e *sidecarError) Error() string {
	if e.stderr != "" {
		return fmt.Sprintf("%s %s: %s", e.command, e.err, e.stderr)
	}
	return fmt.Sprintf("%s %s", e.command, e.err)
}

func (e *sidecarError) Unwrap() error { return e.err }

// run invokes one subcommand and decodes its single JSON object.
//
// The key path is passed as an argument, which is safe — it is a filename, not
// a secret — but the key's contents never cross this boundary in either
// direction.
func (s *Sidecar) run(ctx context.Context, timeout time.Duration, stdin []byte, out any, args ...string) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	full := append([]string{
		"--key-file", s.keyPath,
		"--network", s.network,
	}, args...)
	if s.mirror != "" {
		full = append([]string{"--mirror-url", s.mirror}, full...)
	}

	cmd := exec.CommandContext(ctx, s.command, full...)
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return &sidecarError{
			command: s.command + " " + args[0],
			stderr:  truncate(strings.TrimSpace(stderr.String()), 500),
			err:     err,
		}
	}

	// Only the last line is parsed. A dependency that writes a warning to
	// stdout despite being asked not to would otherwise break every call.
	line := lastJSONLine(stdout.String())
	if line == "" {
		return fmt.Errorf("%s %s produced no JSON on stdout", s.command, args[0])
	}
	if err := json.Unmarshal([]byte(line), out); err != nil {
		return fmt.Errorf("%s %s returned unparseable output (%s): %w",
			s.command, args[0], truncate(line, 200), err)
	}
	return nil
}

func lastJSONLine(output string) string {
	lines := strings.Split(strings.TrimSpace(output), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if strings.HasPrefix(line, "{") && strings.HasSuffix(line, "}") {
			return line
		}
	}
	return ""
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
