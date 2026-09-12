package httpapi

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/YashIIT0909/ClearGate/agent/internal/config"
)

// Minutes bought are minutes delivered.
//
// This pins the number that was wrong in the shipped default. The tolerance
// used to be missed_extensions x the slice the renter bought, so a 15-minute
// lease ran for 45 minutes before it was even frozen and 55 before the
// container died — the provider gave away roughly three times what they sold,
// and from the outside it looked like the node ignoring its own expiry.
func TestFreezeToleranceDoesNotScaleWithTheSliceBought(t *testing.T) {
	server := &Server{cfg: config.Config{Leases: config.Leases{
		OverrunSeconds:   30,
		MissedExtensions: 2,
	}}}

	for _, minutes := range []int{5, 15, 60, 120} {
		tolerance := server.freezeTolerance("")

		if tolerance != 30*time.Second {
			t.Errorf("got a %s tolerance; it must come from overrun_seconds alone", tolerance)
		}
		// The specific regression: never a multiple of the purchased time.
		if tolerance >= time.Duration(minutes)*time.Minute {
			t.Errorf("a %d-minute lease may overrun by %s, which is at least as long as the time sold",
				minutes, tolerance)
		}
	}
}

// An escrow session keeps its own, separate tolerance: it is overdue the moment
// the clock passes what the contract was paid for, and the only slack it needs
// is for mirror-node ingestion lag on a top-up.
func TestEscrowSessionKeepsItsOwnTolerance(t *testing.T) {
	server := &Server{cfg: config.Config{Leases: config.Leases{OverrunSeconds: 300}}}

	if got := server.freezeTolerance("sess_abc"); got != sessionFreezeGrace {
		t.Errorf("session tolerance = %s, want %s — a provider's overrun_seconds must not "+
			"extend time the contract will never pay for", got, sessionFreezeGrace)
	}
}

// A config written before overrun_seconds existed must not freeze a renter the
// instant their clock runs out, leaving no room for an extension already being
// approved in a wallet.
func TestOverrunSecondsDefaultsForOlderConfigs(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	older := "" +
		"node_id: node_test\n" +
		"pay_to: \"0.0.1234\"\n" +
		"price_tinybars: \"100000\"\n" +
		"leases:\n" +
		"  enabled: true\n" +
		"  missed_extensions: 2\n"

	if err := os.WriteFile(path, []byte(older), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("load a config predating overrun_seconds: %v", err)
	}
	if cfg.Leases.OverrunSeconds <= 0 {
		t.Fatalf("overrun_seconds = %d; absent must fall back, not mean zero tolerance",
			cfg.Leases.OverrunSeconds)
	}
}
