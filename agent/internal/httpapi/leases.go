package httpapi

import (
	"context"
	"fmt"
	"time"

	"github.com/YashIIT0909/MACH402/agent/internal/hcs"
	"github.com/YashIIT0909/MACH402/agent/internal/receipts"
	"github.com/YashIIT0909/MACH402/agent/internal/runner"
	"github.com/YashIIT0909/MACH402/agent/internal/tunnel"
	"github.com/YashIIT0909/MACH402/agent/internal/x402"
)

// The machinery every metered session runs on: the container behind it is a
// lease, published through the tunnel, frozen and reaped by the sweep below.
// There is no way to buy a lease on its own any more — interactive time is sold
// only as a metered session (sessions.go), so a renter who stops early always
// gets their unburned credit back.

// leaseSSHUser is who a renter logs in as inside the container.
//
// It is root, and that is deliberate: a rented dev box where `apt-get` and
// `pip install` do not work is not a usable one. Root here is root in a
// container that has had every capability dropped but the handful sshd needs,
// cannot gain privileges, and — on a provider who followed the setup guidance —
// is remapped to an unprivileged host user by the daemon's user namespace.
const leaseSSHUser = "root"

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
// A session whose credit has run out is frozen; a grace period after that
// reaps it. The loop does not take money itself — it runs the meter, and it
// settles a session that has ended, so the renter's refund goes out on the same
// sweep that notices (implementation.md §4).
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
		// A session that ended some other way may still owe its renter the
		// credit they did not burn. Same loop, because it is the same event —
		// the session is over — and a second scheduler for it would be one more
		// thing to keep in agreement with this one. It is also the retry for a
		// refund whose transfer failed: settleSession leaves those pending.
		s.settleSession(ctx, lease)
		return
	}

	// The meter, before the freeze decision rather than after it. Burning is
	// what moves expiry on a session, so a tick that runs afterwards would
	// always be judging the state of affairs one sweep ago.
	if lease.SessionID() != "" && lease.Status() == runner.LeaseActive {
		s.tickMeter(lease)
	}

	leases := s.cfg.Leases
	switch lease.Status() {
	case runner.LeaseActive:
		// A session paid for in full ends as soon as its credit is used: no
		// top-up is coming, so freezing it would only hold the machine for
		// nothing.
		if lease.FullyPaid() && lease.SecondsRemaining() == 0 {
			s.log.Info("session used the time it was bought for; ending it", "lease", lease.ID)
			s.runner.StopLease(ctx, lease, runner.LeaseExpired)
			s.tunnel.Clear(ctx)
			s.settleSession(ctx, lease)
			return
		}
		if lease.OverdueBy() < sessionFreezeGrace {
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
		// nothing and settle nothing — leaving a renter's refund unpaid.
		//
		// A session reaped this way has usually burned its credit to zero, so
		// there is nothing left to return. Usually is not always: a renter who
		// stopped paying with credit still on the clock is refunded here.
		s.settleSession(ctx, lease)
	}
}

// sessionFreezeGrace covers a top-up already in flight: the payment round trip
// through the facilitator takes a couple of seconds, and the meter ticks every
// fifteen, so freezing the instant credit hit zero would freeze renters whose
// money was already moving.
const sessionFreezeGrace = 30 * time.Second

// recordLeasePayment writes the settlement to the node's append-only log.
//
// A provider must be able to audit session earnings the same way they audit
// earnings, without trusting our website. Top-ups land here too, so the log
// shows a metered session as the sequence of chunks it actually was.
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
	// Top-ups land here too, so the audit topic shows a metered session as the
	// sequence of chunks it actually was — same as the local log.
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
