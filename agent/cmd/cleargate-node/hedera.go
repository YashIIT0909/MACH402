package main

import (
	"context"
	"errors"
	"log/slog"

	"github.com/YashIIT0909/ClearGate/agent/internal/config"
	"github.com/YashIIT0909/ClearGate/agent/internal/hcs"
	"github.com/YashIIT0909/ClearGate/agent/internal/hedera"
	"github.com/YashIIT0909/ClearGate/agent/internal/httpapi"
)

// newSidecar builds the handle to the `cleargate-hedera` child process.
//
// Returns nil when the provider never opted in, which is the common case and
// not an error: a node with no Hedera block sells jobs and direct-paid leases
// exactly as it always did.
func newSidecar(cfg config.Config) *hedera.Sidecar {
	if !cfg.Hedera.Enabled {
		return nil
	}
	return hedera.New(cfg.Hedera.Sidecar, cfg.Hedera.OperatorKeyPath, cfg.Network, cfg.Hedera.MirrorURL)
}

// enableHedera wires up the audit trail and metered sessions.
//
// Less independent than it used to be: a provider may publish an audit trail
// without selling sessions, but a session node must publish one, because the
// running refund-owed trail is the only thing that makes prepaying a stranger
// checkable. config.validate refuses the other combination at load.
func enableHedera(_ context.Context, cfg config.Config, server *httpapi.Server, log *slog.Logger) error {
	sidecar := newSidecar(cfg)
	if sidecar == nil {
		return nil
	}

	// Checked here rather than at the first settlement. "cleargate-hedera is
	// not installed" takes a minute to fix and is a terrible thing to discover
	// while a renter is waiting for a container.
	if err := sidecar.Available(); err != nil {
		return err
	}

	if cfg.HCS.Enabled {
		server.EnableAudit(hcs.New(sidecar, cfg.HCS.TopicID, cfg.NodeID, log))
		log.Info("publishing settlements to the audit topic", "topic", cfg.HCS.TopicID)
	}

	if !cfg.Leases.Enabled || cfg.Leases.PaymentMode != config.PaymentSession {
		return nil
	}
	if !cfg.HCS.Enabled {
		// Belt and braces with config.validate, which refuses this at load. A
		// session node that cannot publish its refund-owed trail is asking
		// renters to prepay against nothing but a promise.
		return errors.New("leases.payment_mode is session but hcs.enabled is off; " +
			"the refund-owed audit trail is what makes a metered session checkable")
	}

	// self_settle off means the node meters and publishes what it owes but
	// cannot return it itself. Weaker, and the honest default: paying refunds
	// means the operator account holds more than a fee float.
	var refunder *hedera.Sidecar
	if cfg.Leases.SelfSettle {
		refunder = sidecar
	} else {
		log.Warn("selling metered sessions with leases.self_settle off: " +
			"refunds will be published as owed and must be paid by hand")
	}

	server.EnableSessions(refunder)
	log.Info("selling metered, refundable sessions",
		"chunk_seconds", cfg.Leases.SessionChunkSeconds,
		"audit_topic", cfg.HCS.TopicID,
		"self_settle", cfg.Leases.SelfSettle,
	)
	return nil
}
