package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/YashIIT0909/ClearGate/agent/internal/config"
	"github.com/YashIIT0909/ClearGate/agent/internal/httpapi"
	"github.com/YashIIT0909/ClearGate/agent/internal/registry"
	"github.com/YashIIT0909/ClearGate/agent/internal/runner"
	"github.com/YashIIT0909/ClearGate/agent/internal/x402"
)

func newServeCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Serve the node API and start accepting paid jobs",
		RunE: func(cmd *cobra.Command, _ []string) error {
			path, _ := cmd.Flags().GetString("config")
			return serve(cmd.Context(), path)
		},
	}
	return cmd
}

func serve(parent context.Context, configPath string) error {
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(parent, os.Interrupt, syscall.SIGTERM)
	defer stop()

	fac := x402.NewFacilitator(cfg.FacilitatorURL, 30*time.Second)

	// Fail fast if the facilitator cannot price a job: a node that cannot build
	// a challenge cannot be paid, and it is better to know at startup.
	kind, err := fac.Kind(ctx, x402.SchemeExact, cfg.Network)
	if err != nil {
		return err
	}
	feePayer, _ := kind.FeePayer()
	log.Info("facilitator ready", "url", cfg.FacilitatorURL, "network", cfg.Network, "fee_payer", feePayer)

	run, err := runner.New(ctx, cfg, log)
	if err != nil {
		return err
	}

	log.Info("node ready",
		"node_id", cfg.NodeID,
		"pay_to", cfg.PayTo,
		"price_tinybars", cfg.PriceTinybars,
		"gpu", run.GPU().Available,
	)

	server := httpapi.New(cfg, run, fac, log, version)

	stopAnnouncing := announce(ctx, cfg, server, log)
	defer stopAnnouncing()

	return server.Listen(ctx)
}

// announce starts publishing heartbeats to the registry, if this node is
// configured to be listed, and returns the function that stops them.
//
// Failures are logged and otherwise ignored: discovery is a convenience, and a
// registry that is down must never keep a paid node from working. The returned
// stop function waits for the node to withdraw its listing, so a provider who
// presses ctrl-c is off the website by the time their shell prompt returns.
func announce(parent context.Context, cfg config.Config, server *httpapi.Server, log *slog.Logger) func() {
	if cfg.RegistryURL == "" {
		log.Info("not listed on a registry; renters can still pay this node directly")
		return func() {}
	}

	log.Info("announcing to registry", "registry", cfg.RegistryURL, "public_url", cfg.PublicURL)

	// Its own context, so stopping the announcer does not depend on whoever
	// cancels the parent — `serve` also returns on a listen error, and the
	// listing must come down then too.
	ctx, cancel := context.WithCancel(parent)
	announcer := registry.New(cfg.RegistryURL, cfg.NodeID, cfg.RegistryToken, server.Heartbeat, log)

	done := make(chan struct{})
	go func() {
		defer close(done)
		announcer.Run(ctx)
	}()

	return func() {
		cancel()
		<-done
	}
}
