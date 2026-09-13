// Package nodespec is the node's description of itself.
//
// It has two readers that must never disagree: `GET /v1/specs`, which a renter
// reads before paying, and the heartbeat this node sends the registry. Both
// build from this one type, so a field added for one appears in the other.
package nodespec

import (
	"github.com/YashIIT0909/ClearGate/agent/internal/config"
	"github.com/YashIIT0909/ClearGate/agent/internal/escrow"
	"github.com/YashIIT0909/ClearGate/agent/internal/runner"
)

// Spec mirrors NodeSpec in packages/types. Changing it is a cross-team break.
type Spec struct {
	NodeID         string `json:"node_id"`
	AgentVersion   string `json:"agent_version"`
	PayTo          string `json:"pay_to"`
	FacilitatorURL string `json:"facilitator_url"`
	Network        string `json:"network"`
	Asset          string `json:"asset"`
	GPU            GPU    `json:"gpu"`

	// FeePayer lets a client pre-build a payment without first taking a 402.
	// Omitted when the facilitator could not be reached.
	FeePayer string `json:"fee_payer,omitempty"`

	// Leases describes this node's timed interactive access, if it offers any.
	// Omitted entirely on a node that did not opt in, so a renter's client sees
	// the same shape it saw before leasing existed.
	Leases *LeaseOffer `json:"leases,omitempty"`

	// AgentID is this provider's ERC-8004 identity, or 0 if they never ran
	// `cleargate-node register`. Zero rather than omitted because the contract
	// issues ids from 1 precisely so that 0 means "not registered".
	AgentID uint64 `json:"agent_id,omitempty"`

	// AgentAddress is the EVM address that identity is bound to on-chain.
	AgentAddress string `json:"agent_address,omitempty"`

	// IdentityRegistry is the contract that issued AgentID. Published because
	// an agent id without its registry is unresolvable — the number alone says
	// nothing about who minted it.
	IdentityRegistry string `json:"identity_registry,omitempty"`

	// AuditTopic is the HCS topic this node publishes settlements to, if any.
	// A renter can read it before paying to see how the provider has actually
	// behaved, which is the entire point of publishing it.
	AuditTopic string `json:"audit_topic,omitempty"`
}

// LeaseOffer is what a renter is buying: a container on this machine's GPU,
// metered by the second.
//
// SSH and Jupyter are separate booleans so a renter learns what a lease offers
// before they pay. Every lease is published through a quick tunnel, which
// carries no TCP, so SSH is always false; the field stays so older clients keep
// parsing it.
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

	// PaymentMode is how this node's interactive time is paid for, and it is
	// always "session": a metered credit whose unburned remainder comes back.
	// Older builds also advertised "direct" (prepaid, not refundable), which a
	// current client does not buy — so it stays published for a client to
	// check rather than assume.
	PaymentMode string `json:"payment_mode,omitempty"`

	// PriceTinybarsPerSecond is the rate a session's credit burns at. Per
	// second rather than per minute because that is the granularity a refund is
	// computed at, which is the entire reason to choose this flow.
	PriceTinybarsPerSecond string `json:"price_tinybars_per_second,omitempty"`

	// ChunkSeconds is the most time one session payment ever buys, and so the
	// bound on how much of a renter's money the provider is ever holding ahead
	// of the compute it pays for.
	ChunkSeconds int `json:"chunk_seconds,omitempty"`
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
			SSH:                    false, // a quick tunnel carries no TCP, so there is never an SSH route
			Jupyter:                true,
			GPU:                    leaseGPU,
			MemoryMB:               cfg.Leases.Limits.MemoryMB,
			CPUCores:               cfg.Leases.Limits.CPUCores,
			WorkspaceGB:            cfg.Leases.Limits.WorkspaceGB,
			EgressAllowlist:        cfg.Leases.Egress.Allowlist,
		}
		leases.PaymentMode = config.PaymentSession
		leases.PriceTinybarsPerSecond = sessionPrice(cfg.Leases)
		leases.ChunkSeconds = cfg.Leases.SessionChunkSeconds
	}

	return Spec{
		NodeID: cfg.NodeID,
		Leases: leases,
		// Identity and audit trail travel with the spec so `/v1/specs`, the
		// heartbeat and the agent card cannot disagree about them — the same
		// reason this function exists at all.
		AgentID:          cfg.Identity.AgentID,
		AgentAddress:     cfg.Identity.AgentAddress,
		IdentityRegistry: cfg.Identity.RegistryContractID,
		AuditTopic:       auditTopic(cfg),
		AgentVersion:     version,
		PayTo:            cfg.PayTo,
		FacilitatorURL:   cfg.FacilitatorURL,
		Network:          cfg.Network,
		Asset:            cfg.Asset,
		FeePayer:         feePayer,
		GPU:              gpu,
	}
}

// auditTopic is the HCS topic this node publishes to, or "" if it does not.
//
// Advertised so a renter can audit a provider's settlement history before
// paying them, rather than only after. A topic id is public by nature — it is
// readable by anyone through any mirror node — so there is nothing here a
// provider is giving away.
func auditTopic(cfg config.Config) string {
	if !cfg.HCS.Enabled {
		return ""
	}
	return cfg.HCS.TopicID
}

// sessionPrice is the per-second rate a session settles at.
//
// Derived from the per-minute lease price unless a provider set it explicitly,
// and rounded UP for the reason escrow.PricePerSecond documents: rounding down
// would quietly pay the provider less than their configured rate, because the
// contract does the final multiplication.
func sessionPrice(leases config.Leases) string {
	if leases.PriceTinybarsPerSecond != "" {
		return leases.PriceTinybarsPerSecond
	}
	price, err := escrow.PricePerSecond(leases.PriceTinybarsPerMinute)
	if err != nil {
		// A misconfigured price fails validation at load, so this is
		// unreachable; advertising nothing beats advertising a wrong number.
		return ""
	}
	return price.String()
}
