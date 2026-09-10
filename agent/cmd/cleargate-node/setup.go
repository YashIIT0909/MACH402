package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/YashIIT0909/ClearGate/agent/internal/config"
	"github.com/YashIIT0909/ClearGate/agent/internal/runner"
	"github.com/YashIIT0909/ClearGate/agent/internal/x402"
)

func newSetupCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "setup",
		Short: "Write a config.yaml and check this machine can run jobs",
		Long: "Prompts for the Hedera account that should receive payment, then " +
			"preflights Docker and the GPU so problems surface now rather than " +
			"during someone's paid job.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			opts := setupOptions{}
			opts.configPath, _ = cmd.Flags().GetString("config")
			opts.payTo, _ = cmd.Flags().GetString("pay-to")
			opts.price, _ = cmd.Flags().GetString("price-tinybars")
			opts.registryURL, _ = cmd.Flags().GetString("registry-url")
			opts.publicURL, _ = cmd.Flags().GetString("public-url")
			opts.gpu, _ = cmd.Flags().GetBool("gpu")
			opts.force, _ = cmd.Flags().GetBool("force")
			return setup(cmd.Context(), opts)
		},
	}
	cmd.Flags().String("pay-to", "", "Hedera account that receives payment, e.g. 0.0.1234")
	cmd.Flags().String("price-tinybars", "", "flat price per job in tinybars (100000 = 0.001 HBAR)")
	cmd.Flags().String("registry-url", "", "registry to list this node on; empty keeps it unlisted")
	cmd.Flags().String("public-url", "", "how renters reach this node, e.g. http://1.2.3.4:8402 (required with --registry-url)")
	cmd.Flags().Bool("gpu", false, "offer GPU passthrough (requires the NVIDIA Container Toolkit)")
	cmd.Flags().Bool("force", false, "overwrite an existing config")
	return cmd
}

// setupOptions is what the install script passes in non-interactively, and what
// the prompts fill in when a provider runs setup by hand.
type setupOptions struct {
	configPath  string
	payTo       string
	price       string
	registryURL string
	publicURL   string
	gpu         bool
	force       bool
}

func setup(ctx context.Context, opts setupOptions) error {
	path := opts.configPath
	if _, err := os.Stat(path); err == nil && !opts.force {
		return fmt.Errorf("%s already exists; pass --force to overwrite it", path)
	}

	cfg := config.Default()
	cfg.GPUEnabled = opts.gpu

	reader := bufio.NewReader(os.Stdin)

	payTo := opts.payTo
	if payTo == "" {
		payTo = prompt(reader, "Hedera account to be paid into (e.g. 0.0.1234): ")
	}
	cfg.PayTo = strings.TrimSpace(payTo)

	price := opts.price
	if price == "" {
		entered := prompt(reader, fmt.Sprintf("Price per job in tinybars [%s]: ", cfg.PriceTinybars))
		if entered != "" {
			price = entered
		}
	}
	if price != "" {
		if _, err := strconv.ParseUint(strings.TrimSpace(price), 10, 64); err != nil {
			return fmt.Errorf("price must be a whole number of tinybars: %w", err)
		}
		cfg.PriceTinybars = strings.TrimSpace(price)
	}

	cfg.NodeID = newNodeID()

	// Listing is opt-in. A node with no registry_url still sells jobs to any
	// renter who knows its URL; it just does not appear on the website.
	cfg.RegistryURL = strings.TrimRight(strings.TrimSpace(opts.registryURL), "/")
	cfg.PublicURL = strings.TrimRight(strings.TrimSpace(opts.publicURL), "/")
	if cfg.RegistryURL != "" {
		if cfg.PublicURL == "" {
			cfg.PublicURL = prompt(reader, "Public URL renters use to reach this node (e.g. http://1.2.3.4:8402): ")
			cfg.PublicURL = strings.TrimRight(strings.TrimSpace(cfg.PublicURL), "/")
		}
		// Proves to the registry that later heartbeats come from this same node,
		// so nobody else can repoint the listing at their own machine.
		cfg.RegistryToken = newRegistryToken()
	}

	if err := cfg.Validate(); err != nil {
		return err
	}

	fmt.Println("\npreflight")

	// A node that cannot reach the facilitator cannot be paid.
	fac := x402.NewFacilitator(cfg.FacilitatorURL, 20*time.Second)
	kind, err := fac.Kind(ctx, x402.SchemeExact, cfg.Network)
	if err != nil {
		return fmt.Errorf("  facilitator: %w", err)
	}
	feePayer, _ := kind.FeePayer()
	fmt.Printf("  facilitator  %s (fee payer %s)\n", cfg.FacilitatorURL, feePayer)

	// A node that cannot reach Docker cannot run anything.
	docker, err := runner.NewDocker(cfg.DockerHost)
	if err != nil {
		return fmt.Errorf("  docker: %w", err)
	}
	if err := docker.Ping(ctx); err != nil {
		return fmt.Errorf("  docker: %w\n  is the daemon running, and is your user in the docker group?", err)
	}
	info, err := docker.Info(ctx)
	if err != nil {
		return fmt.Errorf("  docker: %w", err)
	}
	fmt.Printf("  docker       %s\n", info.ServerVersion)

	detected := runner.DetectGPU(ctx, docker, cfg.GPUEnabled)
	if detected.Available {
		fmt.Printf("  gpu          %s (%d MB)\n", detected.Model, detected.VRAMMb)
	} else {
		fmt.Printf("  gpu          CPU-fallback mode — %s\n", detected.Reason)
	}

	// A registry that is unreachable is worth knowing about now, while the
	// provider is still watching, rather than as a node that quietly never
	// appears on the website. It is not fatal: the node sells jobs regardless.
	if cfg.RegistryURL != "" {
		if err := pingRegistry(ctx, cfg.RegistryURL); err != nil {
			fmt.Printf("  registry     unreachable — %v\n", err)
			fmt.Printf("               the node will keep retrying once it starts\n")
		} else {
			fmt.Printf("  registry     %s\n", cfg.RegistryURL)
		}
	}

	if err := config.Save(path, cfg); err != nil {
		return err
	}

	fmt.Printf("\nwrote %s\n", path)
	fmt.Printf("node id      %s\n", cfg.NodeID)
	fmt.Printf("paid into    %s\n", cfg.PayTo)
	fmt.Printf("price        %s tinybars per job\n", cfg.PriceTinybars)
	if cfg.RegistryURL != "" {
		fmt.Printf("listed on    %s as %s\n", cfg.RegistryURL, cfg.PublicURL)
	} else {
		fmt.Printf("listed on    nothing — this node is private\n")
	}
	fmt.Printf("\nstart it with:  cleargate-node serve --config %s\n", path)
	return nil
}

// pingRegistry checks the registry is up. It only ever reports discovery
// health: the registry never touches money (CLAUDE.md invariant 3).
func pingRegistry(ctx context.Context, baseURL string) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/health", nil)
	if err != nil {
		return err
	}
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("answered %s", response.Status)
	}
	return nil
}

func prompt(reader *bufio.Reader, question string) string {
	fmt.Print(question)
	line, err := reader.ReadString('\n')
	if err != nil {
		return ""
	}
	return strings.TrimSpace(line)
}

func newNodeID() string {
	var raw [8]byte
	_, _ = rand.Read(raw[:])
	return "node_" + hex.EncodeToString(raw[:])
}

// newRegistryToken mints this node's listing credential. It authorizes exactly
// one thing — updating this node's own row in the registry — and can never
// authorize a payment.
func newRegistryToken() string {
	var raw [32]byte
	_, _ = rand.Read(raw[:])
	return hex.EncodeToString(raw[:])
}
