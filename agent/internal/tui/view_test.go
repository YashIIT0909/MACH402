package tui

import (
	"io"
	"log/slog"
	"math/big"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/YashIIT0909/ClearGate/agent/internal/config"
	"github.com/YashIIT0909/ClearGate/agent/internal/httpapi"
	"github.com/YashIIT0909/ClearGate/agent/internal/receipts"
	"github.com/YashIIT0909/ClearGate/agent/internal/registry"
	"github.com/YashIIT0909/ClearGate/agent/internal/runner"
	"github.com/YashIIT0909/ClearGate/agent/internal/x402"
)

// The dashboard has to survive terminals that are much narrower and much
// shorter than the one it was designed on — a provider watching their node over
// SSH from a phone is a real case, and a panel that overflows its width corrupts
// every line below it rather than just looking cramped.
func TestEveryTabFitsItsTerminal(t *testing.T) {
	sizes := []struct{ width, height int }{
		{140, 44}, {100, 32}, {80, 24}, {64, 18}, {40, 12},
	}

	for _, size := range sizes {
		model := testModel(t)
		model.width, model.height = size.width, size.height

		for _, target := range tabOrder {
			model.tab = target
			rendered := model.View()

			for i, line := range strings.Split(strings.TrimRight(rendered, "\n"), "\n") {
				if got := lipgloss.Width(line); got > max(size.width, 64) {
					t.Errorf("%dx%d tab %s line %d is %d cells wide, wider than the terminal",
						size.width, size.height, target.title(), i, got)
				}
			}
		}
	}
}

// A metered session is the state where the dashboard is carrying the most
// weight: the credit on screen is simultaneously the renter's remaining runway
// and the refund this node owes them, and a provider has no other local view of
// it. So it has to render, and it has to name the debt.
func TestSessionShowsWhatIsOwedBack(t *testing.T) {
	model := testModel(t)
	model.tab = tabLeases

	lease := runner.NewMeteredLeaseForTest(
		"lease1234abcd", "sess99", "0.0.4242",
		big.NewInt(1_000_000), big.NewInt(300_000_000))
	runner.AdoptLeaseForTest(model.runner, lease)

	rendered := stripANSI(model.View())
	for _, want := range []string{"owed back", "3 HBAR", "sess99", "0.0.4242"} {
		if !strings.Contains(rendered, want) {
			t.Errorf("the leasing screen does not mention %q:\n%s", want, rendered)
		}
	}
}

// Killing a job forfeits a renter's payment and ending a lease takes a
// stranger's shell away mid-command. Neither may happen on one keystroke.
func TestDestructiveKeysAskFirst(t *testing.T) {
	model := testModel(t)
	model.tab = tabLeases

	lease := runner.NewMeteredLeaseForTest(
		"lease1234abcd", "sess99", "0.0.4242",
		big.NewInt(1_000_000), big.NewInt(300_000_000))
	runner.AdoptLeaseForTest(model.runner, lease)

	model.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'e'}})
	if model.confirm != "e" {
		t.Fatalf("pressing e did not arm a confirmation, it would have acted immediately")
	}

	// Anything other than the same key again abandons the action.
	model.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}})
	if model.confirm != "" {
		t.Fatalf("the confirmation survived an unrelated keystroke")
	}
	if lease.Status().IsTerminal() {
		t.Fatalf("the lease was ended by a keystroke that should have cancelled")
	}
}

// Being invisible on the website is a failure a provider can only see here: the
// daemon logs it at warn level and carries on selling, which is correct and also
// silent.
func TestRegistryTroubleIsVisible(t *testing.T) {
	model := testModel(t)
	model.registryStatus = func() registry.Status {
		return registry.Status{
			Configured:  true,
			URL:         "https://registry.example",
			LastAttempt: time.Now(),
			Err:         "registry answered 401 Unauthorized",
		}
	}

	rendered := stripANSI(model.View())
	if !strings.Contains(rendered, "never reached") {
		t.Errorf("a registry that has never answered is not reported:\n%s", rendered)
	}
}

func testModel(t *testing.T) *Model {
	t.Helper()

	cfg := config.Config{
		NodeID:            "node_7f3a2b41",
		PayTo:             "0.0.1234567",
		PriceTinybars:     "100000",
		Asset:             "0.0.0",
		Network:           "hedera:testnet",
		FacilitatorURL:    "https://api.testnet.blocky402.com",
		MaxTimeoutSeconds: 300,
		ListenAddr:        "0.0.0.0:8080",
		PublicURL:         "https://node.example",
		ImageAllowlist:    []string{"python:3.11-slim", "pytorch/pytorch:2.3.0-cuda12.1-cudnn8-runtime"},
		ReceiptsPath:      "receipts.jsonl",
		DockerHost:        "unix:///var/run/docker.sock",
		CORS:              config.CORS{AllowedOrigins: []string{"*"}},
		Limits:            config.Limits{MaxSeconds: 600, MemoryMB: 4096, CPUCores: 2, MaxArtifactMB: 512},
		Dataset:           config.Dataset{MaxMB: 2048, TimeoutSeconds: 600},
		HCS:               config.HCS{Enabled: true, TopicID: "0.0.7654321"},
		Hedera:            config.Hedera{Enabled: true, OperatorAccountID: "0.0.9999", MirrorURL: "https://testnet.mirrornode.hedera.com"},
		Leases: config.Leases{
			Enabled:                   true,
			Image:                     "cleargate/lease:cuda",
			PriceTinybarsPerMinute:    "2000000",
			MinMinutes:                5,
			MaxMinutes:                60,
			MaxTotalMinutes:           240,
			OverrunSeconds:            30,
			GraceMinutes:              5,
			PaymentMode:               config.PaymentSession,
			SessionChunkSeconds:       300,
			LowCreditThresholdSeconds: 60,
			SelfSettle:                true,
			Egress:                    config.Egress{Allowlist: []string{"pypi.org", "huggingface.co"}},
		},
	}

	run := runner.NewForTest()
	runner.ConfigureForTest(run, cfg,
		runner.GPU{Available: true, Model: "NVIDIA RTX 4090", VRAMMb: 24564}, true)

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	server := httpapi.New(cfg, run, x402.NewFacilitator(cfg.FacilitatorURL, time.Second), log, "v0.4.1")

	model := New(Options{
		Config:  cfg,
		Runner:  run,
		Server:  server,
		Version: "v0.4.1",
		Receipts: []receipts.Receipt{{
			JobID:          "job00001",
			Payer:          "0.0.555111",
			AmountTinybars: "100000",
			SettledAt:      time.Now().Add(-3 * time.Hour).Format(time.RFC3339Nano),
		}},
		RegistryStatus: func() registry.Status {
			return registry.Status{
				Configured:  true,
				URL:         "https://registry.cleargate.dev",
				LastAttempt: time.Now(),
				LastSuccess: time.Now(),
			}
		},
	})
	model.gpuUtil, model.gpuUsedMB = 72, 18120
	model.gpuHistory = []int{4, 9, 30, 55, 80, 96, 91, 72}
	return model
}

// stripANSI removes styling so an assertion is about the words on screen rather
// than the escape sequences around them.
func stripANSI(text string) string {
	var b strings.Builder
	for i := 0; i < len(text); i++ {
		if text[i] == 0x1b {
			for i < len(text) && text[i] != 'm' {
				i++
			}
			continue
		}
		b.WriteByte(text[i])
	}
	return b.String()
}
