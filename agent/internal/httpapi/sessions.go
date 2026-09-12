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
	"github.com/YashIIT0909/ClearGate/agent/internal/receipts"
	"github.com/YashIIT0909/ClearGate/agent/internal/runner"
)

// maxSessionSpecBytes bounds the request body, like the lease spec it mirrors.
const maxSessionSpecBytes = 1 << 16

// HeaderDepositProof carries the renter's proof that they funded a session.
//
// A deliberately separate header from PAYMENT-SIGNATURE. The x402 header
// carries a signed-but-unsettled transfer for a facilitator to submit; this
// carries a transaction id for something already final on Hedera consensus.
// Reusing the name would mean two incompatible payloads under one header, and
// the first client to send the wrong one would get a confusing failure rather
// than a clear 402.
const HeaderDepositProof = "PAYMENT-DEPOSIT"

// sessionSpec is what a renter asks for. Mirrors SessionSpec in packages/types.
type sessionSpec struct {
	Seconds    int    `json:"seconds"`
	PublicKey  string `json:"public_key"`
	RequireGPU bool   `json:"require_gpu"`
}

// sessionQuote is the 402 body: everything needed to make the deposit.
type sessionQuote struct {
	SessionID              string `json:"session_id"`
	EscrowContract         string `json:"escrow_contract"`
	ProviderAddress        string `json:"provider_address"`
	PriceTinybarsPerSecond string `json:"price_tinybars_per_second"`
	MinSeconds             int    `json:"min_seconds"`
	MaxSeconds             int    `json:"max_seconds"`
	MaxTotalSeconds        int    `json:"max_total_seconds"`
	Network                string `json:"network"`
}

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

	DepositTransaction     string `json:"deposit_transaction"`
	PriceTinybarsPerSecond string `json:"price_tinybars_per_second"`
	DepositedTinybars      string `json:"deposited_tinybars"`
	Seconds                int    `json:"seconds"`
}

// handleCreateSession sells interactive time paid for through escrow.
//
// The ordering is handleCreateLease's, with one substitution: where the lease
// flow calls the facilitator's /verify, this reads Hedera's mirror node. The
// renter has already signed and submitted their own deposit, so there is
// nothing to hand a facilitator — the payment is final on consensus and the
// node's job is to check it, which needs no key at all.
//
//  1. validate the spec               -> 400, and costs nothing
//  2. no deposit proof                -> 402 carrying the quote
//  3. deposit proof present           -> verify it against the chain
//  4. verified                        -> start the container, sign the cert
//  5. container up                    -> point the tunnel at it
//  6. tunnel up                       -> confirm it is actually reachable
//  7. reachable                       -> receipt, audit trail, connection details
//
// Step 7 has no settle call, and that difference is the whole point. A lease
// takes the money at step 7 and can never give it back; a session's money is
// already in the contract, and what happens at the end is a split computed
// from elapsed time. So a session that fails at step 5 or 6 costs the renter
// nothing real: nothing was provisioned, almost no time elapsed, and calling
// settle returns essentially the whole deposit.
func (s *Server) handleCreateSession(w http.ResponseWriter, r *http.Request) {
	if !s.sessionsEnabled(w) {
		return
	}

	spec, err := decodeSessionSpec(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	leaseSpec := runner.LeaseSpec{
		Minutes:    secondsToMinutes(spec.Seconds),
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
	if err := s.validateSessionSeconds(spec.Seconds); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if s.paused.Load() {
		w.Header().Set("Retry-After", "60")
		writeError(w, http.StatusServiceUnavailable,
			"this node is not accepting new sessions right now; nothing was charged")
		return
	}

	proof := r.Header.Get(HeaderDepositProof)
	if proof == "" {
		// No deposit yet. Mint a session id and quote — the renter deposits
		// against exactly these terms and comes back.
		sessionID, err := newSessionID()
		if err != nil {
			writeError(w, http.StatusInternalServerError, "could not mint a session id")
			return
		}
		s.respondWithSessionQuote(w, sessionID)
		return
	}

	sessionID := r.Header.Get("PAYMENT-SESSION")
	if sessionID == "" {
		writeError(w, http.StatusBadRequest,
			"a deposit proof needs the PAYMENT-SESSION header naming the session it funded")
		return
	}

	quote, err := s.sessionQuoteFor(sessionID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	verifyCtx, cancelVerify := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancelVerify()

	onchain, err := s.escrow.AwaitDeposit(verifyCtx, proof, escrow.Quote{
		SessionID:       sessionID,
		ProviderAddress: s.providerAddress,
		PricePerSecond:  mustBig(quote.PriceTinybarsPerSecond),
		// The deposit has to cover what was ASKED FOR, not merely the node's
		// minimum. Otherwise a renter could request an hour, deposit for the
		// minimum, and be handed a container sized and scheduled for the hour.
		MinSeconds: int64(spec.Seconds),
	})
	if err != nil {
		var rejection *escrow.Rejection
		if errors.As(err, &rejection) {
			// Verified and refused: the deposit exists and does not match what
			// this node offered. Saying which part disagreed is safe — it is
			// all public on-chain data — and it is the only way the renter can
			// fix it.
			s.emit(runner.Event{Kind: runner.EventRejected, LeaseID: sessionID, Detail: rejection.Reason})
			writeError(w, http.StatusPaymentRequired, rejection.Reason)
			return
		}
		s.log.Error("could not verify a session deposit", "session", sessionID, "error", err)
		writeError(w, http.StatusBadGateway,
			"could not confirm the deposit against Hedera's mirror node; nothing was provisioned: "+err.Error())
		return
	}

	// A session whose paid time has already run out buys nothing. Without this,
	// a renter could re-present an expired-but-unsettled deposit and be given a
	// fresh container: the contract would still charge them for the wall-clock
	// time, so it is not theft, but the node would be running work nobody is
	// paying it for from here on.
	if expiresAt := onchain.ExpiresAt(); expiresAt <= time.Now().Unix() {
		writeError(w, http.StatusPaymentRequired,
			"that session's paid time has already elapsed; open a new one, and call settle to close this one")
		return
	}

	s.emit(runner.Event{Kind: runner.EventVerified, LeaseID: sessionID, Payer: onchain.Renter})

	leaseID := newLeaseID()
	token := newAccessToken()

	provisionCtx, cancelProvision := context.WithTimeout(context.WithoutCancel(r.Context()), 3*time.Minute)
	defer cancelProvision()

	lease, err := s.runner.StartLease(provisionCtx, leaseID, token, s.ca.PublicKey(), leaseSpec)
	if err != nil {
		s.log.Error("session failed to start", "session", sessionID, "error", err)
		writeError(w, http.StatusInternalServerError,
			"could not start the session; call settle on the escrow contract to recover your deposit: "+err.Error())
		return
	}

	// The contract's startTime is authoritative about when the paid time ends,
	// because the contract is what computes the final split. Mirroring it here
	// rather than measuring locally keeps the freeze from landing early or late.
	expiresAt := time.Unix(onchain.ExpiresAt(), 0)
	lease.MarkEscrow(sessionID, proof, expiresAt, onchain.Duration.Int64())

	certificate, endpoints, err := s.publishLease(provisionCtx, lease)
	if err != nil {
		s.log.Error("session never became reachable", "session", sessionID, "error", err)
		s.runner.StopLease(provisionCtx, lease, runner.LeaseFailed)
		s.tunnel.Clear(provisionCtx)
		// Nothing usable ever ran, and barely any time has elapsed, so settling
		// now returns almost the entire deposit. The node offers to do it so a
		// renter is not left chasing a refund for a failure that was not theirs.
		s.settleSession(provisionCtx, lease)
		writeError(w, http.StatusBadGateway,
			"the session came up but could not be reached from the internet, so it was torn down "+
				"and your deposit refunded: "+err.Error())
		return
	}

	s.recordSessionPayment(lease, onchain, hcs.KindSessionOpen)

	writeJSON(w, http.StatusOK, sessionResponse{
		SessionID:              sessionID,
		LeaseID:                leaseID,
		Token:                  token,
		Status:                 string(lease.Status()),
		ExpiresAt:              expiresAt.UTC().Format(time.RFC3339),
		Certificate:            certificate,
		SSHUser:                leaseSSHUser,
		SSHHost:                endpoints.SSHHost,
		SSHPrincipal:           leaseID,
		JupyterURL:             endpoints.JupyterURL,
		JupyterToken:           lease.JupyterToken(),
		TunnelMode:             endpoints.Mode,
		DepositTransaction:     proof,
		PriceTinybarsPerSecond: onchain.PricePerSecond.String(),
		DepositedTinybars:      onchain.Deposited.String(),
		Seconds:                int(onchain.Duration.Int64()),
	})
}

// handleTopUpSession extends a live session after a verified on-chain top-up.
//
// Unlike a lease extension there is nothing to revert on failure: the renter's
// money is already in the contract before this endpoint is called, and the node
// is only reading what the chain says. Either the top-up is there — in which
// case the time is theirs — or it is not, and nothing changed.
func (s *Server) handleTopUpSession(w http.ResponseWriter, r *http.Request) {
	if !s.sessionsEnabled(w) {
		return
	}

	lease, ok := s.authorizeSession(w, r)
	if !ok {
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	// Read the contract rather than trusting the request: the renter's top-up
	// already moved money, so the only question is what the chain now says the
	// paid duration is.
	//
	// With the same bounded retry the deposit path uses. A renter calls this the
	// moment their top-up reaches consensus, and the mirror node is seconds
	// behind — reading once would reject a payment that has already happened,
	// and do it with their session about to freeze.
	onchain, err := s.escrow.AwaitTopUp(ctx, lease.SessionID(), lease.PaidSeconds())
	if err != nil {
		var rejection *escrow.Rejection
		switch {
		case errors.As(err, &rejection):
			writeError(w, http.StatusConflict, rejection.Reason)
		case errors.Is(err, escrow.ErrNoAdditionalTime):
			writeError(w, http.StatusPaymentRequired,
				"no additional time is visible on-chain yet; top up the escrow contract, then retry")
		default:
			writeError(w, http.StatusBadGateway, "could not read the session from the chain: "+err.Error())
		}
		return
	}

	expiresAt := time.Unix(onchain.ExpiresAt(), 0)

	paidSeconds := int(onchain.Duration.Int64())
	if max := s.cfg.Leases.MaxTotalMinutes * 60; paidSeconds > max {
		// The renter has bought more than this node sells. Their money is not
		// lost — they get it back at settle — but the extra time is not served.
		writeError(w, http.StatusConflict,
			fmt.Sprintf("this node caps a session at %d seconds total; the extra deposit is refunded at settle", max))
		return
	}

	// Buying time on a frozen session thaws it, exactly as extending a lease
	// does — the renter's work is still in the paused container.
	if lease.Status() == runner.LeasePaused {
		if err := s.runner.ResumeLease(ctx, lease); err != nil {
			s.log.Error("could not resume a topped-up session", "session", lease.SessionID(), "error", err)
		}
	}
	lease.ExtendPaidUntil(expiresAt, int64(paidSeconds))

	s.recordSessionPayment(lease, onchain, hcs.KindSessionOpen)
	writeJSON(w, http.StatusOK, s.sessionState(lease, onchain))
}

// handleSessionState reports status and time remaining. Free, token-gated.
func (s *Server) handleSessionState(w http.ResponseWriter, r *http.Request) {
	if !s.sessionsEnabled(w) {
		return
	}
	lease, ok := s.authorizeSession(w, r)
	if !ok {
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	// Best-effort chain read: a mirror hiccup should degrade this to the node's
	// own view rather than fail a poll the renter's CLI depends on.
	onchain, err := s.escrow.Session(ctx, lease.SessionID())
	if err != nil {
		s.log.Warn("could not read session state from the chain", "session", lease.SessionID(), "error", err)
		onchain = nil
	}
	writeJSON(w, http.StatusOK, s.sessionState(lease, onchain))
}

// handleSessionStop ends a session early. Free, token-gated.
//
// This is where escrow earns its place. Stopping a lease ends the meter and
// forfeits the rest of the slice; stopping a session tears the container down
// AND closes the contract, so the renter is paid back for time they did not
// use. The node settles on their behalf when it can, rather than leaving them
// to remember — but `settle` is permissionless, so a renter whose node has
// vanished can always close it themselves.
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

	s.runner.StopLease(stopCtx, lease, runner.LeaseStopped)
	s.tunnel.Clear(stopCtx)
	s.settleSession(stopCtx, lease)

	onchain, err := s.escrow.Session(stopCtx, lease.SessionID())
	if err != nil {
		onchain = nil
	}
	writeJSON(w, http.StatusOK, s.sessionState(lease, onchain))
}

// settleSession closes a session on-chain, if this node is configured to.
//
// Not an error when it cannot. `settle` is permissionless and one-shot: the
// renter's own clean exit may already have closed it, and if nobody has, the
// money is still safely in the contract waiting for whoever calls first. So a
// failure here is logged with the session id — which is all anyone needs to
// close it by hand — and nothing more.
func (s *Server) settleSession(ctx context.Context, lease *runner.Lease) {
	sessionID := lease.SessionID()
	// Not a session, already settled, or this node does not sell sessions at
	// all — the last case matters because the lease sweep calls this for every
	// lease, including direct-paid ones on a node with no escrow configured.
	if s.escrow == nil || sessionID == "" || lease.SettleState() != runner.SettlePending {
		return
	}
	if s.sidecar == nil {
		// self_settle is off. Say so once, with the id, so a provider who wants
		// their payout knows exactly what to call.
		s.log.Info("session finished and is not settled on-chain; anyone may call settle",
			"session", sessionID, "contract", s.escrow.Contract())
		return
	}

	transaction, err := s.sidecar.SettleSession(ctx, s.escrow.Contract(), sessionID)
	if err != nil {
		s.log.Error("could not settle the session on-chain; the deposit is still in the contract",
			"session", sessionID, "error", err)
		return
	}
	lease.MarkSettled()
	s.log.Info("session settled on-chain", "session", sessionID, "transaction", transaction)

	if onchain, err := s.escrow.Session(ctx, sessionID); err == nil {
		s.recordSessionSettlement(lease, onchain, transaction)
	}
}

// recordSessionPayment logs a verified deposit locally and to the audit topic.
//
// A session's "payment" is not a settlement — the money is in the contract, not
// in the provider's account — so the receipt records what was deposited and
// under what terms. The matching settle record comes later and the two together
// are what let a third party check that the payout matched the promise.
func (s *Server) recordSessionPayment(lease *runner.Lease, onchain *escrow.Session, kind string) {
	sessionID := lease.SessionID()

	s.emit(runner.Event{
		Kind:        runner.EventSettled,
		LeaseID:     lease.ID,
		Payer:       onchain.Renter,
		Transaction: lease.DepositTransaction(),
		Tinybars:    onchain.Deposited.String(),
	})

	receipt := receipts.Receipt{
		JobID:          sessionID,
		Transaction:    lease.DepositTransaction(),
		Payer:          onchain.Renter,
		PayTo:          s.cfg.PayTo,
		AmountTinybars: onchain.Deposited.String(),
		Asset:          s.cfg.Asset,
		Network:        s.cfg.Network,
	}
	if err := s.receipts.Append(receipt); err != nil {
		s.log.Error("could not write session receipt", "session", sessionID, "error", err)
	}

	s.publishAudit(hcs.AuditMessage{
		Kind:           kind,
		SessionID:      sessionID,
		JobID:          lease.ID,
		Transaction:    lease.DepositTransaction(),
		Payer:          onchain.Renter,
		PayTo:          s.cfg.PayTo,
		AmountTinybars: onchain.Deposited.String(),
		Asset:          s.cfg.Asset,
		Network:        s.cfg.Network,
		PricePerSecond: onchain.PricePerSecond.String(),
		DurationSecs:   onchain.Duration.Int64(),
	})

	s.log.Info("session funded",
		"session", sessionID,
		"payer", onchain.Renter,
		"deposited_tinybars", onchain.Deposited,
		"seconds", onchain.Duration,
	)
}

// recordSessionSettlement publishes the closing half of a session's trail: what
// was actually paid out, against the deposit published when it opened.
func (s *Server) recordSessionSettlement(lease *runner.Lease, onchain *escrow.Session, transaction string) {
	elapsed := onchain.Duration.Int64()
	if actual := time.Now().Unix() - onchain.StartTime.Int64(); actual < elapsed {
		elapsed = actual
	}
	earned := new(big.Int).Mul(onchain.PricePerSecond, big.NewInt(elapsed))
	refund := new(big.Int).Sub(onchain.Deposited, earned)

	s.publishAudit(hcs.AuditMessage{
		Kind:           hcs.KindSessionSettled,
		SessionID:      lease.SessionID(),
		JobID:          lease.ID,
		Transaction:    transaction,
		Payer:          onchain.Renter,
		PayTo:          s.cfg.PayTo,
		AmountTinybars: earned.String(),
		RefundTinybars: refund.String(),
		PricePerSecond: onchain.PricePerSecond.String(),
		DurationSecs:   onchain.Duration.Int64(),
		ElapsedSecs:    elapsed,
		Asset:          s.cfg.Asset,
		Network:        s.cfg.Network,
	})
}

// --- quoting and helpers ---

// respondWithSessionQuote answers 402 with everything needed to deposit.
//
// Shaped like the x402 challenge it sits beside — authoritative in the body,
// 402 status, no-store — but carrying contract terms instead of transfer
// requirements, because there is no transfer to sign.
func (s *Server) respondWithSessionQuote(w http.ResponseWriter, sessionID string) {
	quote, err := s.sessionQuoteFor(sessionID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	s.emit(runner.Event{Kind: runner.EventChallenged, LeaseID: sessionID})

	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusPaymentRequired, map[string]any{
		"error": "Deposit required",
		"quote": quote,
		"how": fmt.Sprintf(
			"call openSession(%s, %s, %s, <seconds>) on the escrow contract, "+
				"then repeat this request with %s set to the transaction id and PAYMENT-SESSION set to the session id",
			sessionID, quote.ProviderAddress, quote.PriceTinybarsPerSecond, HeaderDepositProof),
	})
}

// sessionQuoteFor builds the terms this node will honour.
//
// Everything comes from the node's own config — never from the renter's
// request — which is the same rule s.requirements follows for x402. It is what
// makes verification meaningful: the node compares the chain against what IT
// said, not against what the renter claims it said.
func (s *Server) sessionQuoteFor(sessionID string) (sessionQuote, error) {
	price, err := s.sessionPricePerSecond()
	if err != nil {
		return sessionQuote{}, err
	}
	leases := s.cfg.Leases
	return sessionQuote{
		SessionID:              sessionID,
		EscrowContract:         s.escrow.Contract(),
		ProviderAddress:        s.providerAddress,
		PriceTinybarsPerSecond: price.String(),
		MinSeconds:             leases.MinMinutes * 60,
		MaxSeconds:             leases.MaxMinutes * 60,
		MaxTotalSeconds:        leases.MaxTotalMinutes * 60,
		Network:                s.cfg.Network,
	}, nil
}

// sessionPricePerSecond is the rate the contract settles at.
//
// Derived from the per-minute lease price unless a provider set a per-second
// figure explicitly. Converted in exactly one place: the contract multiplies
// this by elapsed seconds, so two conversions that disagreed would mean the
// node quoting one rate and being paid another.
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

// sessionState renders the read-only view, preferring the chain's numbers where
// they are available — the contract is the authority on what was paid for.
func (s *Server) sessionState(lease *runner.Lease, onchain *escrow.Session) map[string]any {
	base := lease.State()

	settleState := string(lease.SettleState())
	if onchain != nil && onchain.Settled {
		settleState = string(runner.SettleDone)
	}

	paidSeconds := base.PaidMinutes * 60
	expiresAt := base.ExpiresAt
	if onchain != nil {
		paidSeconds = int(onchain.Duration.Int64())
		expiresAt = time.Unix(onchain.ExpiresAt(), 0).UTC().Format(time.RFC3339)
	}

	return map[string]any{
		"session_id":        lease.SessionID(),
		"lease_id":          base.LeaseID,
		"status":            base.Status,
		"created_at":        base.CreatedAt,
		"expires_at":        expiresAt,
		"seconds_remaining": base.SecondsRemaining,
		"paid_seconds":      paidSeconds,
		"gpu":               base.GPU,
		"settle_state":      settleState,
		"error":             base.Error,
	}
}

// sessionsEnabled answers 404 on a node that does not sell escrow sessions.
//
// 404 rather than 403, for the same reason /v1/leases does: a node that never
// opted in should be indistinguishable from one running a version from before
// sessions existed.
func (s *Server) sessionsEnabled(w http.ResponseWriter) bool {
	if s.escrow != nil && s.runner.LeasesEnabled() && s.ca != nil && s.tunnel != nil {
		return true
	}
	writeError(w, http.StatusNotFound, "this node does not offer escrow-backed sessions")
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

// newSessionID mints the contract's key for a session.
//
// Minted by the node, not the renter, so a renter cannot present a deposit they
// made against terms of their own choosing. 32 random bytes: the contract
// refuses to reopen an existing id, so a collision would be a denial of service
// rather than a theft, and 256 bits makes it impossible either way.
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

func mustBig(s string) *big.Int {
	value, ok := new(big.Int).SetString(s, 10)
	if !ok {
		return big.NewInt(0)
	}
	return value
}
