package main

import (
	"fmt"
	"math/big"
	"strings"

	"github.com/spf13/cobra"

	"github.com/YashIIT0909/ClearGate/agent/internal/config"
	"github.com/YashIIT0909/ClearGate/agent/internal/receipts"
)

func newEarningsCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "earnings",
		Short: "Show settled payments from this node's local receipt log",
		Long: "Reads the append-only receipt file the node writes on every " +
			"settlement, so earnings can be audited without trusting any website.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			path, _ := cmd.Flags().GetString("config")
			cfg, err := config.Load(path)
			if err != nil {
				return err
			}
			return showEarnings(receipts.Open(cfg.ReceiptsPath))
		},
	}
}

func showEarnings(log *receipts.Log) error {
	all, err := log.All()
	if err != nil {
		return err
	}
	if len(all) == 0 {
		fmt.Printf("no settlements yet in %s\n", log.Path())
		return nil
	}

	// Sum in big.Int: HBAR amounts are never handled as floating point.
	total := new(big.Int)
	fmt.Printf("%-18s  %-12s  %-14s  %s\n", "SETTLED", "JOB", "TINYBARS", "TRANSACTION")
	for _, receipt := range all {
		amount, ok := new(big.Int).SetString(receipt.AmountTinybars, 10)
		if ok {
			total.Add(total, amount)
		}
		settled := receipt.SettledAt
		if len(settled) > 16 {
			settled = settled[:16]
		}
		fmt.Printf("%-18s  %-12s  %-14s  %s\n", settled, receipt.JobID, receipt.AmountTinybars, receipt.Transaction)
	}

	fmt.Printf("\n%d settlement(s), %s tinybars (%s)\n", len(all), total.String(), formatHBAR(total))
	return nil
}

// formatHBAR renders tinybars as HBAR using integer division only.
func formatHBAR(tinybars *big.Int) string {
	perHBAR := big.NewInt(100_000_000)
	whole, frac := new(big.Int).QuoRem(tinybars, perHBAR, new(big.Int))
	if frac.Sign() == 0 {
		return whole.String() + " HBAR"
	}
	fraction := strings.TrimRight(fmt.Sprintf("%08d", frac), "0")
	return whole.String() + "." + fraction + " HBAR"
}
