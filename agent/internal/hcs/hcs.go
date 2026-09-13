// Package hcs publishes settlements to a Hedera Consensus Service topic.
//
// CLAUDE.md has said since M1 that every settlement is logged "to the local
// append-only receipt file **and** to HCS". This is the second half. The point
// is that a provider can audit their own earnings without trusting our website
// — and, more than that, without trusting their own node: the topic is
// append-only and ordered by Hedera consensus, so a record on it cannot be
// quietly edited later by anyone, us included.
//
// Why the provider's key and not the renter's: by the time there is a receipt
// worth publishing, the renter already has their compute. Asking them to spend
// HBAR recording the provider's earnings is asking for a favour nobody has a
// reason to do. The party who benefits from the record existing has to be the
// one who writes it.
package hcs

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/YashIIT0909/ClearGate/agent/internal/hedera"
)

// Message kinds.
const (
	KindLease       = "lease"
	KindSessionOpen = "session_open"

	// KindSessionBurn is the running refund-owed checkpoint, published every
	// fifteen seconds a metered session is live rather than only when it ends.
	//
	// This is the whole reason a metered session is trustworthy. Between paying
	// for a chunk and being refunded the remainder, the renter's money is in
	// the provider's hands and nothing but the provider's word says how much of
	// it is still theirs. A burn checkpoint turns that word into a
	// consensus-ordered, running-hash-bound public fact, published continuously
	// and well before anyone has a reason to dispute it — so a provider who
	// later refuses to refund has already signed the number they are refusing
	// to honour, over and over, at a time when they had no motive to lie.
	KindSessionBurn = "session_burn"

	// KindSessionSettled closes the trail: what was earned, what was returned,
	// and the transaction that returned it.
	KindSessionSettled = "session_settle"
)

// AuditMessage is one entry on the trail.
//
// It mirrors receipts.Receipt, plus the session fields escrow adds, so the
// public trail and the local log can be compared line for line. Amounts stay
// strings, like everywhere else in ClearGate.
type AuditMessage struct {
	Kind      string `json:"kind"`
	NodeID    string `json:"node_id"`
	JobID     string `json:"job_id,omitempty"`
	SessionID string `json:"session_id,omitempty"`

	Transaction    string `json:"transaction,omitempty"`
	Payer          string `json:"payer,omitempty"`
	PayTo          string `json:"pay_to,omitempty"`
	AmountTinybars string `json:"amount_tinybars,omitempty"`
	Asset          string `json:"asset,omitempty"`
	Network        string `json:"network,omitempty"`

	// Metered sessions only.
	//
	// RefundTinybars is the load-bearing field: on a burn checkpoint it is what
	// the node owes back *right now*, and on the closing record it is what was
	// actually returned. A reader who sees the two disagree — or who sees a
	// session end with no closing record after a trail of burn checkpoints —
	// has everything they need to say so, without trusting either party's
	// account of it.
	PricePerSecond string `json:"price_per_second,omitempty"`
	DurationSecs   int64  `json:"duration_seconds,omitempty"`
	ElapsedSecs    int64  `json:"elapsed_seconds,omitempty"`
	RefundTinybars string `json:"refund_tinybars,omitempty"`

	SettledAt string `json:"settled_at"`
}

// Publisher writes audit messages, or quietly does nothing when HCS is off.
type Publisher struct {
	sidecar *hedera.Sidecar
	topicID string
	nodeID  string
	log     *slog.Logger
}

// New builds a publisher. A nil sidecar or empty topic yields a no-op
// publisher, so every call site can publish unconditionally instead of
// repeating an enabled check.
func New(sidecar *hedera.Sidecar, topicID, nodeID string, log *slog.Logger) *Publisher {
	if sidecar == nil || topicID == "" {
		return nil
	}
	return &Publisher{sidecar: sidecar, topicID: topicID, nodeID: nodeID, log: log}
}

// TopicID is the topic this publisher writes to, for display and for the
// node's own spec.
func (p *Publisher) TopicID() string {
	if p == nil {
		return ""
	}
	return p.topicID
}

// Publish records one settlement, in the background, best-effort.
//
// Deliberately returns nothing. A publish failure must never fail the request
// that triggered it: the renter has paid and the container is running, and
// refusing to answer them because a topic submission timed out would turn a
// bookkeeping problem into a lost sale. receipts.jsonl is written first and
// stays the record of account; this is the copy nobody can edit.
//
// It runs on its own context rather than the request's, because the request is
// about to finish and cancelling the publish with it would mean almost nothing
// ever got published.
func (p *Publisher) Publish(msg AuditMessage) {
	if p == nil {
		return
	}

	msg.NodeID = p.nodeID
	if msg.SettledAt == "" {
		msg.SettledAt = time.Now().UTC().Format(time.RFC3339)
	}

	encoded, err := json.Marshal(msg)
	if err != nil {
		p.log.Error("could not encode an audit message", "kind", msg.Kind, "error", err)
		return
	}

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()

		sequence, err := p.sidecar.PublishMessage(ctx, p.topicID, encoded)
		if err != nil {
			p.log.Error("could not publish to the audit topic; the local receipt still stands",
				"kind", msg.Kind, "topic", p.topicID, "error", err)
			return
		}
		p.log.Info("published to the audit topic",
			"kind", msg.Kind, "topic", p.topicID, "sequence", sequence)
	}()
}
