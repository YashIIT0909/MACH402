package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/YashIIT0909/MACH402/agent/internal/config"
	"github.com/YashIIT0909/MACH402/agent/internal/httpapi"
	"github.com/YashIIT0909/MACH402/agent/internal/registry"
	"github.com/YashIIT0909/MACH402/agent/internal/sshca"
	"github.com/YashIIT0909/MACH402/agent/internal/tunnel"
)

func newServeCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Serve the node API and start selling metered sessions",
		RunE: func(cmd *cobra.Command, _ []string) error {
			path, _ := cmd.Flags().GetString("config")
			return serve(cmd.Context(), path)
		},
	}
	return cmd
}

func serve(parent context.Context, configPath string) error {
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	ctx, stop := signal.NotifyContext(parent, os.Interrupt, syscall.SIGTERM)
	defer stop()

	n, err := buildNode(ctx, configPath, log)
	if err != nil {
		return err
	}
	defer n.stop()

	return n.server.Listen(ctx)
}

// startLeasing prepares the CA and the quick tunnel that publishes each lease.
//
// Nothing here involves a Cloudflare account: a quick tunnel needs only the
// cloudflared binary, and it is started per lease, so there is nothing to bring
// up until a renter pays.
func startLeasing(ctx context.Context, cfg *config.Config, configPath string, log *slog.Logger) (*leasing, error) {
	if !cfg.Leases.Enabled {
		return nil, nil
	}

	ca, err := sshca.Ensure(ctx, cfg.Leases.CAKeyPath, "cleargate-"+cfg.NodeID)
	if err != nil {
		return nil, err
	}
	log.Info("lease certificate authority ready", "path", cfg.Leases.CAKeyPath)

	return &leasing{ca: ca, tunnel: tunnel.New(cfg.Leases.Tunnel, log)}, nil
}

// announce starts publishing heartbeats to the registry, if this node is
// configured to be listed, and returns the function that stops them plus a way
// to read how those beats are going.
//
// Failures are logged and otherwise ignored: discovery is a convenience, and a
// registry that is down must never keep a paid node from working. The returned
// stop function waits for the node to withdraw its listing, so a provider who
// presses ctrl-c is off the website by the time their shell prompt returns.
//
// The status function exists because "logged and otherwise ignored" is the
// right behaviour for the daemon and the wrong one for a provider watching the
// dashboard: being invisible on the website is exactly the failure they would
// want to see, and it is silent everywhere else.
func announce(parent context.Context, cfg config.Config, server *httpapi.Server, log *slog.Logger) (func(), func() registry.Status) {
	if cfg.RegistryURL == "" {
		log.Info("not listed on a registry; renters can still pay this node directly")
		unlisted := registry.Status{}
		return func() {}, func() registry.Status { return unlisted }
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
	}, announcer.Status
}
