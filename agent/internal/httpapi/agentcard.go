package httpapi

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/YashIIT0909/ClearGate/agent/internal/nodespec"
)

// agentCard is what an ERC-8004 agent id resolves to.
//
// The on-chain record is deliberately tiny: an id, a domain and an address. It
// has to be, because everything else about a GPU node changes by the minute and
// a transaction per heartbeat would be absurd. What makes the id useful is that
// the domain points here, and this is served by the provider themselves.
//
// So the resolution path is: agent id -> agentDomain -> this card -> the same
// spec a renter reads before paying. ClearGate's registry appears nowhere in
// that chain, which is the entire reason for putting identity on-chain rather
// than in another Postgres column.
type agentCard struct {
	// ProtocolVersion follows the A2A agent-card convention, so an agent that
	// already knows how to read one of those can read this.
	ProtocolVersion string `json:"protocolVersion"`
	Name            string `json:"name"`
	Description     string `json:"description"`
	URL             string `json:"url"`
	Version         string `json:"version"`

	Provider cardProvider `json:"provider"`

	// Registrations is the on-chain identity this card is the resolution
	// target of. Empty on a node that never ran `cleargate-node register`,
	// which is a perfectly valid node — it is simply not discoverable by id.
	Registrations []cardRegistration `json:"registrations,omitempty"`

	Capabilities cardCapabilities `json:"capabilities"`
	Skills       []cardSkill      `json:"skills"`

	// Payments is how an agent pays for what is advertised here, stated in the
	// card so a machine can act on it without a separate discovery step.
	Payments cardPayments `json:"payments"`
}

type cardProvider struct {
	Organization string `json:"organization"`
	URL          string `json:"url,omitempty"`
}

type cardRegistration struct {
	// AgentID as a string: JSON numbers are float64 in most parsers, and a
	// uint256 agent id would lose precision long before it stopped being valid.
	AgentID      string `json:"agentId"`
	AgentAddress string `json:"agentAddress"`
	// RegistryContract and CAIP2 together say which chain and which contract
	// issued the id, without which the number resolves to nothing.
	RegistryContract string `json:"registryContract"`
	CAIP2            string `json:"caip2"`
}

type cardCapabilities struct {
	CUDA    bool `json:"cuda"`
	Docker  bool `json:"docker"`
	SSH     bool `json:"ssh"`
	Jupyter bool `json:"jupyter"`
}

type cardSkill struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Tags        []string `json:"tags"`
}

type cardPayments struct {
	Scheme  string `json:"scheme"`
	Network string `json:"network"`
	Asset   string `json:"asset"`
	PayTo   string `json:"payTo"`
	// Refundable is true on every node selling sessions: the unburned remainder
	// of a paid chunk comes back when the session ends.
	Refundable bool `json:"refundable,omitempty"`
	// AuditTopic lets a paying agent check a provider's settlement history
	// before trusting them with money, not only afterwards.
	AuditTopic string `json:"auditTopic,omitempty"`
}

// handleAgentCard serves this node's card. Free, unauthenticated, cacheable.
//
// Free because it must be: an agent discovering a provider has not paid yet and
// has no token, and a card behind a paywall could never serve its purpose.
// Everything in it is already published through `GET /v1/specs` or is on-chain.
func (s *Server) handleAgentCard(w http.ResponseWriter, r *http.Request) {
	spec := s.Spec(r.Context())

	// A short cache: an agent resolving several providers should not hammer
	// each one, but a provider who re-registers or changes price wants that
	// visible in seconds rather than hours.
	w.Header().Set("Cache-Control", "public, max-age=60")
	writeJSON(w, http.StatusOK, s.agentCardFrom(spec))
}

func (s *Server) agentCardFrom(spec nodespec.Spec) agentCard {
	card := agentCard{
		ProtocolVersion: "0.3.0",
		Name:            "ClearGate node " + s.cfg.NodeID,
		Description:     describeNode(spec),
		URL:             strings.TrimRight(s.cfg.PublicURL, "/"),
		Version:         s.version,
		Provider: cardProvider{
			Organization: "ClearGate",
			URL:          s.cfg.RegistryURL,
		},
		Capabilities: cardCapabilities{
			// Derived, never stored: a second copy of "what can this node do"
			// would only be a second thing to keep in step with the first.
			CUDA:    spec.GPU.Available,
			Docker:  true,
			SSH:     spec.Leases != nil && spec.Leases.SSH,
			Jupyter: spec.Leases != nil && spec.Leases.Jupyter,
		},
		Skills: nodeSkills(spec),
		Payments: cardPayments{
			Scheme:     "x402",
			Network:    s.cfg.Network,
			Asset:      s.cfg.Asset,
			PayTo:      s.cfg.PayTo,
			AuditTopic: spec.AuditTopic,
		},
	}

	// Interactive time is only ever sold as a metered session, which refunds
	// what a renter does not use.
	if s.cfg.Leases.Enabled {
		card.Payments.Refundable = true
	}

	if spec.AgentID != 0 {
		card.Registrations = []cardRegistration{{
			AgentID:          strconv.FormatUint(spec.AgentID, 10),
			AgentAddress:     spec.AgentAddress,
			RegistryContract: spec.IdentityRegistry,
			CAIP2:            caip2For(s.cfg.Network),
		}}
	}

	return card
}

// nodeSkills lists what this node actually sells, so an agent can match on a
// skill rather than inferring it from a price field.
func nodeSkills(spec nodespec.Spec) []cardSkill {
	if spec.Leases == nil {
		return []cardSkill{}
	}
	return []cardSkill{{
		ID:          "gpu-session",
		Name:        "Metered GPU session",
		Description: "A Jupyter server in a container on this node's GPU, billed by the second, with unused credit refunded.",
		Tags:        []string{"gpu", "interactive", "jupyter", "metered", "x402"},
	}}
}

func describeNode(spec nodespec.Spec) string {
	if spec.GPU.Available && spec.GPU.Model != nil {
		return "GPU sessions on " + *spec.GPU.Model + ", billed by the second and settled on Hedera."
	}
	return "CPU sessions, billed by the second and settled on Hedera."
}

// caip2For maps ClearGate's network string to the CAIP-2 chain id an agent
// outside this project would recognise.
func caip2For(network string) string {
	switch network {
	case "hedera:mainnet":
		return "eip155:295"
	default:
		return "eip155:296"
	}
}
