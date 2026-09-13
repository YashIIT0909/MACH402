package main

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/YashIIT0909/ClearGate/agent/internal/config"
	"github.com/YashIIT0909/ClearGate/agent/internal/httpapi"
	"github.com/YashIIT0909/ClearGate/agent/internal/registry"
	"github.com/YashIIT0909/ClearGate/agent/internal/runner"
	"github.com/YashIIT0909/ClearGate/agent/internal/sshca"
	"github.com/YashIIT0909/ClearGate/agent/internal/tunnel"
	"github.com/YashIIT0909/ClearGate/agent/internal/x402"
)

// node is a fully wired node — everything `serve` and `tui` both need.
//
// This exists because they diverged once and it was not visible. `tui` built
// its own server without the leasing wiring, so a provider who had opted into
// leases, built the image and configured a price watched their dashboard report
// a healthy node while every `POST /v1/sessions` answered
// "this node does not offer interactive leases", and nothing froze or reaped a
// lease either because the sweep was never started.
//
// CLAUDE.md's claim about the dashboard is that it "is a view over the same
// runner and server `serve` uses. It owns no state of its own, so a provider
// never has to wonder whether it and the daemon disagree." Two call sites
// assembling the same node by hand is how that claim quietly stopped being
// true, so there is now one assembly and both commands use it.
type node struct {
	cfg     config.Config
	runner  *runner.Runner
	server  *httpapi.Server
	leasing *leasing

	// registryStatus reports how this node's listing is going. Always non-nil;
	// on an unlisted node it reports exactly that.
	registryStatus func() registry.Status

	// stop unwinds the background work this node started. Safe to call at any
	// point, including on a startup failure: it cancels the node's own context
	// first, so the lease sweep is never waited on while it still has a live
	// context to run under.
	stop func()
}

// buildNode does everything up to — but not including — serving.
//
// The caller runs `node.server.Listen(ctx)` itself, because that is the one
// thing the two commands genuinely do differently: `serve` blocks on it, and
// `tui` runs it alongside a dashboard that owns the terminal.
func buildNode(ctx context.Context, configPath string, log *slog.Logger) (*node, error) {
	cfg, err := config.Load(configPath)
	if err != nil {
		return nil, err
	}

	// The node's background work runs under its own context rather than the
	// caller's, so `stop` can end it deterministically. Without this, a failure
	// between here and Listen leaves the caller unwinding into a wait on a
	// sweep that has been given no reason to exit.
	ctx, cancelNode := context.WithCancel(ctx)

	fac := x402.NewFacilitator(cfg.FacilitatorURL, 30*time.Second)

	// Fail fast if the facilitator is unusable: a node that cannot build a
	// challenge cannot be paid, and it is better to know at startup.
	kind, err := fac.Kind(ctx, x402.SchemeExact, cfg.Network)
	if err != nil {
		cancelNode()
		return nil, fmt.Errorf("facilitator is not usable, so this node cannot be paid: %w", err)
	}
	feePayer, _ := kind.FeePayer()
	log.Info("facilitator ready",
		"url", cfg.FacilitatorURL, "network", cfg.Network, "fee_payer", feePayer)

	// Leasing is prepared before the runner and the server, which are handed it
	// below.
	leases, err := startLeasing(ctx, &cfg, configPath, log)
	if err != nil {
		// Fatal: sessions are all a node sells, so a node that cannot prepare
		// them — its SSH certificate authority, most likely — has nothing to
		// offer and should say so at startup rather than list itself.
		cancelNode()
		return nil, fmt.Errorf("could not prepare leasing: %w", err)
	}
	if leases == nil {
		log.Warn("leases.enabled is false, so this node sells nothing; re-run `cleargate-node setup`")
	}

	run, err := runner.New(ctx, cfg, log)
	if err != nil {
		cancelNode()
		return nil, err
	}

	log.Info("node ready",
		"node_id", cfg.NodeID,
		"pay_to", cfg.PayTo,
		"price_tinybars_per_minute", cfg.Leases.PriceTinybarsPerMinute,
		"gpu", run.GPU().Available,
		"leases", leases != nil,
	)

	server := httpapi.New(cfg, run, fac, log, version)

	// The signing sidecar and everything built on it. Failures here are logged
	// and survived rather than fatal: without the sidecar the /v1/sessions routes
	// stay closed, and the error says why.
	if err := enableHedera(ctx, cfg, server, log); err != nil {
		log.Error("Hedera features are configured but could not be started", "error", err)
	}

	reaped := make(chan struct{})
	if leases != nil {
		server.EnableLeases(leases.ca, leases.tunnel)

		// The loop that freezes a lease whose paid time lapsed and reclaims the
		// machine once its grace period is gone. Without it a lease would run
		// forever on one payment.
		go func() {
			defer close(reaped)
			server.ReapLeases(ctx)
		}()
	} else {
		close(reaped)
	}

	stopAnnouncing, registryStatus := announce(ctx, cfg, server, log)

	return &node{
		cfg:            cfg,
		runner:         run,
		server:         server,
		leasing:        leases,
		registryStatus: registryStatus,
		stop: func() {
			stopAnnouncing()
			cancelNode()

			// Bounded even so. The sweep is mid-reap at most for as long as
			// stopping a container takes, and a provider's shutdown should not
			// hang indefinitely on a Docker daemon that is not answering.
			select {
			case <-reaped:
			case <-time.After(10 * time.Second):
				log.Warn("the lease sweep did not stop in time; shutting down anyway")
			}

			if leases != nil {
				leases.tunnel.Stop()
			}
		},
	}, nil
}

// leasing is the pair of things a node needs before it can sell interactive
// access: its own certificate authority, and a way in through NAT.
type leasing struct {
	ca     *sshca.CA
	tunnel *tunnel.Manager
}
