package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"net/http"
	"os"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"

	"github.com/YashIIT0909/ClearGate/agent/internal/receipts"
	"github.com/YashIIT0909/ClearGate/agent/internal/tui"
)

func newTUICommand() *cobra.Command {
	return &cobra.Command{
		Use:   "tui",
		Short: "Run the node with a live dashboard",
		Long: "Serves exactly what `serve` serves, with a terminal dashboard on top: " +
			"payments as they settle, jobs as they run, and GPU utilisation.\n\n" +
			"`serve` remains the right command for a machine running under systemd. " +
			"This is for a provider watching their own box.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			path, _ := cmd.Flags().GetString("config")
			return runTUI(cmd.Context(), path)
		},
	}
}

func runTUI(parent context.Context, configPath string) error {
	// The dashboard owns the terminal, so the daemon's logs must not write to
	// it. They go to a file instead, which is also where a provider looks when
	// something went wrong while they were not watching.
	logFile, err := os.OpenFile("cleargate-node.log", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("open log file: %w", err)
	}
	defer logFile.Close()

	log := slog.New(slog.NewTextHandler(logFile, &slog.HandlerOptions{Level: slog.LevelInfo}))

	ctx, cancel := context.WithCancel(parent)
	defer cancel()

	// The same assembly `serve` uses, deliberately — including the leasing
	// wiring and the lease sweep. Building a second one here by hand is what
	// previously left the dashboard serving a node that answered 404 on
	// /v1/leases however the provider had configured it. See node.go.
	n, err := buildNode(ctx, configPath, log)
	if err != nil {
		return err
	}
	defer n.stop()

	cfg, run, server := n.cfg, n.runner, n.server

	// Seed the dashboard from the receipt log so a restart does not appear to
	// reset the provider's earnings.
	earned, settlements := priorEarnings(cfg.ReceiptsPath)

	model := tui.New(cfg, run, server, version, earned, settlements)
	program := tea.NewProgram(model, tea.WithAltScreen(), tea.WithContext(ctx))
	model.Attach(program)

	// The HTTP server runs alongside the dashboard. If it dies, the dashboard
	// must not sit there implying the node is still selling compute.
	serverErrors := make(chan error, 1)
	go func() {
		err := server.Listen(ctx)
		if err != nil && !errors.Is(err, http.ErrServerClosed) && !errors.Is(err, context.Canceled) {
			serverErrors <- err
			program.Quit()
			return
		}
		serverErrors <- nil
	}()

	if _, err := program.Run(); err != nil && !errors.Is(err, tea.ErrProgramKilled) {
		return err
	}

	cancel()
	select {
	case err := <-serverErrors:
		if err != nil {
			return fmt.Errorf("node stopped: %w", err)
		}
	case <-time.After(5 * time.Second):
	}
	return nil
}

// priorEarnings totals what this node has already been paid, so the dashboard
// opens with the truth rather than with zero.
func priorEarnings(path string) (int64, int) {
	all, err := receipts.Open(path).All()
	if err != nil {
		return 0, 0
	}
	total := new(big.Int)
	for _, receipt := range all {
		if amount, ok := new(big.Int).SetString(receipt.AmountTinybars, 10); ok {
			total.Add(total, amount)
		}
	}
	if !total.IsInt64() {
		return 0, len(all)
	}
	return total.Int64(), len(all)
}
