// Command cleargate-node is the ClearGate provider daemon: an x402 resource
// server that sells GPU time and runs containers for whoever pays.
//
// It never holds a private key and never links a Hedera SDK. The merchant side
// of x402 needs only JSON and HTTP calls to a facilitator — see CLAUDE.md.
package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// version is set at build time via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	root := &cobra.Command{
		Use:           "cleargate-node",
		Short:         "Rent your idle GPU out over x402 on Hedera",
		SilenceUsage:  true,
		SilenceErrors: true,
		Version:       version,
	}

	root.PersistentFlags().StringP("config", "c", "config.yaml", "path to config.yaml")

	root.AddCommand(newServeCommand())
	root.AddCommand(newTUICommand())
	root.AddCommand(newSetupCommand())
	root.AddCommand(newEarningsCommand())
	root.AddCommand(newRegisterCommand())

	if err := root.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}
