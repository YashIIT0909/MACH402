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
	return server.Listen(ctx)
}
