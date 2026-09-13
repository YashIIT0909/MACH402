package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"time"

	"github.com/YashIIT0909/ClearGate/agent/internal/escrow"
	"github.com/YashIIT0909/ClearGate/agent/internal/hcs"
	"github.com/YashIIT0909/ClearGate/agent/internal/runner"
	"github.com/YashIIT0909/ClearGate/agent/internal/x402"
)

// maxSessionSpecBytes bounds the request body, like the lease spec it mirrors.
const maxSessionSpecBytes = 1 << 16

// sessionSpec is what a renter asks for. Mirrors SessionSpec in packages/types.
type sessionSpec struct {
	Seconds    int    `json:"seconds"`
	PublicKey  string `json:"public_key"`
	RequireGPU bool   `json:"require_gpu"`
}

// There is no separate session quote endpoint any more, and deliberately so.
// A session is bought with an ordinary x402 cycle, so the authoritative price
// is in the PAYMENT-REQUIRED header of the 402 like every other paid route
// here, and the shopping-ahead terms — rate, bounds, chunk size — are in
// nodespec.LeaseOffer on the free /v1/specs. The escrow flow needed its own
// quote because the renter had to echo contract arguments back; nothing does
// that now, and a second source for the same numbers is a second thing to keep
// in agreement with the first.

// sessionResponse is the 200 body. Mirrors SessionCreated in packages/types.
type sessionResponse struct {
	SessionID string `json:"session_id"`
	LeaseID   string `json:"lease_id"`
	Token     string `json:"token,omitempty"`
	Status    string `json:"status"`
	ExpiresAt string `json:"expires_at"`

	Certificate  string `json:"certificate"`
	SSHUser      string `json:"ssh_user"`
	SSHHost      string `json:"ssh_host,omitempty"`
	SSHPrincipal string `json:"ssh_principal"`

	JupyterURL   string `json:"jupyter_url"`
	JupyterToken string `json:"jupyter_token"`
	TunnelMode   string `json:"tunnel_mode"`

	Transaction    string `json:"transaction"`
	Payer          string `json:"payer"`
	AmountTinybars string `json:"amount_tinybars"`

	PriceTinybarsPerSecond string `json:"price_tinybars_per_second"`
	CreditTinybars         string `json:"credit_tinybars"`
	LowCredits             bool   `json:"low_credits"`
	Seconds                int    `json:"seconds"`
	// SessionSeconds is the session length the renter chose; FullyPaid is
	// whether every chunk of it has been bought.
	SessionSeconds int  `json:"session_seconds"`
	FullyPaid      bool `json:"fully_paid"`
}

// handleCreateSession sells interactive time as a metered, refundable credit.
//
// Mechanically this is handleCreateJob's x402 exact-scheme cycle, against the
// same facilitator, in the same order, with a reachability check before the
// settlement. What the money becomes once it settles is CREDIT, which the meter
// burns down second by second and whose remainder belongs to the renter the
// moment they stop.
//
//  1. validate the spec                  -> 400, and costs nothing
//  2. no payment header                  -> 402 challenge, priced per chunk
//  3. payment header present             -> /verify
//  4. verified                           -> start the container, sign the cert
//  5. container up                       -> point the tunnel at it
//  6. tunnel up                          -> confirm it is actually reachable
//  7. reachable                          -> /settle, and the credit is banked
//
// A failure anywhere before step 7 tears the session down and settles nothing,
// so a session that never came up costs the renter nothing at all — which is
// strictly better than the escrow flow this replaced, where a failed
// provisioning left the renter's deposit needing a recovery call.
//
// The amount charged is capped at leases.session_chunk_seconds no matter how
// long a session the renter asked for. That cap is the bound on the only trust
// gap this design has: between this settlement and the refund, the provider is
// holding money that is partly the renter's. Nothing else in the flow can make
// that gap larger than one chunk.
func (s *Server) handleCreateSession(w http.ResponseWriter, r *http.Request) {
	if !s.sessionsEnabled(w) {
		return
	}

	spec, err := decodeSessionSpec(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.validateSessionSeconds(spec.Seconds); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	// What is actually being sold now. The renter may have asked for an hour;
	// they are charged for a chunk and top up as they go.
	chunkSeconds := s.sessionChunk(spec.Seconds)

	leaseSpec := runner.LeaseSpec{
		Minutes:    secondsToMinutes(chunkSeconds),
		PublicKey:  spec.PublicKey,
		RequireGPU: spec.RequireGPU,
	}

	validateCtx, cancelValidate := context.WithTimeout(r.Context(), 20*time.Second)
	err = s.runner.ValidateLeaseSpec(validateCtx, leaseSpec)
	cancelValidate()
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if s.paused.Load() {
		w.Header().Set("Retry-After", "60")
		writeError(w, http.StatusServiceUnavailable,
			"this node is not accepting new sessions right now; nothing was charged")
		return
	}

	price, err := s.sessionPricePerSecond()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	amount := new(big.Int).Mul(price, big.NewInt(int64(chunkSeconds))).String()

	payload, requirements, ok := s.collectPayment(w, r, amount, s.sessionDescription(chunkSeconds))
	if !ok {
		return
	}

	sessionID, err := newSessionID()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not mint a session id")
		return
	}
	leaseID := newLeaseID()
	token := newAccessToken()

	provisionCtx, cancelProvision := context.WithTimeout(context.WithoutCancel(r.Context()), 3*time.Minute)
	defer cancelProvision()

	lease, err := s.runner.StartLease(provisionCtx, leaseID, token, s.ca.PublicKey(), leaseSpec)
	if err != nil {
		// Nothing settled, so the renter has not been charged.
		s.log.Error("session failed to start", "session", sessionID, "error", err)
		writeError(w, http.StatusInternalServerError, "could not start the session; nothing was charged: "+err.Error())
		return
	}

	certificate, endpoints, err := s.publishLease(provisionCtx, lease)
	if err != nil {
		s.log.Error("session never became reachable; nothing was charged", "session", sessionID, "error", err)
		s.runner.StopLease(provisionCtx, lease, runner.LeaseFailed)
		s.tunnel.Clear(provisionCtx)
		writeError(w, http.StatusBadGateway,
			"the session came up but could not be reached from the internet, so nothing was charged: "+err.Error())
		return
	}

	settleCtx, settleCancel := context.WithTimeout(context.WithoutCancel(r.Context()), 60*time.Second)
	defer settleCancel()

	settlement, err := s.fac.Settle(settleCtx, x402.SettleRequest{
		X402Version:         x402.Version,
		PaymentPayload:      *payload,
		PaymentRequirements: *requirements,
	})
	if err != nil || !settlement.Success {
		s.emit(runner.Event{Kind: runner.EventSettleFailed, LeaseID: leaseID, Detail: reasonOf(settlement)})
		s.log.Error("settlement failed after the session started; tearing it down",
			"session", sessionID, "error", err, "reason", reasonOf(settlement))
		s.runner.StopLease(settleCtx, lease, runner.LeaseFailed)
		s.tunnel.Clear(settleCtx)
		s.respondWithChallengeFor(w, r, settlementFailureMessage(settlement, err), amount, s.sessionDescription(chunkSeconds))
		return
	}

	// The credit is what the facilitator confirmed moved, never a figure
	// recomputed from the price table. The refund trail's whole claim is that
	// the node owes back money it actually received, so the ledger has to start
	// from the settled amount rather than from what the node meant to charge.
	credit, ok := new(big.Int).SetString(requirements.Amount, 10)
	if !ok {
		// Unreachable: s.requirements built this string from a *big.Int a few
		// lines ago. Refusing to meter an amount that cannot be parsed is still
		// the right answer, because the alternative is a session with no ledger.
		s.log.Error("settled amount is not an integer", "session", sessionID, "amount", requirements.Amount)
		s.runner.StopLease(settleCtx, lease, runner.LeaseFailed)
		s.tunnel.Clear(settleCtx)
		writeError(w, http.StatusInternalServerError, "this node settled a payment it cannot meter; contact the provider")
		return
	}
	lease.MarkMetered(sessionID, settlement.Payer, price, credit)
	// The length the renter chose is the length the session runs for: top-ups
	// buy the rest of it in chunks, and stop once it is paid for.
	lease.SetSessionLength(int64(spec.Seconds))

	s.recordSessionPayment(lease, settlement, requirements, hcs.KindSessionOpen)

	if err := x402.WriteSettlement(w, settlement); err != nil {
		s.log.Error("could not attach settlement header", "error", err)
	}
	writeJSON(w, http.StatusOK, sessionResponse{
		SessionID:              sessionID,
		LeaseID:                leaseID,
		Token:                  token,
		Status:                 string(lease.Status()),
		ExpiresAt:              lease.ExpiresAt().UTC().Format(time.RFC3339),
		Certificate:            certificate,
		SSHUser:                leaseSSHUser,
		SSHHost:                endpoints.SSHHost,
		SSHPrincipal:           leaseID,
		JupyterURL:             endpoints.JupyterURL,
		JupyterToken:           lease.JupyterToken(),
		TunnelMode:             endpoints.Mode,
		Transaction:            settlement.Transaction,
		Payer:                  settlement.Payer,
		AmountTinybars:         requirements.Amount,
		PriceTinybarsPerSecond: price.String(),
		CreditTinybars:         lease.Credit().String(),
		LowCredits:             s.lowCredits(lease),
		Seconds:                int(lease.SecondsRemaining()),
		SessionSeconds:         spec.Seconds,
		FullyPaid:              lease.FullyPaid(),
	})
}

// handleTopUpSession banks another paid chunk onto a live session.
//
// A top-up buys credit, which is worth nothing until it is banked — so it is
// banked after the settlement, and a failed payment leaves the session exactly
// as it was. Nothing has to be applied ahead of the money and reverted if the
// money does not arrive.
func (s *Server) handleTopUpSession(w http.ResponseWriter, r *http.Request) {
	if !s.sessionsEnabled(w) {
		return
	}

	lease, ok := s.authorizeSession(w, r)
	if !ok {
		return
	}
	if lease.Status().IsTerminal() {
		writeError(w, http.StatusConflict,
			fmt.Sprintf("this session is already %s and cannot be topped up", lease.Status()))
		return
	}

	price := lease.PricePerSecond()
	if price.Sign() <= 0 {
		writeError(w, http.StatusConflict, "this session has no meter; it cannot be topped up")
		return
	}

	// A top-up buys the next chunk of the session the renter chose, and never
	// more than is left of it.
	chunkSeconds := s.cfg.Leases.SessionChunkSeconds
	if lease.SessionLength() > 0 {
		unpaid := lease.UnpaidSeconds()
		if unpaid <= 0 {
			writeError(w, http.StatusConflict, fmt.Sprintf(
				"this session was bought for %d seconds and is already paid for in full; it ends when "+
					"that time is used — start a new session to keep going", lease.SessionLength()))
			return
		}
		chunkSeconds = int(min(int64(chunkSeconds), unpaid))
	}

	// The node's own total cap, checked before the renter is asked to pay so a
	// chunk that would exceed it is refused for free.
	maxTotal := s.cfg.Leases.MaxTotalMinutes * 60
	boughtSeconds := lease.PaidSeconds()
	if boughtSeconds+int64(chunkSeconds) > int64(maxTotal) {
		writeError(w, http.StatusConflict,
			fmt.Sprintf("this node caps a session at %d seconds total; %d have been bought already",
				maxTotal, boughtSeconds))
		return
	}

	amount := new(big.Int).Mul(price, big.NewInt(int64(chunkSeconds))).String()

	payload, requirements, ok := s.collectPayment(w, r, amount, s.sessionDescription(chunkSeconds))
	if !ok {
		return
	}

	settleCtx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 60*time.Second)
	defer cancel()

	settlement, err := s.fac.Settle(settleCtx, x402.SettleRequest{
		X402Version:         x402.Version,
		PaymentPayload:      *payload,
		PaymentRequirements: *requirements,
	})
	if err != nil || !settlement.Success {
		s.emit(runner.Event{Kind: runner.EventSettleFailed, LeaseID: lease.ID, Detail: reasonOf(settlement)})
		s.log.Error("top-up settlement failed; the session is unchanged",
			"session", lease.SessionID(), "reason", reasonOf(settlement))
		s.respondWithChallengeFor(w, r, settlementFailureMessage(settlement, err), amount, s.sessionDescription(chunkSeconds))
		return
	}

	credit, ok := new(big.Int).SetString(requirements.Amount, 10)
	if !ok {
		s.log.Error("settled top-up is not an integer", "session", lease.SessionID(), "amount", requirements.Amount)
		writeError(w, http.StatusInternalServerError, "this node settled a payment it cannot meter; contact the provider")
		return
	}
	lease.AddCredit(credit)

	// Paying thaws a frozen session, for the same reason extending thaws a
	// frozen lease: the renter's work is still in the paused container, and
	// buying more time is what resumption means. The meter's clock restarts
	// with the thaw, so the frozen stretch is never charged for.
	if lease.Status() == runner.LeasePaused {
		if err := s.runner.ResumeLease(settleCtx, lease); err != nil {
			s.log.Error("could not resume a topped-up session", "session", lease.SessionID(), "error", err)
		}
	}

	s.recordSessionPayment(lease, settlement, requirements, hcs.KindSessionOpen)

	if err := x402.WriteSettlement(w, settlement); err != nil {
		s.log.Error("could not attach settlement header", "error", err)
	}
	writeJSON(w, http.StatusOK, s.sessionState(lease))
}

// handleSessionState reports status, credit and time remaining. Free,
// token-gated: charging for the poll that decides whether to pay would be
// absurd, and this is also how a renter reads what they are owed.
func (s *Server) handleSessionState(w http.ResponseWriter, r *http.Request) {
	if !s.sessionsEnabled(w) {
		return
	}
	lease, ok := s.authorizeSession(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, s.sessionState(lease))
}

// handleSessionStop ends a session early. Free, token-gated.
//
// This is where metering earns its place: stopping charges for the seconds
// actually used and returns the rest. The final burn happens first, so what is
// refunded is measured against the same meter that has been publishing burn
// checkpoints all along rather than against a fresh calculation at the moment
// the provider has the most reason to prefer a different answer.
func (s *Server) handleSessionStop(w http.ResponseWriter, r *http.Request) {
	if !s.sessionsEnabled(w) {
		return
	}
	lease, ok := s.authorizeSession(w, r)
	if !ok {
		return
	}

	stopCtx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 2*time.Minute)
	defer cancel()

	// Charge for the time up to this instant before anything is torn down.
	if lease.Status() == runner.LeaseActive {
		lease.Burn(time.Now())
	}

	s.runner.StopLease(stopCtx, lease, runner.LeaseStopped)
	s.tunnel.Clear(stopCtx)

	// Settle before answering, so what comes back already reflects the refund.
	// A renter reading `settle_state: done` and `credit_tinybars: 0` has their
	// answer in the same round trip they used to stop.
	s.settleSession(stopCtx, lease)
	writeJSON(w, http.StatusOK, s.sessionState(lease))
}

// tickMeter charges a live session for the time since the last tick and
// publishes what the node would owe if it stopped right now.
//
// The publish is unconditional and happens on every tick, not only at open and
// close, and that is the point rather than a convenience. Between a chunk
// payment and its refund, the renter's remaining money is in the provider's
// hands and only the provider's bookkeeping says how much of it is still the
// renter's. Publishing that figure continuously, to a topic ordered by Hedera
// consensus and bound by a running hash, converts it from a claim into a public
// fact recorded before anyone had a motive to shade it. A provider who later
// refuses to refund is refusing a number they themselves signed, repeatedly, in
// advance.
//
// Nothing about freezing or reaping lives here: credit reaching zero moves the
// lease's expiry to now, and the sweep's existing LeaseActive branch does the
// rest. One scheduler, one freeze path.
func (s *Server) tickMeter(lease *runner.Lease) {
	burned, remaining := lease.Burn(time.Now())

	price := lease.PricePerSecond()
	var elapsed int64
	if price.Sign() > 0 {
		elapsed = new(big.Int).Div(lease.Burned(), price).Int64()
	}

	s.publishAudit(hcs.AuditMessage{
		Kind:           hcs.KindSessionBurn,
		SessionID:      lease.SessionID(),
		JobID:          lease.ID,
		Payer:          lease.Payer(),
		PayTo:          s.cfg.PayTo,
		AmountTinybars: lease.Burned().String(),
		RefundTinybars: remaining.String(),
		PricePerSecond: price.String(),
		ElapsedSecs:    elapsed,
		Asset:          s.cfg.Asset,
		Network:        s.cfg.Network,
	})

	s.log.Debug("session meter ticked",
		"session", lease.SessionID(),
		"burned_tinybars", burned,
		"owed_tinybars", remaining,
	)

	// Mirrored onto the dashboard's event feed, not just the audit topic: a
	// provider watching their own node locally should see a session's credit
	// counting down in real time without going and reading their own HCS topic
	// through a mirror node to find out.
	s.emit(runner.Event{
		Kind:     runner.EventSessionBurn,
		LeaseID:  lease.ID,
		Payer:    lease.Payer(),
		Tinybars: remaining.String(),
		Detail:   fmt.Sprintf("burned %s, %s owed if stopped now", burned.String(), remaining.String()),
	})
}

// settleSession returns a finished session's unburned credit to its renter.
//
// Called from every path a session can end by — the renter stopping it, the
// sweep reaping it after its grace period — and idempotent, because more than
// one of those can fire for the same session.
//
// A node without a working sidecar cannot pay, and says so with everything needed to
// pay by hand: the session, the payer and the amount. That is not an error
// state that loses anyone money, but it is a debt, and the honest thing is to
// log it as one rather than to let it disappear. The burn trail on the audit
// topic is the record that outlives the log.
func (s *Server) settleSession(ctx context.Context, lease *runner.Lease) {
	sessionID := lease.SessionID()
	// Not a session yet, or already settled. The first case is a lease whose
	// opening chunk never settled, which the sweep can still see.
	if sessionID == "" || lease.SettleState() != runner.SettlePending {
		return
	}

	owed := lease.Credit()
	payer := lease.Payer()

	if owed.Sign() <= 0 {
		// The session ran its credit all the way down. Nothing is owed, which
		// is a settled session rather than an unpaid one.
		lease.MarkSettled(owed)
		s.recordSessionSettlement(lease, owed, "")
		return
	}

	if s.sidecar == nil {
		s.log.Warn("session ended with credit owed back, and this node cannot refund it itself; "+
			"pay it by hand, and check that cleargate-hedera is installed",
			"session", sessionID, "payer", payer, "owed_tinybars", owed.String())
		s.recordSessionSettlement(lease, owed, "")
		return
	}

	transaction, err := s.sidecar.Refund(ctx, payer, owed, "cleargate session "+sessionID)
	if err != nil {
		// The debt stands. Left as SettlePending on purpose: the sweep calls
		// this again on its next pass, so a transient failure retries itself
		// rather than quietly writing the renter's money off.
		s.log.Error("could not refund a session's unburned credit; it is still owed",
			"session", sessionID, "payer", payer, "owed_tinybars", owed.String(), "error", err)
		return
	}

	lease.MarkSettled(owed)
	s.log.Info("session refunded",
		"session", sessionID, "payer", payer, "refund_tinybars", owed.String(), "transaction", transaction)
	s.recordSessionSettlement(lease, owed, transaction)
}

// recordSessionPayment logs a settled chunk locally and to the audit topic.
//
// A chunk payment is an ordinary settlement — the money is in pay_to, exactly
// as a lease's is — so it goes through the same receipt shape. What makes a
// session different is the burn trail that follows it, not this.
func (s *Server) recordSessionPayment(
	lease *runner.Lease,
	settlement *x402.SettleResponse,
	requirements *x402.PaymentRequirements,
	kind string,
) {
	s.recordLeasePayment(lease.ID, settlement, requirements)

	s.publishAudit(hcs.AuditMessage{
		Kind:           kind,
		SessionID:      lease.SessionID(),
		JobID:          lease.ID,
		Transaction:    settlement.Transaction,
		Payer:          settlement.Payer,
		PayTo:          requirements.PayTo,
		AmountTinybars: requirements.Amount,
		Asset:          requirements.Asset,
		Network:        requirements.Network,
		PricePerSecond: lease.PricePerSecond().String(),
		DurationSecs:   lease.SecondsRemaining(),
		RefundTinybars: lease.Credit().String(),
	})

	s.log.Info("session funded",
		"session", lease.SessionID(),
		"payer", settlement.Payer,
		"amount_tinybars", requirements.Amount,
		"credit_tinybars", lease.Credit().String(),
	)
}

// recordSessionSettlement publishes the closing half of a session's trail: what
// was earned, what was returned, and the transaction that returned it.
//
// An empty transaction is meaningful and is published rather than suppressed —
// it says the node reached the end of a session owing this much and did not pay
// it, which is precisely the thing a renter needs on the record.
func (s *Server) recordSessionSettlement(lease *runner.Lease, refund *big.Int, transaction string) {
	price := lease.PricePerSecond()
	earned := lease.Burned()

	var elapsed int64
	if price.Sign() > 0 {
		elapsed = new(big.Int).Div(earned, price).Int64()
	}

	s.publishAudit(hcs.AuditMessage{
		Kind:           hcs.KindSessionSettled,
		SessionID:      lease.SessionID(),
		JobID:          lease.ID,
		Transaction:    transaction,
		Payer:          lease.Payer(),
		PayTo:          s.cfg.PayTo,
		AmountTinybars: earned.String(),
		RefundTinybars: refund.String(),
		PricePerSecond: price.String(),
		ElapsedSecs:    elapsed,
		Asset:          s.cfg.Asset,
		Network:        s.cfg.Network,
	})
}

// --- quoting and helpers ---

// sessionChunk is how many seconds one payment buys.
//
// Capped at leases.session_chunk_seconds whatever the renter asked for. A
// renter who wants an hour gets a chunk now and tops up as they go, which
// costs them nothing extra and bounds what the provider is ever holding of
// theirs to a single chunk.
func (s *Server) sessionChunk(requested int) int {
	chunk := s.cfg.Leases.SessionChunkSeconds
	if requested > 0 && requested < chunk {
		return requested
	}
	return chunk
}

// sessionDescription is what the 402 challenge says the money is for. It names
// the chunk rather than the session the renter asked for, because the chunk is
// what this particular payment buys.
func (s *Server) sessionDescription(seconds int) string {
	return fmt.Sprintf("%d seconds of metered, refundable GPU time on ClearGate node %s",
		seconds, s.cfg.NodeID)
}

// sessionPricePerSecond is the rate a session's credit burns at.
//
// Derived from the per-minute lease price unless a provider set a per-second
// figure explicitly. Converted in exactly one place: the meter multiplies this
// by elapsed seconds, so two conversions that disagreed would mean the node
// quoting one rate and burning at another.
func (s *Server) sessionPricePerSecond() (*big.Int, error) {
	if explicit := s.cfg.Leases.PriceTinybarsPerSecond; explicit != "" {
		price, ok := new(big.Int).SetString(explicit, 10)
		if !ok {
			return nil, errors.New("this node's per-second session price is misconfigured")
		}
		return price, nil
	}
	price, err := escrow.PricePerSecond(s.cfg.Leases.PriceTinybarsPerMinute)
	if err != nil {
		return nil, fmt.Errorf("this node's lease price is misconfigured: %w", err)
	}
	return price, nil
}

func (s *Server) validateSessionSeconds(seconds int) error {
	leases := s.cfg.Leases
	if seconds < leases.MinMinutes*60 {
		return fmt.Errorf("this node's minimum session is %d seconds", leases.MinMinutes*60)
	}
	if seconds > leases.MaxMinutes*60 {
		return fmt.Errorf("this node's maximum session is %d seconds", leases.MaxMinutes*60)
	}
	return nil
}

// lowCredits reports whether the renter should be buying another chunk.
//
// Computed here rather than by the client, because the threshold has to be
// bigger than the sweep interval plus a payment round trip — numbers the node
// knows and a client would have to guess.
func (s *Server) lowCredits(lease *runner.Lease) bool {
	// A session paid for in full has nothing left to buy: it ends when its
	// credit is used rather than being topped up past the length chosen.
	if lease.FullyPaid() {
		return false
	}
	return lease.SecondsRemaining() < int64(s.cfg.Leases.LowCreditThresholdSeconds)
}

// sessionState renders the read-only view. Everything about money comes from
// the lease's own ledger, which is the same ledger the burn checkpoints are
// published from — so what a renter reads here and what a third party reads off
// the audit topic cannot disagree.
func (s *Server) sessionState(lease *runner.Lease) map[string]any {
	base := lease.State()

	return map[string]any{
		"session_id":                lease.SessionID(),
		"lease_id":                  base.LeaseID,
		"status":                    base.Status,
		"created_at":                base.CreatedAt,
		"expires_at":                base.ExpiresAt,
		"seconds_remaining":         lease.SecondsRemaining(),
		"paid_seconds":              lease.PaidSeconds(),
		"session_seconds":           lease.SessionLength(),
		"fully_paid":                lease.FullyPaid(),
		"gpu":                       base.GPU,
		"price_tinybars_per_second": lease.PricePerSecond().String(),
		"credit_tinybars":           lease.Credit().String(),
		"burned_tinybars":           lease.Burned().String(),
		// What was actually paid back, once settled. Deliberately not the same
		// field as credit_tinybars: credit is "owed right now" and is correctly
		// zero the instant it has been refunded, so a caller reading state after
		// the session ended needs this one to see what they got.
		"refunded_tinybars": lease.Refunded().String(),
		"low_credits":       s.lowCredits(lease),
		"settle_state":      string(lease.SettleState()),
		"error":             base.Error,
	}
}

// sessionsEnabled answers 404 on a node that does not sell metered sessions.
//
// 404 rather than 403: a node that never opted in should be indistinguishable
// from one running a version from before sessions existed.
func (s *Server) sessionsEnabled(w http.ResponseWriter) bool {
	if s.sessions && s.runner.LeasesEnabled() && s.ca != nil && s.tunnel != nil {
		return true
	}
	writeError(w, http.StatusNotFound, "this node does not offer metered sessions")
	return false
}

// authorizeSession resolves a session by id and checks the caller's token.
func (s *Server) authorizeSession(w http.ResponseWriter, r *http.Request) (*runner.Lease, bool) {
	id := r.PathValue("id")

	lease, ok := s.runner.ActiveLease()
	if !ok || lease.SessionID() != id || !lease.AuthorizedBy(bearerToken(r)) {
		writeError(w, http.StatusNotFound, "no such session, or the token does not authorize it")
		return nil, false
	}
	return lease, true
}

func decodeSessionSpec(r *http.Request) (sessionSpec, error) {
	var spec sessionSpec
	decoder := json.NewDecoder(io.LimitReader(r.Body, maxSessionSpecBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&spec); err != nil {
		return sessionSpec{}, fmt.Errorf("invalid session spec: %w", err)
	}
	return spec, nil
}

// newSessionID mints the key a renter's session is tracked by.
//
// Minted by the node rather than the renter, and unguessable, because it names
// the session on a public audit topic: a renter who could choose it could
// collide it with someone else's trail.
func newSessionID() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return "0x" + hex.EncodeToString(raw), nil
}

// secondsToMinutes rounds up, so the container is always provisioned for at
// least as long as the renter paid for.
func secondsToMinutes(seconds int) int {
	minutes := seconds / 60
	if seconds%60 != 0 {
		minutes++
	}
	if minutes < 1 {
		minutes = 1
	}
	return minutes
}
