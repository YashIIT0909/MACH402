package nodespec_test

import (
	"encoding/json"
	"testing"

	"github.com/YashIIT0909/ClearGate/agent/internal/config"
	"github.com/YashIIT0909/ClearGate/agent/internal/nodespec"
	"github.com/YashIIT0909/ClearGate/agent/internal/runner"
)

// The registry validates every one of these and rejects a heartbeat missing
// any, so renaming a field here is a cross-team break, not a refactor.
var requiredHeartbeatFields = []string{
	"node_id", "agent_version", "public_url", "pay_to", "price_tinybars",
	"facilitator_url", "network", "asset", "paused", "gpu", "limits", "image_allowlist",
}

func testConfig() config.Config {
	cfg := config.Default()
	cfg.NodeID = "node_0123456789abcdef"
	cfg.PayTo = "0.0.1234"
	cfg.PublicURL = "http://localhost:8402"
	return cfg
}

func marshal(t *testing.T, value any) map[string]any {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return decoded
}

func TestHeartbeatCarriesEveryFieldTheRegistryRequires(t *testing.T) {
	beat := nodespec.Heartbeat{
		Spec:      nodespec.Build(testConfig(), "v1.2.3", "0.0.5678", runner.GPU{Available: true, Model: "NVIDIA RTX 4090", VRAMMb: 24576}),
		PublicURL: "http://localhost:8402",
		Paused:    false,
	}

	decoded := marshal(t, beat)
	for _, field := range requiredHeartbeatFields {
		if _, present := decoded[field]; !present {
			t.Errorf("heartbeat is missing %q, which the registry requires", field)
		}
	}

	// A string end to end: tinybars overflow a double, and no amount in
	// ClearGate is ever a number on the wire.
	if _, ok := decoded["price_tinybars"].(string); !ok {
		t.Errorf("price_tinybars must serialize as a string, got %T", decoded["price_tinybars"])
	}
}

// A node with no usable card must say so as null rather than as "" and 0,
// which would render on the website as a real GPU with no memory.
func TestCPUFallbackReportsNullGPUDetails(t *testing.T) {
	spec := nodespec.Build(testConfig(), "v1.2.3", "", runner.GPU{
		Available: false,
		Reason:    "nvidia-smi unavailable",
	})

	decoded := marshal(t, spec)
	gpu, ok := decoded["gpu"].(map[string]any)
	if !ok {
		t.Fatalf("gpu is not an object: %T", decoded["gpu"])
	}
	if gpu["available"] != false {
		t.Errorf("gpu.available = %v, want false", gpu["available"])
	}
	if gpu["model"] != nil {
		t.Errorf("gpu.model = %v, want null", gpu["model"])
	}
	if gpu["vram_mb"] != nil {
		t.Errorf("gpu.vram_mb = %v, want null", gpu["vram_mb"])
	}

	// An unknown fee payer is left out entirely: a guessed one would fail the
	// client SDK's check against /supported (CLAUDE.md invariant 5).
	if _, present := decoded["fee_payer"]; present {
		t.Error("fee_payer should be omitted when the facilitator did not confirm one")
	}
}
