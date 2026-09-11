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
	"github.com/YashIIT0909/ClearGate/agent/internal/sshca"
	"github.com/YashIIT0909/ClearGate/agent/internal/tunnel"
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

	// Leasing is set up before the runner and the server so that a tunnel that
	// supplies this node's public URL has done so before anything reads it.
	leasing, err := startLeasing(ctx, &cfg, configPath, log)
	if err != nil {
		// Not fatal: a node whose tunnel will not come up should still sell
		// batch jobs, which is the mode that needs no inbound reachability at
		// all. It just does not sell leases (CLAUDE.md invariant 8).
		log.Error("leasing is configured but could not be started; this node will sell jobs only", "error", err)
		leasing = nil
	}
	if leasing != nil {
		defer leasing.tunnel.Stop()
	}

	run, err := runner.New(ctx, cfg, log)
	if err != nil {
		return err
	}

	log.Info("node ready",
		"node_id", cfg.NodeID,
		"pay_to", cfg.PayTo,
		"price_tinybars", cfg.PriceTinybars,
		"gpu", run.GPU().Available,
		"leases", leasing != nil,
	)

	server := httpapi.New(cfg, run, fac, log, version)
	if leasing != nil {
		server.EnableLeases(leasing.ca, leasing.tunnel)

		// The loop that freezes a lease whose paid time lapsed and reclaims the
		// machine once its grace period is gone.
		reaped := make(chan struct{})
		go func() {
			defer close(reaped)
			server.ReapLeases(ctx)
		}()
		defer func() { <-reaped }()
	}

	stopAnnouncing := announce(ctx, cfg, server, log)
	defer stopAnnouncing()

	return server.Listen(ctx)
}

// leasing is the pair of things a node needs before it can sell interactive
// access: its own certificate authority, and a way in through NAT.
type leasing struct {
	ca     *sshca.CA
	tunnel *tunnel.Manager
}

// startLeasing prepares the CA and the tunnel, and — in named mode — makes sure
// this node has a tunnel credential from the registry.
//
// Nothing here involves the provider having a Cloudflare account. The registry
// holds the platform's single Cloudflare API token and provisions a tunnel and
// its DNS routes on the node's behalf; what comes back authorizes running that
// one tunnel and nothing else.
func startLeasing(ctx context.Context, cfg *config.Config, configPath string, log *slog.Logger) (*leasing, error) {
	if !cfg.Leases.Enabled {
		return nil, nil
	}

	ca, err := sshca.Ensure(ctx, cfg.Leases.CAKeyPath, "cleargate-"+cfg.NodeID)
	if err != nil {
		return nil, err
	}
	log.Info("lease certificate authority ready", "path", cfg.Leases.CAKeyPath)

	// A named tunnel needs a credential, and the credential is cached in
	// config.yaml so this call happens once in a node's life rather than once
	// per start. That matters: it means a registry outage cannot stop an
	// established provider from selling leases.
	if cfg.Leases.Tunnel.Mode == config.TunnelNamed && cfg.Leases.Tunnel.Token == "" {
		creds, err := tunnel.Fetch(ctx, cfg.RegistryURL, cfg.NodeID, cfg.RegistryToken)
		if err != nil {
			return nil, err
		}
		cfg.Leases.Tunnel.Token = creds.Token
		cfg.Leases.Tunnel.SSHHostname = creds.SSHHostname
		cfg.Leases.Tunnel.JupyterHostname = creds.JupyterHostname
		cfg.Leases.Tunnel.APIHostname = creds.APIHostname

		if err := config.Save(configPath, *cfg); err != nil {
			// Losing the cache is survivable — the registry hands back the same
			// tunnel next time — but it is worth saying loudly.
			log.Warn("could not cache the tunnel credential; it will be fetched again next start", "error", err)
		}
		log.Info("tunnel provisioned",
			"ssh", creds.SSHHostname, "jupyter", creds.JupyterHostname, "api", creds.APIHostname)
	}

	tunnels := tunnel.New(cfg.Leases.Tunnel, cfg.ListenAddr, log)

	// A provider behind NAT usually cannot be reached for the x402 API either,
	// not just for SSH. When they asked for it, the tunnel's own API hostname
	// replaces the public_url they typed in — which has to happen before the
	// first heartbeat, or the registry publishes an address nobody can dial.
	if cfg.Leases.Tunnel.DerivePublicURL {
		if derived := tunnels.APIURL(); derived != "" {
			log.Info("public URL derived from this node's tunnel",
				"was", cfg.PublicURL, "now", derived)
			cfg.PublicURL = derived
		} else {
			log.Warn("leases.tunnel.derive_public_url is set but this node has no API hostname; keeping the configured public_url")
		}
	}

	if err := tunnels.Start(ctx); err != nil {
		return nil, err
	}
	return &leasing{ca: ca, tunnel: tunnels}, nil
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
