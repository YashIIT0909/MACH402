// Package nodespec is the node's description of itself.
//
// It has two readers that must never disagree: `GET /v1/specs`, which a renter
// reads before paying, and the heartbeat this node sends the registry. Both
// build from this one type, so a field added for one appears in the other.
package nodespec

import (
	"github.com/YashIIT0909/ClearGate/agent/internal/config"
	"github.com/YashIIT0909/ClearGate/agent/internal/runner"
)

// Spec mirrors NodeSpec in packages/types. Changing it is a cross-team break.
type Spec struct {
	NodeID         string   `json:"node_id"`
	AgentVersion   string   `json:"agent_version"`
	PayTo          string   `json:"pay_to"`
	PriceTinybars  string   `json:"price_tinybars"`
	FacilitatorURL string   `json:"facilitator_url"`
	Network        string   `json:"network"`
	Asset          string   `json:"asset"`
	ImageAllowlist []string `json:"image_allowlist"`
	GPU            GPU      `json:"gpu"`
	Limits         Limits   `json:"limits"`

	// FeePayer lets a client pre-build a payment without first taking a 402.
	// Omitted when the facilitator could not be reached.
	FeePayer string `json:"fee_payer,omitempty"`
}

// GPU is what this node can actually pass through to a container. Model and
// VRAM are pointers so an absent card serializes as null rather than as a
// misleading "" and 0.
type GPU struct {
	Available bool    `json:"available"`
	Model     *string `json:"model"`
	VRAMMb    *int    `json:"vram_mb"`
	Reason    string  `json:"reason,omitempty"`
}

// Limits are the per-job caps a renter is buying within.
type Limits struct {
	MaxSeconds    int   `json:"max_seconds"`
	MemoryMB      int64 `json:"memory_mb"`
	CPUCores      int   `json:"cpu_cores"`
	MaxArtifactMB int64 `json:"max_artifact_mb"`
}

// Heartbeat is what the node POSTs to the registry: its spec, plus the two
// things only the node knows — where to reach it, and whether it is paused.
type Heartbeat struct {
	Spec
	PublicURL string `json:"public_url"`
	Paused    bool   `json:"paused"`
}

// Build assembles the spec. feePayer may be empty if the facilitator was
// unreachable; that is worth advertising as "unknown", not worth failing over.
func Build(cfg config.Config, version, feePayer string, detected runner.GPU) Spec {
	gpu := GPU{Available: detected.Available, Reason: detected.Reason}
	if detected.Model != "" {
		model := detected.Model
		gpu.Model = &model
	}
	if detected.VRAMMb > 0 {
		vram := detected.VRAMMb
		gpu.VRAMMb = &vram
	}

	return Spec{
		NodeID:         cfg.NodeID,
		AgentVersion:   version,
		PayTo:          cfg.PayTo,
		PriceTinybars:  cfg.PriceTinybars,
		FacilitatorURL: cfg.FacilitatorURL,
		Network:        cfg.Network,
		Asset:          cfg.Asset,
		ImageAllowlist: cfg.ImageAllowlist,
		FeePayer:       feePayer,
		GPU:            gpu,
		Limits: Limits{
			MaxSeconds:    cfg.Limits.MaxSeconds,
			MemoryMB:      cfg.Limits.MemoryMB,
			CPUCores:      cfg.Limits.CPUCores,
			MaxArtifactMB: cfg.Limits.MaxArtifactMB,
		},
	}
}
