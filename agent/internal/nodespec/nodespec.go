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

	// Leases describes this node's timed interactive access, if it offers any.
	// Omitted entirely on a node that did not opt in, so a renter's client sees
	// the same shape it saw before leasing existed.
	Leases *LeaseOffer `json:"leases,omitempty"`
}

// LeaseOffer is what a renter is buying when they rent a shell rather than
// submit a job: a container on this machine's GPU, for a number of minutes.
//
// SSH and Jupyter are separate booleans because they genuinely differ by tunnel
// mode — a node running a quick tunnel can serve a notebook but has no TCP
// route for a terminal — and a renter needs to know that before they pay, not
// after.
type LeaseOffer struct {
	PriceTinybarsPerMinute string `json:"price_tinybars_per_minute"`
	MinMinutes             int    `json:"min_minutes"`
	MaxMinutes             int    `json:"max_minutes"`
	MaxTotalMinutes        int    `json:"max_total_minutes"`
	SSH                    bool   `json:"ssh"`
	Jupyter                bool   `json:"jupyter"`
	// GPU is whether a lease container can actually compute on this node's card.
	// It is narrower than the node-level `gpu.available`, which describes the
	// host: a node can have a working card and a lease image with no CUDA
	// runtime, and a renter needs to know that before paying rather than after.
	GPU         bool  `json:"gpu"`
	MemoryMB    int64 `json:"memory_mb"`
	CPUCores    int   `json:"cpu_cores"`
	WorkspaceGB int   `json:"workspace_gb"`
	// EgressAllowlist is what the container may reach on the network. Published
	// because it is a real constraint on the work a renter can do here — a lease
	// that cannot reach the index they need is not the lease they wanted.
	EgressAllowlist []string `json:"egress_allowlist"`
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
//
// leaseGPU is narrower than detected.Available: it is whether a *lease
// container* can compute on the card, which additionally requires the lease
// image to carry a CUDA runtime.
func Build(cfg config.Config, version, feePayer string, detected runner.GPU, leaseGPU bool) Spec {
	gpu := GPU{Available: detected.Available, Reason: detected.Reason}
	if detected.Model != "" {
		model := detected.Model
		gpu.Model = &model
	}
	if detected.VRAMMb > 0 {
		vram := detected.VRAMMb
		gpu.VRAMMb = &vram
	}

	var leases *LeaseOffer
	if cfg.Leases.Enabled {
		leases = &LeaseOffer{
			PriceTinybarsPerMinute: cfg.Leases.PriceTinybarsPerMinute,
			MinMinutes:             cfg.Leases.MinMinutes,
			MaxMinutes:             cfg.Leases.MaxMinutes,
			MaxTotalMinutes:        cfg.Leases.MaxTotalMinutes,
			// Only a named tunnel carries TCP, and SSH is TCP. Advertising a
			// terminal a quick tunnel cannot provide would sell something that
			// does not exist.
			SSH:             cfg.Leases.Tunnel.Mode != config.TunnelQuick,
			Jupyter:         true,
			GPU:             leaseGPU,
			MemoryMB:        cfg.Leases.Limits.MemoryMB,
			CPUCores:        cfg.Leases.Limits.CPUCores,
			WorkspaceGB:     cfg.Leases.Limits.WorkspaceGB,
			EgressAllowlist: cfg.Leases.Egress.Allowlist,
		}
	}

	return Spec{
		NodeID:         cfg.NodeID,
		Leases:         leases,
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
