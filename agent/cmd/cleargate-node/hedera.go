package main

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/YashIIT0909/ClearGate/agent/internal/config"
	"github.com/YashIIT0909/ClearGate/agent/internal/escrow"
	"github.com/YashIIT0909/ClearGate/agent/internal/hcs"
	"github.com/YashIIT0909/ClearGate/agent/internal/hedera"
	"github.com/YashIIT0909/ClearGate/agent/internal/httpapi"
	"github.com/YashIIT0909/ClearGate/agent/internal/mirror"
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

// enableHedera wires up the audit trail and escrow sessions.
//
// Each half is independent: a provider may publish an audit trail without
// selling escrow sessions, or sell sessions without publishing anything. What
// they share is the sidecar and the one node-local operator key behind it.
func enableHedera(ctx context.Context, cfg config.Config, server *httpapi.Server, log *slog.Logger) error {
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

	if !cfg.Leases.Enabled || cfg.Leases.PaymentMode != config.PaymentEscrow {
		return nil
	}

	mirrorClient := mirror.New(cfg.Hedera.MirrorURL, 0)

	// Resolved once at startup, and deliberately fatal to escrow mode if it
	// fails. A pay_to that a contract cannot pay would let renters deposit into
	// sessions whose payout reverts, and finding that out here costs a log line
	// while finding it out later costs someone real money.
	providerAddress, err := escrow.ResolveProviderAddress(ctx, mirrorClient, cfg.PayTo)
	if err != nil {
		return fmt.Errorf("escrow mode is on but pay_to cannot be paid by a contract: %w", err)
	}

	// self_settle off means the node holds no ability to close a session, which
	// is the safer default: settle is permissionless, so the renter closes it
	// on exit and nothing is lost either way.
	var settler *hedera.Sidecar
	if cfg.Leases.SelfSettle {
		settler = sidecar
	}

	server.EnableEscrow(
		escrow.NewVerifier(mirrorClient, cfg.Leases.EscrowContractID),
		providerAddress,
		settler,
	)
	log.Info("selling escrow-backed sessions",
		"contract", cfg.Leases.EscrowContractID,
		"provider_address", providerAddress,
		"self_settle", cfg.Leases.SelfSettle,
	)
	return nil
}
