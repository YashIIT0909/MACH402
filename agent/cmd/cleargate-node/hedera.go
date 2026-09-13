package main

import (
	"context"
	"errors"
	"log/slog"

	"github.com/YashIIT0909/MACH402/agent/internal/config"
	"github.com/YashIIT0909/MACH402/agent/internal/hcs"
	"github.com/YashIIT0909/MACH402/agent/internal/hedera"
	"github.com/YashIIT0909/MACH402/agent/internal/httpapi"
)

// newSidecar builds the handle to the `cleargate-hedera` child process.
//
// Returns nil when the Hedera block is off. config.validate refuses that on any
// node that sells sessions, so in practice this is a node selling nothing.
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

	if !cfg.Leases.Enabled {
		return nil
	}
	if !cfg.HCS.Enabled {
		// Belt and braces with config.validate, which refuses this at load. A
		// session node that cannot publish its refund-owed trail is asking
		// renters to prepay against nothing but a promise.
		return errors.New("leases.enabled is on but hcs.enabled is off; " +
			"the refund-owed audit trail is what makes a metered session checkable")
	}

	// Refunds are always paid by the node itself, from the operator account.
	// Leaving them to be paid by hand is not an option a provider is offered:
	// a renter who stops early is owed their credit back, not a promise of it.
	server.EnableSessions(sidecar)
	log.Info("selling metered, refundable sessions",
		"chunk_seconds", cfg.Leases.SessionChunkSeconds,
		"audit_topic", cfg.HCS.TopicID,
	)
	return nil
}
