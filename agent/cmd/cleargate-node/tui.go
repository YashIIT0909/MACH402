package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"

	"github.com/YashIIT0909/MACH402/agent/internal/receipts"
	"github.com/YashIIT0909/MACH402/agent/internal/tui"
)

func newTUICommand() *cobra.Command {
	return &cobra.Command{
		Use:   "tui",
		Short: "Run the node with a live dashboard",
		Long: "Serves exactly what `serve` serves, with a terminal dashboard on top: " +
			"payments as they settle, the session running now, and GPU utilisation.\n\n" +
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
	// /v1/sessions however the provider had configured it. See node.go.
	n, err := buildNode(ctx, configPath, log)
	if err != nil {
		return err
	}
	defer n.stop()

	// Seed the dashboard from the receipt log so a restart does not appear to
	// reset the provider's earnings, and so it can answer "what did I make
	// today" rather than only "what have I made since you started looking".
	history, err := receipts.Open(n.cfg.ReceiptsPath).All()
	if err != nil {
		log.Warn("could not read the receipt log; the dashboard will start from zero",
			"path", n.cfg.ReceiptsPath, "error", err)
	}

	server := n.server
	model := tui.New(tui.Options{
		Config:         n.cfg,
		Runner:         n.runner,
		Server:         server,
		Version:        version,
		Receipts:       history,
		RegistryStatus: n.registryStatus,
	})
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
