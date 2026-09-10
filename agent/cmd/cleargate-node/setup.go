package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
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
			path, _ := cmd.Flags().GetString("config")
			payTo, _ := cmd.Flags().GetString("pay-to")
			price, _ := cmd.Flags().GetString("price-tinybars")
			gpu, _ := cmd.Flags().GetBool("gpu")
			force, _ := cmd.Flags().GetBool("force")
			return setup(cmd.Context(), path, payTo, price, gpu, force)
		},
	}
	cmd.Flags().String("pay-to", "", "Hedera account that receives payment, e.g. 0.0.1234")
	cmd.Flags().String("price-tinybars", "", "flat price per job in tinybars (100000 = 0.001 HBAR)")
	cmd.Flags().Bool("gpu", false, "offer GPU passthrough (requires the NVIDIA Container Toolkit)")
	cmd.Flags().Bool("force", false, "overwrite an existing config")
	return cmd
}

func setup(ctx context.Context, path, payTo, price string, gpu, force bool) error {
	if _, err := os.Stat(path); err == nil && !force {
		return fmt.Errorf("%s already exists; pass --force to overwrite it", path)
	}

	cfg := config.Default()
	cfg.GPUEnabled = gpu

	reader := bufio.NewReader(os.Stdin)

	if payTo == "" {
		payTo = prompt(reader, "Hedera account to be paid into (e.g. 0.0.1234): ")
	}
	cfg.PayTo = strings.TrimSpace(payTo)

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

	if err := config.Save(path, cfg); err != nil {
		return err
	}

	fmt.Printf("\nwrote %s\n", path)
	fmt.Printf("node id      %s\n", cfg.NodeID)
	fmt.Printf("paid into    %s\n", cfg.PayTo)
	fmt.Printf("price        %s tinybars per job\n", cfg.PriceTinybars)
	fmt.Printf("\nstart it with:  cleargate-node serve --config %s\n", path)
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
