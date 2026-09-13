package httpapi

import (
	"io"
	"log/slog"
	"math/big"
	"sync"
	"testing"

	"github.com/YashIIT0909/ClearGate/agent/internal/config"
	"github.com/YashIIT0909/ClearGate/agent/internal/hcs"
	"github.com/YashIIT0909/ClearGate/agent/internal/runner"
)

// recordingAudit stands in for the provider's HCS topic.
type recordingAudit struct {
	mu       sync.Mutex
	messages []hcs.AuditMessage
}

func (r *recordingAudit) Publish(msg hcs.AuditMessage) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.messages = append(r.messages, msg)
}

func (r *recordingAudit) ofKind(kind string) []hcs.AuditMessage {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []hcs.AuditMessage
	for _, msg := range r.messages {
		if msg.Kind == kind {
			out = append(out, msg)
		}
	}
	return out
}

func meterServer(audit *recordingAudit) *Server {
	return &Server{
		cfg: config.Config{
			NodeID:  "node_test",
			PayTo:   "0.0.1234",
			Asset:   "0.0.0",
			Network: "hedera:testnet",
			Leases: config.Leases{
				SessionChunkSeconds:       300,
				LowCreditThresholdSeconds: 60,
			},
		},
		// tickMeter mirrors every burn onto the dashboard's event feed as well
		// as the audit topic (EventSessionBurn), so it needs a runner to
		// publish through — a bare one, with no Docker behind it.
		runner: runner.NewForTest(),
		audit:  audit,
		log:    slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}

// The mitigation itself: what the node owes back is published on every tick,
// not only when the session ends.
//
// This is the test that the whole design rests on. A metered session asks a
// renter to pay a stranger before the compute happens, and the only thing that
// makes the remainder checkable is that the node has already published it — to
// a consensus-ordered, running-hash-bound topic — at a time when it had no
// reason to lie. If the burn checkpoint stops firing, nothing else in the flow
// notices, and the design quietly becomes "trust the provider".
func TestEveryMeterTickPublishesWhatIsOwed(t *testing.T) {
	audit := &recordingAudit{}
	server := meterServer(audit)

	// 100 tinybars a second, 300 seconds bought: a 30,000 tinybar credit.
	price := big.NewInt(100)
	lease := runner.NewMeteredLeaseForTest("lease1", "0xsess", "0.0.999", price, big.NewInt(30_000))

	// Three sweeps' worth of time, 15 seconds apart.
	want := []string{"28500", "27000", "25500"}
	for i, owed := range want {
		runner.RewindMeterForTest(lease, 15)
		server.tickMeter(lease)

		burns := audit.ofKind(hcs.KindSessionBurn)
		if len(burns) != i+1 {
			t.Fatalf("after tick %d there are %d burn checkpoints; every tick must publish one",
				i+1, len(burns))
		}
		if got := burns[i].RefundTinybars; got != owed {
			t.Errorf("tick %d published refund_tinybars %q, want %q — this is the number a "+
				"provider is later held to, so it has to be what the meter actually says",
				i+1, got, owed)
		}
		if burns[i].SessionID != "0xsess" {
			t.Errorf("tick %d published session %q; a checkpoint nobody can attribute proves nothing",
				i+1, burns[i].SessionID)
		}
		if got := burns[i].AmountTinybars; got != new(big.Int).Sub(big.NewInt(30_000), mustInt(t, owed)).String() {
			t.Errorf("tick %d published earned %q, which does not add up with the refund it also published", i+1, got)
		}
	}

	if got, want := lease.Credit().String(), "25500"; got != want {
		t.Errorf("credit = %s, want %s", got, want)
	}
	if got, want := lease.SecondsRemaining(), int64(255); got != want {
		t.Errorf("seconds_remaining = %d, want %d", got, want)
	}
}

// A session cannot go into debt. The meter stops at zero and the freeze path
// takes over — the provider is not owed more than the renter put in, and the
// renter is not owed a negative refund.
func TestMeterStopsAtZeroRatherThanGoingNegative(t *testing.T) {
	audit := &recordingAudit{}
	server := meterServer(audit)

	lease := runner.NewMeteredLeaseForTest("lease1", "0xsess", "0.0.999", big.NewInt(100), big.NewInt(1_000))

	// Ten seconds of credit, a minute of running.
	runner.RewindMeterForTest(lease, 60)
	server.tickMeter(lease)

	if got := lease.Credit().String(); got != "0" {
		t.Errorf("credit = %s after overrunning, want 0", got)
	}
	if got := lease.Burned().String(); got != "1000" {
		t.Errorf("burned = %s, want 1000 — a session must never earn more than was paid in", got)
	}

	burns := audit.ofKind(hcs.KindSessionBurn)
	if len(burns) != 1 || burns[0].RefundTinybars != "0" {
		t.Fatalf("expected one checkpoint owing nothing, got %+v", burns)
	}
}

// Exhausted credit has to keep the expiry it ran out at, or the sweep never
// sees the session as overdue and the container runs unpaid for ever.
func TestExhaustedCreditPinsExpirySoTheFreezeCanFire(t *testing.T) {
	server := meterServer(&recordingAudit{})
	lease := runner.NewMeteredLeaseForTest("lease1", "0xsess", "0.0.999", big.NewInt(100), big.NewInt(1_000))

	runner.RewindMeterForTest(lease, 60)
	server.tickMeter(lease)
	first := lease.ExpiresAt()

	// A later tick on an empty credit must not push the expiry out again.
	runner.RewindMeterForTest(lease, 15)
	server.tickMeter(lease)

	if got := lease.ExpiresAt(); !got.Equal(first) {
		t.Errorf("expiry moved from %s to %s on an empty credit; the sweep freezes on how far "+
			"past expiry a lease is, so a session that keeps moving its own expiry never freezes",
			first, got)
	}
	if lease.OverdueBy() <= 0 {
		t.Error("an exhausted session reports itself as not overdue, so it would never be frozen")
	}
}

// The renter is told to top up by the node, which knows its own sweep interval,
// rather than by a client guessing at the threshold.
func TestLowCreditsTracksTheNodesOwnThreshold(t *testing.T) {
	server := meterServer(&recordingAudit{})

	// 120 seconds of credit at 100/s, against a 60s threshold.
	lease := runner.NewMeteredLeaseForTest("lease1", "0xsess", "0.0.999", big.NewInt(100), big.NewInt(12_000))
	if server.lowCredits(lease) {
		t.Error("a session with 120s left is not low on credit at a 60s threshold")
	}

	runner.RewindMeterForTest(lease, 70)
	server.tickMeter(lease)

	if !server.lowCredits(lease) {
		t.Errorf("a session with %ds left is low on credit at a 60s threshold, and a renter "+
			"who is not told will be frozen mid-run", lease.SecondsRemaining())
	}
}

// Earned plus owed always equals paid in — after every tick and after a
// top-up.
//
// The accounting identity the whole trail depends on. A renter reading the
// audit topic checks a provider by adding the published earned and refund
// figures and comparing them against the settlements they can see on-chain; if
// those two numbers can drift apart, the trail stops proving anything.
func TestEarnedPlusOwedAlwaysEqualsPaidIn(t *testing.T) {
	server := meterServer(&recordingAudit{})
	lease := runner.NewMeteredLeaseForTest("lease1", "0xsess", "0.0.999", big.NewInt(100), big.NewInt(30_000))

	paidIn := big.NewInt(30_000)
	check := func(stage string) {
		t.Helper()
		total := new(big.Int).Add(lease.Burned(), lease.Credit())
		if total.Cmp(paidIn) != 0 {
			t.Fatalf("%s: earned %s + owed %s = %s, but %s was paid in",
				stage, lease.Burned(), lease.Credit(), total, paidIn)
		}
	}

	for i := 0; i < 4; i++ {
		runner.RewindMeterForTest(lease, 15)
		server.tickMeter(lease)
		check("after a tick")
	}

	lease.AddCredit(big.NewInt(30_000))
	paidIn.Add(paidIn, big.NewInt(30_000))
	check("after a top-up")

	runner.RewindMeterForTest(lease, 15)
	server.tickMeter(lease)
	check("after a tick following a top-up")
}

// The length a renter picks is the length they pay for: top-ups are asked for
// until it is covered, and never after.
func TestAFullyPaidSessionStopsAskingForTopUps(t *testing.T) {
	server := meterServer(&recordingAudit{})

	// 100 tinybars a second; a 600-second session opened with one 300-second chunk.
	lease := runner.NewMeteredLeaseForTest("lease1", "0xsess", "0.0.999", big.NewInt(100), big.NewInt(30_000))
	lease.SetSessionLength(600)

	if lease.FullyPaid() {
		t.Fatal("half of a session is not fully paid")
	}
	if got := lease.UnpaidSeconds(); got != 300 {
		t.Fatalf("unpaid = %ds, want 300s", got)
	}

	runner.RewindMeterForTest(lease, 250)
	server.tickMeter(lease)
	if !server.lowCredits(lease) {
		t.Fatal("50s left on a session that is not yet paid for must ask for the next chunk")
	}

	lease.AddCredit(big.NewInt(30_000))
	if !lease.FullyPaid() || lease.UnpaidSeconds() != 0 {
		t.Fatalf("two 300s chunks cover a 600s session: fully_paid=%v unpaid=%d",
			lease.FullyPaid(), lease.UnpaidSeconds())
	}

	runner.RewindMeterForTest(lease, 330)
	server.tickMeter(lease)
	if server.lowCredits(lease) {
		t.Errorf("a fully paid session with %ds left asked for a top-up; it must end when its time is used",
			lease.SecondsRemaining())
	}
}

func mustInt(t *testing.T, s string) *big.Int {
	t.Helper()
	value, ok := new(big.Int).SetString(s, 10)
	if !ok {
		t.Fatalf("not an integer: %q", s)
	}
	return value
}
