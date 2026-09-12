package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"time"

	"github.com/YashIIT0909/ClearGate/agent/internal/hcs"
	"github.com/YashIIT0909/ClearGate/agent/internal/receipts"
	"github.com/YashIIT0909/ClearGate/agent/internal/runner"
	"github.com/YashIIT0909/ClearGate/agent/internal/tunnel"
	"github.com/YashIIT0909/ClearGate/agent/internal/x402"
)

// maxLeaseSpecBytes bounds the request body. A lease spec is a number, a public
// key and a flag.
const maxLeaseSpecBytes = 1 << 16

// leaseResponse is the 200 body of a paid POST /v1/leases or /extend.
//
// Everything a renter needs to connect is here and returned exactly once: the
// certificate is not stored anywhere the renter can fetch it again, and neither
// is the Jupyter token.
type leaseResponse struct {
	LeaseID   string `json:"lease_id"`
	Token     string `json:"token,omitempty"`
	Status    string `json:"status"`
	ExpiresAt string `json:"expires_at"`

	// Certificate is the renter's own public key, signed by this node's CA,
	// scoped to this lease and valid until ExpiresAt. Their private key never
	// left their machine and this node never saw it.
	Certificate string `json:"certificate"`
	// SSHUser and SSHHost are what to connect to. SSHHost is empty when the
	// node's tunnel mode cannot carry SSH — a quick tunnel is HTTP only.
	SSHUser string `json:"ssh_user"`
	SSHHost string `json:"ssh_host,omitempty"`
	// SSHPrincipal is the certificate principal, which is the lease id. Worth
	// returning so a renter can see for themselves what their cert authorizes.
	SSHPrincipal string `json:"ssh_principal"`

	JupyterURL   string `json:"jupyter_url"`
	JupyterToken string `json:"jupyter_token"`
	TunnelMode   string `json:"tunnel_mode"`

	Transaction    string `json:"transaction"`
	Payer          string `json:"payer"`
	AmountTinybars string `json:"amount_tinybars"`
	Minutes        int    `json:"minutes"`
}

// leaseSSHUser is who a renter logs in as inside the container.
//
// It is root, and that is deliberate: a rented dev box where `apt-get` and
// `pip install` do not work is not a usable one. Root here is root in a
// container that has had every capability dropped but the handful sshd needs,
// cannot gain privileges, and — on a provider who followed the setup guidance —
// is remapped to an unprivileged host user by the daemon's user namespace.
const leaseSSHUser = "root"

// handleCreateLease sells timed interactive access.
//
// The order mirrors handleCreateJob, with one addition that matters
// (implementation.md §5): a reachability check between provisioning and
// settling, so a renter is never charged for a lease that never came up.
//
//  1. validate the spec                  -> 400, and costs nothing
//  2. no payment header                  -> 402 challenge, priced per minute
//  3. payment header present             -> /verify
//  4. verified                           -> start the container, sign the cert
//  5. container up                       -> point the tunnel at it
//  6. tunnel up                          -> confirm it is actually reachable
//  7. reachable                          -> /settle
//  8. settled                            -> receipt, and the connection details
//
// A failure anywhere before step 7 tears the lease down and settles nothing.
// A provider who goes offline *after* step 7 is a different problem, handled by
// the freeze-and-reap loop rather than by refusing payment.
func (s *Server) handleCreateLease(w http.ResponseWriter, r *http.Request) {
	if !s.leasesEnabled(w) {
		return
	}

	spec, err := decodeLeaseSpec(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	validateCtx, cancelValidate := context.WithTimeout(r.Context(), 20*time.Second)
	err = s.runner.ValidateLeaseSpec(validateCtx, spec)
	cancelValidate()
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if s.paused.Load() {
		w.Header().Set("Retry-After", "60")
		writeError(w, http.StatusServiceUnavailable,
			"this node is not accepting new leases right now; nothing was charged")
		return
	}

	amount, err := s.leasePrice(spec.Minutes)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	payload, requirements, ok := s.collectPayment(w, r, amount, s.leaseDescription(spec.Minutes))
	if !ok {
		return
	}

	// Verified. Provision, prove it works, and only then take the money.
	leaseID := newLeaseID()
	token := newAccessToken()

	provisionCtx, cancelProvision := context.WithTimeout(context.WithoutCancel(r.Context()), 3*time.Minute)
	defer cancelProvision()

	lease, err := s.runner.StartLease(provisionCtx, leaseID, token, s.ca.PublicKey(), spec)
	if err != nil {
		// Nothing settled, so the renter has not been charged.
		s.log.Error("lease failed to start", "lease", leaseID, "error", err)
		writeError(w, http.StatusInternalServerError, "could not start the lease: "+err.Error())
		return
	}

	certificate, endpoints, err := s.publishLease(provisionCtx, lease)
	if err != nil {
		s.log.Error("lease never became reachable; nothing was charged", "lease", leaseID, "error", err)
		s.runner.StopLease(provisionCtx, lease, runner.LeaseFailed)
		s.tunnel.Clear(provisionCtx)
		writeError(w, http.StatusBadGateway,
			"the lease came up but could not be reached from the internet, so nothing was charged: "+err.Error())
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
		s.log.Error("settlement failed after the lease started; tearing it down",
			"lease", leaseID, "error", err, "reason", reasonOf(settlement))
		s.runner.StopLease(settleCtx, lease, runner.LeaseFailed)
		s.tunnel.Clear(settleCtx)
		s.respondWithChallengeFor(w, r, settlementFailureMessage(settlement, err), amount, s.leaseDescription(spec.Minutes))
		return
	}

	s.recordLeasePayment(leaseID, settlement, requirements)

	if err := x402.WriteSettlement(w, settlement); err != nil {
		s.log.Error("could not attach settlement header", "error", err)
	}
	writeJSON(w, http.StatusOK, leaseResponse{
		LeaseID:        leaseID,
		Token:          token,
		Status:         string(lease.Status()),
		ExpiresAt:      lease.ExpiresAt().UTC().Format(time.RFC3339),
		Certificate:    certificate,
		SSHUser:        leaseSSHUser,
		SSHHost:        endpoints.SSHHost,
		SSHPrincipal:   leaseID,
		JupyterURL:     endpoints.JupyterURL,
		JupyterToken:   lease.JupyterToken(),
		TunnelMode:     endpoints.Mode,
		Transaction:    settlement.Transaction,
		Payer:          settlement.Payer,
		AmountTinybars: requirements.Amount,
		Minutes:        spec.Minutes,
	})
}

// handleExtendLease buys another slice of time on a live lease.
//
// This is the metered half of ClearGate: a renter pays for what they actually
// use, in slices, and stops paying by simply not extending. Each extension
// re-signs a fresh certificate with a later expiry rather than mutating the old
// one, so a certificate is never valid for longer than the time behind it.
func (s *Server) handleExtendLease(w http.ResponseWriter, r *http.Request) {
	if !s.leasesEnabled(w) {
		return
	}

	lease, ok := s.authorizeLease(w, r)
	if !ok {
		return
	}

	spec, err := decodeLeaseSpec(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	// An extension re-signs whatever key the renter presents now, which may be
	// a fresh one; the public key is required here for the same reason it is on
	// create, and validated the same way.
	if err := s.runner.ValidateLeaseExtension(lease, spec); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	amount, err := s.leasePrice(spec.Minutes)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	payload, requirements, ok := s.collectPayment(w, r, amount, s.leaseDescription(spec.Minutes))
	if !ok {
		return
	}

	extendCtx, cancelExtend := context.WithTimeout(context.WithoutCancel(r.Context()), 60*time.Second)
	defer cancelExtend()

	// The extension is applied before it settles, for the same reason a job
	// starts before it settles: the renter's signed payload expires, and the
	// time they are buying has to be theirs first. Unlike a job, this is
	// trivially reversible — so a settlement failure reverses it rather than
	// leaving the renter with time they did not pay for.
	previous, err := s.runner.ExtendLease(extendCtx, lease, spec)
	if err != nil {
		// Nothing settled: the renter keeps their money and their old expiry.
		writeError(w, http.StatusConflict, err.Error())
		return
	}

	certificate, err := s.ca.Sign(extendCtx, lease.ID, spec.PublicKey, lease.ExpiresAt())
	if err != nil {
		s.log.Error("could not re-sign the extended lease's certificate", "lease", lease.ID, "error", err)
		s.runner.RevertExtension(extendCtx, lease, previous)
		writeError(w, http.StatusInternalServerError, "could not issue a certificate for the extended lease; nothing was charged")
		return
	}

	settlement, err := s.fac.Settle(extendCtx, x402.SettleRequest{
		X402Version:         x402.Version,
		PaymentPayload:      *payload,
		PaymentRequirements: *requirements,
	})
	if err != nil || !settlement.Success {
		s.emit(runner.Event{Kind: runner.EventSettleFailed, LeaseID: lease.ID, Detail: reasonOf(settlement)})
		// The lease is not torn down, unlike a failed first settlement — the
		// renter already paid for the slice they are inside, and that time is
		// still theirs. Only the slice that did not settle is taken back, which
		// puts them exactly where they were a moment ago. If they never manage
		// to pay, the freeze-and-reap loop takes it from there.
		s.log.Error("extension settlement failed; reverting it", "lease", lease.ID, "reason", reasonOf(settlement))
		s.runner.RevertExtension(extendCtx, lease, previous)
		s.respondWithChallengeFor(w, r, settlementFailureMessage(settlement, err), amount, s.leaseDescription(spec.Minutes))
		return
	}

	s.recordLeasePayment(lease.ID, settlement, requirements)

	endpoints := s.tunnel.Endpoints()
	if err := x402.WriteSettlement(w, settlement); err != nil {
		s.log.Error("could not attach settlement header", "error", err)
	}
	writeJSON(w, http.StatusOK, leaseResponse{
		LeaseID:        lease.ID,
		Status:         string(lease.Status()),
		ExpiresAt:      lease.ExpiresAt().UTC().Format(time.RFC3339),
		Certificate:    certificate,
		SSHUser:        leaseSSHUser,
		SSHHost:        endpoints.SSHHost,
		SSHPrincipal:   lease.ID,
		JupyterURL:     endpoints.JupyterURL,
		JupyterToken:   lease.JupyterToken(),
		TunnelMode:     endpoints.Mode,
		Transaction:    settlement.Transaction,
		Payer:          settlement.Payer,
		AmountTinybars: requirements.Amount,
		Minutes:        spec.Minutes,
	})
}

// handleLeaseState reports status and time remaining. Lease-token gated.
//
// The CLI's auto-extend loop polls this, so it is free: charging for the check
// that decides whether to pay would be absurd.
func (s *Server) handleLeaseState(w http.ResponseWriter, r *http.Request) {
	if !s.leasesEnabled(w) {
		return
	}
	lease, ok := s.authorizeLease(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, lease.State())
}

// handleLeaseStop ends a lease early. Lease-token gated.
//
// Time already bought is not refunded — forward payment is what removes the
// need for escrow — but stopping ends the meter, which is the point: a renter
// who is finished stops buying slices, and the provider's node is free for
// someone else within seconds rather than at the end of a slice.
func (s *Server) handleLeaseStop(w http.ResponseWriter, r *http.Request) {
	if !s.leasesEnabled(w) {
		return
	}
	lease, ok := s.authorizeLease(w, r)
	if !ok {
		return
	}

	stopCtx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 2*time.Minute)
	defer cancel()

	s.runner.StopLease(stopCtx, lease, runner.LeaseStopped)
	s.tunnel.Clear(stopCtx)
	writeJSON(w, http.StatusOK, lease.State())
}

// publishLease signs the renter's certificate, points the tunnel at the
// container, and proves the result is reachable from outside.
//
// The order is deliberate: the certificate is cheap and local, the tunnel is
// the part that can fail, and the reachability check is what stands between a
// renter and being charged for a lease they cannot connect to.
func (s *Server) publishLease(ctx context.Context, lease *runner.Lease) (string, tunnel.Endpoints, error) {
	certificate, err := s.ca.Sign(ctx, lease.ID, lease.PublicKey(), lease.ExpiresAt())
	if err != nil {
		return "", tunnel.Endpoints{}, fmt.Errorf("sign the lease certificate: %w", err)
	}

	endpoints, err := s.tunnel.PointAt(ctx, tunnel.Target{
		LeaseID:     lease.ID,
		SSHAddr:     lease.SSHAddr(),
		JupyterAddr: lease.JupyterAddr(),
	})
	if err != nil {
		return "", tunnel.Endpoints{}, fmt.Errorf("publish the lease: %w", err)
	}

	if err := s.tunnel.Reachable(ctx, endpoints); err != nil {
		return "", tunnel.Endpoints{}, err
	}
	return certificate, endpoints, nil
}

// reapLeases is the background loop that turns unpaid time into a frozen
// container and then into a reclaimed machine.
//
// Two missed extensions freeze; a grace period after that reaps. Nothing here
// takes money or gives it back — it only decides what happens to a container
// whose paid time has run out (implementation.md §4).
func (s *Server) reapLeases(ctx context.Context) {
	ticker := time.NewTicker(leaseSweepInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.sweepLeases(ctx)
		}
	}
}

// leaseSweepInterval is how often unpaid leases are checked. Fine enough that a
// renter is frozen within a few seconds of their grace running out, coarse
// enough to be free.
const leaseSweepInterval = 15 * time.Second

func (s *Server) sweepLeases(ctx context.Context) {
	lease, ok := s.runner.ActiveLease()
	if !ok {
		return
	}
	if lease.Status().IsTerminal() {
		// A session that ended some other way may still have its deposit in the
		// contract. Same loop, because it is the same event — the session is
		// over — and a second scheduler for it would be one more thing to keep
		// in agreement with this one.
		s.settleSession(ctx, lease)
		return
	}

	leases := s.cfg.Leases
	switch lease.Status() {
	case runner.LeaseActive:
		if lease.OverdueBy() < s.freezeTolerance(lease.SessionID()) {
			return
		}
		if err := s.runner.PauseLease(ctx, lease); err != nil {
			s.log.Error("could not freeze an unpaid lease", "lease", lease.ID, "error", err)
		}

	case runner.LeasePaused:
		if lease.PausedFor() < time.Duration(leases.GraceMinutes)*time.Minute {
			return
		}
		s.log.Info("reaping a lease whose grace period ran out", "lease", lease.ID)
		s.runner.StopLease(ctx, lease, runner.LeaseExpired)
		s.tunnel.Clear(ctx)
		// Settled with the lease we already hold, NOT by looking it up again:
		// StopLease releases the node's single lease slot, so by this point
		// ActiveLease no longer returns it and a re-lookup would silently find
		// nothing and settle nothing.
		//
		// An expired session's deposit is by now entirely the provider's —
		// elapsed has reached the paid duration, so there is nothing to refund —
		// but it stays in the contract until somebody calls settle.
		s.settleSession(ctx, lease)
	}
}

// freezeTolerance is how far past expiry a container runs before it is frozen.
//
// Small and fixed, for both payment models, and the reason is the same in each:
// the minutes a renter bought are the minutes they get. The tolerance exists
// only to absorb a payment that is already in flight — a wallet prompt being
// approved, or a contract top-up that consensus has accepted but the mirror
// node has not yet served — never to hand out free time.
//
//   - A direct-paid lease is overdue the moment expires_at passes. It used to
//     be given missed_extensions x the slice it bought, which with the shipped
//     default meant a 60-minute lease held the machine for 180 minutes before
//     it was even frozen. That is not a tolerance, it is three times the
//     product sold, and it read to a provider as their node ignoring its own
//     expiry.
//   - An escrow session is overdue the moment the clock passes what the
//     CONTRACT says was paid for. There is no slice to be late on, and running
//     past that point is time the contract will never pay the provider for —
//     `elapsed` is capped at the paid duration.
//
// Freezing rather than killing is unchanged and still deliberate: the container
// is paused, so a renter who extends gets their work back exactly as it was.
// leases.grace_minutes is how long that frozen state survives before the
// machine is reclaimed for good.
// Takes the session id rather than the lease because that is all it reads, and
// a pure function of configuration is one a test can pin without standing up a
// container.
func (s *Server) freezeTolerance(sessionID string) time.Duration {
	if sessionID != "" {
		return sessionFreezeGrace
	}
	return time.Duration(s.cfg.Leases.OverrunSeconds) * time.Second
}

// sessionFreezeGrace covers mirror-node lag on a top-up: the renter's money is
// already on consensus, and freezing them for the seconds it takes the node to
// see it would be punishing them for our own read latency.
const sessionFreezeGrace = 30 * time.Second

// leasePrice multiplies the per-minute price by the minutes bought.
//
// math/big, not int64 and never a float: tinybar amounts are strings end to end
// in ClearGate, and a price that overflows is a bug that silently undercharges.
func (s *Server) leasePrice(minutes int) (string, error) {
	perMinute, ok := new(big.Int).SetString(s.cfg.Leases.PriceTinybarsPerMinute, 10)
	if !ok {
		return "", errors.New("this node's lease price is misconfigured")
	}
	total := new(big.Int).Mul(perMinute, big.NewInt(int64(minutes)))
	return total.String(), nil
}

func (s *Server) leaseDescription(minutes int) string {
	return fmt.Sprintf("%d minutes of interactive GPU time on ClearGate node %s", minutes, s.cfg.NodeID)
}

// recordLeasePayment writes the settlement to the node's append-only log.
//
// A provider must be able to audit lease earnings the same way they audit job
// earnings, without trusting our website. Extensions land here too, so the log
// shows a metered session as the sequence of slices it actually was.
func (s *Server) recordLeasePayment(leaseID string, settlement *x402.SettleResponse, requirements *x402.PaymentRequirements) {
	s.emit(runner.Event{
		Kind:        runner.EventSettled,
		LeaseID:     leaseID,
		Payer:       settlement.Payer,
		Transaction: settlement.Transaction,
		Tinybars:    requirements.Amount,
	})

	receipt := receipts.Receipt{
		JobID:          leaseID,
		Transaction:    settlement.Transaction,
		Payer:          settlement.Payer,
		PayTo:          requirements.PayTo,
		AmountTinybars: requirements.Amount,
		Asset:          requirements.Asset,
		Network:        requirements.Network,
	}
	if err := s.receipts.Append(receipt); err != nil {
		s.log.Error("could not write lease receipt",
			"lease", leaseID, "transaction", settlement.Transaction, "error", err)
	}
	// Extensions land here too, so the audit topic shows a metered lease as the
	// sequence of slices it actually was — same as the local log.
	s.publishAudit(hcs.AuditMessage{
		Kind:           hcs.KindLease,
		JobID:          leaseID,
		Transaction:    settlement.Transaction,
		Payer:          settlement.Payer,
		PayTo:          requirements.PayTo,
		AmountTinybars: requirements.Amount,
		Asset:          requirements.Asset,
		Network:        requirements.Network,
	})

	s.log.Info("lease paid",
		"lease", leaseID,
		"payer", settlement.Payer,
		"amount_tinybars", requirements.Amount,
		"transaction", settlement.Transaction,
	)
}

// leasesEnabled answers 404 when this node does not sell leases.
//
// 404 rather than 403: a node that never opted in should look, to a renter,
// exactly like a node running a version from before leases existed.
func (s *Server) leasesEnabled(w http.ResponseWriter) bool {
	if s.runner.LeasesEnabled() && s.ca != nil && s.tunnel != nil {
		return true
	}
	writeError(w, http.StatusNotFound, "this node does not offer interactive leases")
	return false
}

// authorizeLease resolves a lease and checks the caller's token. An unknown
// lease and a wrong token both answer 404, for the same reason jobs do: whether
// a lease exists is not something an unauthorized caller gets to learn.
func (s *Server) authorizeLease(w http.ResponseWriter, r *http.Request) (*runner.Lease, bool) {
	id := r.PathValue("id")
	lease, found := s.runner.GetLease(id)
	if !found || !lease.AuthorizedBy(bearerToken(r)) {
		writeError(w, http.StatusNotFound, "no such lease, or the token does not authorize it")
		return nil, false
	}
	return lease, true
}

func decodeLeaseSpec(r *http.Request) (runner.LeaseSpec, error) {
	var spec runner.LeaseSpec
	decoder := json.NewDecoder(io.LimitReader(r.Body, maxLeaseSpecBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&spec); err != nil {
		return runner.LeaseSpec{}, fmt.Errorf("invalid lease spec: %w", err)
	}
	return spec, nil
}
