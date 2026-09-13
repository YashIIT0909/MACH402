// Package config loads and saves the node's on-disk configuration.
//
// There is deliberately no key material here. The node receives payment; it
// never signs, so it has nothing to keep secret (CLAUDE.md invariant 1).
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// DefaultPath is where `cleargate-node setup` writes config.yaml.
const DefaultPath = "config.yaml"

// Config is the node's complete configuration.
type Config struct {
	// NodeID is stable across restarts and identifies this node to renters and,
	// from M2, to the registry.
	NodeID string `yaml:"node_id"`

	// PayTo is the Hedera account that receives payment.
	PayTo string `yaml:"pay_to"`

	// Asset is "0.0.0" for HBAR, or an HTS token id for token settlement.
	Asset string `yaml:"asset"`

	// Network is a CAIP-2 identifier. Testnet only for now.
	Network string `yaml:"network"`

	// FacilitatorURL is the facilitator that co-signs and submits to Hedera.
	FacilitatorURL string `yaml:"facilitator_url"`

	// MaxTimeoutSeconds bounds how long a signed payment payload stays valid.
	MaxTimeoutSeconds int `yaml:"max_timeout_seconds"`

	// ListenAddr is the local bind address.
	ListenAddr string `yaml:"listen_addr"`

	// PublicURL is how renters reach this node. It goes into the challenge's
	// resource.url, so it must match what the client actually requested.
	// Empty means derive it from the incoming request.
	PublicURL string `yaml:"public_url"`

	// RegistryURL is the ClearGate registry this node announces itself to, so
	// it appears on the website. Empty means "do not announce": a node is fully
	// functional unlisted, and renters who know its URL can still pay it.
	RegistryURL string `yaml:"registry_url"`

	// RegistryToken proves to the registry that this node owns its listing, so
	// nobody else can repoint node_id at their own machine and collect payments
	// meant for this one. It is a listing credential, not key material: it
	// cannot move funds, and the node still holds no Hedera key.
	RegistryToken string `yaml:"registry_token"`

	// GPUEnabled asks for GPU passthrough. The runner still refuses unless the
	// nvidia container runtime is actually present, and falls back to CPU.
	GPUEnabled bool `yaml:"gpu_enabled"`

	// Leases configures what this node sells: metered sessions on a container
	// with a Jupyter server, on this machine's GPU. `setup` always turns it on;
	// a node with it off runs but sells nothing.
	Leases Leases `yaml:"leases"`

	// Hedera configures the signing sidecar and the mirror node.
	//
	// Off unless a provider opted in. When it is on, the node has a key file on
	// disk for the first time — read by the `cleargate-hedera` child process,
	// never by this binary — so it stays an explicit choice rather than
	// something another feature can switch on for you.
	Hedera Hedera `yaml:"hedera"`

	// HCS publishes every settlement to a topic this provider owns, so earnings
	// can be audited without trusting our website or this node's own log.
	HCS HCS `yaml:"hcs"`

	// Identity is this provider's ERC-8004 agent registration.
	Identity Identity `yaml:"identity"`

	// ReceiptsPath is the append-only local earnings log.
	ReceiptsPath string `yaml:"receipts_path"`

	// DockerHost is the Docker endpoint. Unix socket by default.
	DockerHost string `yaml:"docker_host"`

	// CORS governs which web origins a browser may call this node from.
	//
	// Needed because a renter paying from the ClearGate website is a browser
	// talking straight to this node — payments are renter -> node, direct
	// (CLAUDE.md invariant 3), so there is no server in between to relay them.
	CORS CORS `yaml:"cors"`
}

// CORS is the browser-origin policy for this node's API.
//
// Permissive by default, and that is a considered position rather than a
// shortcut: every authenticated endpoint here is guarded by a bearer token in a
// header that the renter was handed at payment time, and *nothing* on this node
// authenticates with a cookie. Cross-origin access therefore grants a hostile
// page nothing it could not already do by calling the node from its own server,
// because there is no ambient authority in a browser for it to borrow. The
// classic reason to lock CORS down — a logged-in victim's cookies riding along
// on a forged request — does not exist here.
//
// What is never sent is `Access-Control-Allow-Credentials`, which is what would
// change that answer.
type CORS struct {
	// AllowedOrigins is the set of origins permitted to call this node, or
	// ["*"] for any. A provider who serves their own renter UI can narrow this
	// to their own site.
	AllowedOrigins []string `yaml:"allowed_origins"`
}

// Hedera configures the node's one indirect route to signing a transaction.
//
// The agent itself still links no Hedera SDK and reads no key (CLAUDE.md
// invariant 1). What this block enables is a child process — `cleargate-hedera`,
// from the hederakit package — that the node invokes the same way internal/sshca
// invokes ssh-keygen and internal/tunnel invokes cloudflared. The key file named
// here is read by that process and never by this binary.
//
// The account behind that key is NOT pay_to. pay_to accumulates a provider's
// earnings and signs nothing; this one holds a few HBAR of fee float and is the
// only thing a compromise could reach.
type Hedera struct {
	Enabled bool `yaml:"enabled"`

	// Sidecar is the command to invoke. A bare name is looked up on PATH.
	Sidecar string `yaml:"sidecar"`

	// OperatorKeyPath is the node-local key file, mode 0600, generated at setup
	// and never transmitted. Relative paths resolve against config.yaml's
	// directory, like ca_key_path.
	OperatorKeyPath string `yaml:"operator_key_path"`

	// OperatorAccountID is the resolved 0.0.x for that key, cached here once
	// the account has been funded into existence.
	OperatorAccountID string `yaml:"operator_account_id"`

	// MirrorURL is the public mirror node the agent reads to verify deposits
	// and resolve addresses. Reading needs no key, which is what lets escrow
	// verification live in Go at all.
	MirrorURL string `yaml:"mirror_url"`
}

// HCS is the provider's public audit trail.
type HCS struct {
	Enabled bool `yaml:"enabled"`

	// TopicID is created once at setup. The topic's submit key is the operator
	// key, so only this provider can write to their own trail; it has no admin
	// key, so nobody — including the provider — can delete or rewrite it.
	TopicID string `yaml:"topic_id"`
}

// Identity is this node's ERC-8004 registration.
//
// Written by `cleargate-node register` and only read afterwards. `serve` never
// registers: minting a second identity would orphan the first, and an identity
// that appears as a side effect of starting a daemon is not an identity anyone
// chose to claim.
type Identity struct {
	// AgentID is 0 until registered. The contract issues ids from 1 precisely
	// so that 0 stays usable as "not registered".
	AgentID uint64 `yaml:"agent_id"`

	// AgentAddress is the operator key's EVM address, which is what the
	// registration is bound to on-chain.
	AgentAddress string `yaml:"agent_address"`

	// RegistryContractID is the IdentityRegistry deployment to register with.
	RegistryContractID string `yaml:"registry_contract_id"`
}

// Leases is the policy for timed interactive access to this machine.
//
// The renter's code never leaves their machine; instead they get a shell on the
// provider's. That means the containment story has to be strong — see Egress below and
// runner.leaseContainerRequest.
type Leases struct {
	// Enabled gates the whole feature, including the /v1/sessions routes. A node
	// with this false answers 404 there and behaves exactly as it did before
	// leasing existed.
	Enabled bool `yaml:"enabled"`

	// Image is the lease runtime: sshd with certificate authentication plus a
	// Jupyter server. Built by `make lease-image`, or pulled if the operator
	// points this at a published tag.
	Image string `yaml:"image"`

	// PriceTinybarsPerMinute is what a minute of interactive time costs, as a
	// string. Never a float, and never multiplied in floating point — the
	// handler uses math/big.
	PriceTinybarsPerMinute string `yaml:"price_tinybars_per_minute"`

	// MinMinutes and MaxMinutes bound how long a session may ask for. Too short
	// spends more on settlement than on compute; too long pins the node's only
	// lease slot for someone who may have walked away.
	MinMinutes int `yaml:"min_minutes"`
	MaxMinutes int `yaml:"max_minutes"`

	// MaxTotalMinutes caps a session's whole life across top-ups, so a node is
	// never leased out indefinitely by a renter who keeps topping it up.
	MaxTotalMinutes int `yaml:"max_total_minutes"`

	// GraceMinutes is how long a frozen lease survives before it is reaped —
	// container killed, tunnel ingress withdrawn, workspace wiped.
	GraceMinutes int `yaml:"grace_minutes"`

	// CAKeyPath is the node-local SSH certificate authority. The private half
	// is generated here at setup and never leaves this machine, exactly like
	// the node never holding a Hedera key (CLAUDE.md invariant 1).
	CAKeyPath string `yaml:"ca_key_path"`

	// Limits cap one lease container.
	Limits LeaseLimits `yaml:"limits"`

	// Egress is the allowlist the lease container's network is confined to.
	Egress Egress `yaml:"egress"`

	// Tunnel is how a renter reaches the container from outside the provider's
	// NAT.
	Tunnel Tunnel `yaml:"tunnel"`

	// PaymentMode is how interactive time is paid for, and it has one value:
	// "session", which buys a credit over x402 and meters it down second by
	// second, refunding whatever is left when the session ends.
	//
	// "direct" — forward payment per slice, no refunds — was removed. A config
	// still naming it is refused at load with a way forward, rather than
	// quietly switched to a mode that holds a renter's money differently.
	//
	// "escrow" is accepted as the old spelling of "session" and normalizes to
	// it at load. It named a genuinely different mechanism — a deposit into
	// SessionEscrow, verified by reading the mirror node — and a node that used
	// it now meters and refunds instead. The contract itself is still in the
	// repo; see the note in the README about payment_mode: escrow-vault.
	PaymentMode string `yaml:"payment_mode"`

	// EscrowContractID is retained so configs written for the escrow flow still
	// load. Nothing reads it on the metered path.
	EscrowContractID string `yaml:"escrow_contract_id"`

	// PriceTinybarsPerSecond is the rate a session's credit burns at. Left
	// empty it is derived from PriceTinybarsPerMinute, rounding up.
	//
	// The meter multiplies this by elapsed seconds, so once a session opens
	// this is the only price that matters — which is why the node converts once
	// at quote time and treats the per-second figure as authoritative from then
	// on, rather than converting in two places that could disagree.
	PriceTinybarsPerSecond string `yaml:"price_tinybars_per_second"`

	// SessionChunkSeconds is the most time one session payment ever buys.
	//
	// It is the bound on the whole trust gap this mode has: between paying for
	// a chunk and the refund at the end, the provider is holding the renter's
	// money. Capping the chunk caps how much that can ever be, regardless of
	// how long a session the renter asked for — they get a chunk now and top up
	// as they go, and each top-up is independently small.
	SessionChunkSeconds int `yaml:"session_chunk_seconds"`

	// LowCreditThresholdSeconds is when a session starts telling its renter to
	// top up. Reported as `low_credits` on the session state, so the threshold
	// lives on the node that knows its own sweep interval rather than being
	// guessed at by every client.
	LowCreditThresholdSeconds int `yaml:"low_credit_threshold_seconds"`
}

// Lease payment modes. See Leases.PaymentMode.
const (
	PaymentSession = "session"

	// PaymentEscrow is the old spelling of PaymentSession, normalized at load
	// so a config written against the escrow flow still starts.
	PaymentEscrow = "escrow"
)

// LeaseLimits caps one lease container. It is sized for a person working, and
// has no wall-clock kill: the session's credit is the clock.
type LeaseLimits struct {
	MemoryMB    int64 `yaml:"memory_mb"`
	CPUCores    int   `yaml:"cpu_cores"`
	PidsLimit   int64 `yaml:"pids_limit"`
	WorkspaceGB int   `yaml:"workspace_gb"`
}

// Egress is the lease container's network policy.
//
// A renter has to be able to `pip install`, so a container with no network at
// all is not an option. Instead lease containers sit on an internal Docker network with no route
// out and reach the world only through a ClearGate-built proxy that denies by
// default. A renter with a live shell can unset HTTP_PROXY, but there is
// nothing behind it: the network itself has no path off the host.
type Egress struct {
	// ProxyImage is the deny-by-default forward proxy, built by
	// `make lease-image` from agent/lease-image/egress.
	ProxyImage string `yaml:"proxy_image"`

	// Allowlist is matched against the request host: an entry matches that host
	// exactly or any subdomain of it. Empty means the container can reach
	// nothing, which is a usable if unfriendly configuration.
	Allowlist []string `yaml:"allowlist"`
}

// Tunnel is how renters reach a lease container on a machine that has no public
// address — the normal case for a provider behind a home router.
type Tunnel struct {
	// Mode is "quick", the only mode: `cloudflared tunnel --url`, which needs no
	// Cloudflare account and gives an HTTP-only, random, per-lease hostname.
	// Jupyter works; SSH does not. "named" (registry-provisioned) and "off" (no
	// tunnel) were removed, and a config still naming either is refused.
	Mode string `yaml:"mode"`

	// Binary is the cloudflared executable. Looked up on PATH by default.
	Binary string `yaml:"binary"`
}

// Default returns a config that runs safely out of the box: CPU mode and
// conservative caps.
func Default() Config {
	return Config{
		Asset:             "0.0.0",
		Network:           "hedera:testnet",
		FacilitatorURL:    "https://api.testnet.blocky402.com",
		MaxTimeoutSeconds: 300,
		ListenAddr:        "0.0.0.0:8402",
		GPUEnabled:        false,
		Leases:            DefaultLeases(),
		Hedera:            DefaultHedera(),
		ReceiptsPath:      "receipts.jsonl",
		DockerHost:        "unix:///var/run/docker.sock",
		CORS:              DefaultCORS(),
	}
}

// DefaultCORS allows any origin. See the CORS type for why that is safe here.
func DefaultCORS() CORS {
	return CORS{AllowedOrigins: []string{"*"}}
}

// applyDefaults treats an absent or empty `cors:` block as "unset".
//
// An empty allowlist would otherwise mean "no browser may ever call this node",
// which is a setting nobody asks for by leaving a block out — and it would
// break the website's rent flow on every node whose config predates it.
func (c *CORS) applyDefaults() {
	if len(c.AllowedOrigins) == 0 {
		c.AllowedOrigins = DefaultCORS().AllowedOrigins
	}
}

// DefaultLeases is the leasing policy a node gets when it opts in. Enabled is
// false here on purpose: turning this on is `setup --enable-leases`, never a
// default (implementation.md §8.2).
func DefaultLeases() Leases {
	return Leases{
		Enabled:                false,
		Image:                  "cleargate/lease-runtime:dev",
		PriceTinybarsPerMinute: "200000", // 0.002 HBAR/min
		MinMinutes:             5,
		MaxMinutes:             120,
		MaxTotalMinutes:        1440, // one day
		GraceMinutes:           10,
		CAKeyPath:              "lease-ca",
		Limits: LeaseLimits{
			MemoryMB:    16384,
			CPUCores:    4,
			PidsLimit:   4096,
			WorkspaceGB: 50,
		},
		Egress: Egress{
			ProxyImage: "cleargate/lease-egress:dev",
			// Package and model registries, and the hosts they redirect to for
			// the actual bytes. Deny by default otherwise: a live shell on a
			// provider's IP is a materially bigger liability than a sandboxed
			// script, and this is the list that bounds it.
			Allowlist: []string{
				"pypi.org", "files.pythonhosted.org", "pypi.python.org",
				"anaconda.org", "repo.anaconda.com", "conda.anaconda.org",
				"registry.npmjs.org",
				"github.com", "codeload.github.com", "raw.githubusercontent.com",
				"objects.githubusercontent.com", "release-assets.githubusercontent.com",
				"huggingface.co", "cdn-lfs.huggingface.co", "cdn-lfs-us-1.hf.co",
				"download.pytorch.org",
				"deb.debian.org", "security.debian.org",
				"archive.ubuntu.com", "security.ubuntu.com",
				"storage.googleapis.com",
			},
		},
		Tunnel: Tunnel{
			Mode:   TunnelQuick,
			Binary: "cloudflared",
		},
		// The only mode there is. Leasing itself stays off by default, and
		// turning it on is what brings the operator key refunds are paid from.
		PaymentMode: PaymentSession,

		// Five minutes. Long enough that a renter is not paying every few
		// seconds, short enough that the money a provider is holding ahead of
		// the compute is a few tenths of a cent rather than an hour's rental.
		SessionChunkSeconds: 300,

		// A minute of runway. The meter ticks every 15s and a payment round
		// trip is a couple of seconds, so this leaves several chances to top up
		// before the credit hits zero and the container freezes.
		LowCreditThresholdSeconds: 60,
	}
}

// DefaultHedera is the signing sidecar's configuration when a node opts in.
// Disabled here for the same reason leases are: a key file on disk is a choice
// a provider makes, never a side effect.
func DefaultHedera() Hedera {
	return Hedera{
		Enabled:         false,
		Sidecar:         "cleargate-hedera",
		OperatorKeyPath: "hedera-operator",
		MirrorURL:       DefaultMirrorURL,
	}
}

// DefaultMirrorURL is Hedera's public testnet mirror node.
const DefaultMirrorURL = "https://testnet.mirrornode.hedera.com"

// TunnelQuick is the only tunnel mode. See Tunnel.Mode.
const TunnelQuick = "quick"

// Load reads a config file and applies defaults for anything left unset.
func Load(path string) (Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Config{}, fmt.Errorf("no config at %s — run `cleargate-node setup` first", path)
		}
		return Config{}, fmt.Errorf("read %s: %w", path, err)
	}

	cfg := Default()
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		return Config{}, fmt.Errorf("parse %s: %w", path, err)
	}
	cfg.Leases.applyDefaults(filepath.Dir(path))
	cfg.Hedera.applyDefaults(filepath.Dir(path))
	cfg.CORS.applyDefaults()
	if err := cfg.Validate(); err != nil {
		return Config{}, fmt.Errorf("invalid config %s: %w", path, err)
	}
	return cfg, nil
}

// applyDefaults fills in anything a hand-written or older `leases:` block left
// at zero, and resolves relative paths against the config file's directory so a
// node started from another working directory still finds its own CA.
//
// A zero here would not be a strict setting, it would be a broken one: a
// max_minutes of 0 rejects every lease, and a grace_minutes of 0 reaps a frozen
// container the instant it freezes.
func (l *Leases) applyDefaults(configDir string) {
	fallback := DefaultLeases()

	if l.Image == "" {
		l.Image = fallback.Image
	}
	if l.PriceTinybarsPerMinute == "" {
		l.PriceTinybarsPerMinute = fallback.PriceTinybarsPerMinute
	}
	if l.MinMinutes <= 0 {
		l.MinMinutes = fallback.MinMinutes
	}
	if l.MaxMinutes <= 0 {
		l.MaxMinutes = fallback.MaxMinutes
	}
	if l.MaxTotalMinutes <= 0 {
		l.MaxTotalMinutes = fallback.MaxTotalMinutes
	}
	if l.GraceMinutes <= 0 {
		l.GraceMinutes = fallback.GraceMinutes
	}
	if l.CAKeyPath == "" {
		l.CAKeyPath = fallback.CAKeyPath
	}
	if l.Limits.MemoryMB <= 0 {
		l.Limits.MemoryMB = fallback.Limits.MemoryMB
	}
	if l.Limits.CPUCores <= 0 {
		l.Limits.CPUCores = fallback.Limits.CPUCores
	}
	if l.Limits.PidsLimit <= 0 {
		l.Limits.PidsLimit = fallback.Limits.PidsLimit
	}
	if l.Limits.WorkspaceGB <= 0 {
		l.Limits.WorkspaceGB = fallback.Limits.WorkspaceGB
	}
	if l.Egress.ProxyImage == "" {
		l.Egress.ProxyImage = fallback.Egress.ProxyImage
	}
	if l.Egress.Allowlist == nil {
		l.Egress.Allowlist = fallback.Egress.Allowlist
	}
	if l.Tunnel.Mode == "" {
		l.Tunnel.Mode = fallback.Tunnel.Mode
	}
	if l.Tunnel.Binary == "" {
		l.Tunnel.Binary = fallback.Tunnel.Binary
	}
	if l.PaymentMode == "" {
		l.PaymentMode = fallback.PaymentMode
	}
	// The escrow flow's name, kept loadable. A node that opted into refundable
	// interactive time still gets refundable interactive time; what changed
	// underneath is that the refund comes from the provider against a published
	// running balance instead of from a contract.
	if l.PaymentMode == PaymentEscrow {
		l.PaymentMode = PaymentSession
	}
	if l.SessionChunkSeconds <= 0 {
		l.SessionChunkSeconds = fallback.SessionChunkSeconds
	}
	if l.LowCreditThresholdSeconds <= 0 {
		l.LowCreditThresholdSeconds = fallback.LowCreditThresholdSeconds
	}

	if configDir == "" {
		configDir = "."
	}
	l.CAKeyPath = resolveAgainst(configDir, l.CAKeyPath)
}

// applyDefaults fills in a `hedera:` block that an older config never had, and
// resolves the key path against config.yaml's directory so a node started from
// elsewhere still finds its own key — the same rule as the SSH CA.
func (h *Hedera) applyDefaults(configDir string) {
	fallback := DefaultHedera()

	if h.Sidecar == "" {
		h.Sidecar = fallback.Sidecar
	}
	if h.OperatorKeyPath == "" {
		h.OperatorKeyPath = fallback.OperatorKeyPath
	}
	if h.MirrorURL == "" {
		h.MirrorURL = fallback.MirrorURL
	}

	if configDir == "" {
		configDir = "."
	}
	h.OperatorKeyPath = resolveAgainst(configDir, h.OperatorKeyPath)
}

func resolveAgainst(dir, path string) string {
	if path == "" || filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(dir, path)
}

// Save writes the config, creating parent directories as needed.
func Save(path string, cfg Config) error {
	if dir := filepath.Dir(path); dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create %s: %w", dir, err)
		}
	}
	raw, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("encode config: %w", err)
	}
	// 0600, not 0644. There is still no key material here — the node cannot
	// sign anything — but the file holds two credentials that authorize acting
	// as this node: registry_token, which owns its listing, and the cached
	// tunnel token, which owns the route renters connect through.
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

// Validate rejects configurations that would produce an unpayable challenge or
// an unsafe container.
func (c Config) Validate() error {
	if err := c.validateBase(); err != nil {
		return err
	}
	if c.HCS.Enabled && !c.Hedera.Enabled {
		return errors.New("hcs.enabled needs hedera.enabled — publishing to a topic requires the signing sidecar")
	}
	if c.HCS.Enabled && c.HCS.TopicID == "" {
		return errors.New("hcs.enabled is set but hcs.topic_id is empty — re-run `cleargate-node setup --enable-hcs` to create one")
	}
	return nil
}

// ValidateForSetup allows HCS to be enabled while still creating the topic in the
// same setup run. The runtime config remains stricter: a saved config without a
// topic is invalid, but setup itself is the thing that creates it.
func (c Config) ValidateForSetup() error {
	if err := c.validateBase(); err != nil {
		return err
	}
	if c.HCS.Enabled && !c.Hedera.Enabled {
		return errors.New("hcs.enabled needs hedera.enabled — publishing to a topic requires the signing sidecar")
	}
	return nil
}

func (c Config) validateBase() error {
	if !isHederaAccountID(c.PayTo) {
		return fmt.Errorf("pay_to %q is not a Hedera account id like 0.0.1234", c.PayTo)
	}
	if c.Asset == "" {
		return errors.New("asset must be \"0.0.0\" for HBAR or an HTS token id")
	}
	if !strings.HasPrefix(c.Network, "hedera:") {
		return fmt.Errorf("network %q must be a Hedera CAIP-2 id such as hedera:testnet", c.Network)
	}
	if c.FacilitatorURL == "" {
		return errors.New("facilitator_url must be set")
	}
	if c.MaxTimeoutSeconds <= 0 {
		return errors.New("max_timeout_seconds must be positive")
	}
	if c.RegistryURL != "" {
		// A listing renters cannot dial is worse than no listing at all: the
		// registry has nowhere to send them, so refuse to publish one.
		if c.PublicURL == "" {
			return errors.New("public_url must be set when registry_url is — the registry has to tell renters where to reach this node")
		}
		if c.RegistryToken == "" {
			return errors.New("registry_token must be set when registry_url is — re-run `cleargate-node setup` to generate one")
		}
	}
	if err := c.Leases.validate(); err != nil {
		return err
	}
	if err := c.Hedera.validate(); err != nil {
		return err
	}
	if err := c.Identity.validate(); err != nil {
		return err
	}
	// A metered session's whole claim to being trustworthy is the running
	// "owed if you stopped now" figure on the provider's audit topic, and
	// publishing that is a sidecar call. A session node with no key would be
	// asking renters to prepay against nothing but a promise, so this is
	// refused at startup rather than discovered by a renter.
	if c.Leases.Enabled && (!c.Hedera.Enabled || !c.HCS.Enabled) {
		return errors.New("leases.enabled needs hedera.enabled and hcs.enabled, so the node can publish its " +
			"refund-owed trail and pay refunds — re-run `cleargate-node setup --enable-leases`")
	}
	return nil
}

// validate checks the leasing block, but only when leasing is actually on: a
// node that never opted in should not be told its lease price is malformed.
func (l Leases) validate() error {
	if !l.Enabled {
		return nil
	}
	if l.Image == "" {
		return errors.New("leases.image must name the lease runtime image — run `make lease-image` to build it")
	}
	if _, err := strconv.ParseUint(l.PriceTinybarsPerMinute, 10, 64); err != nil {
		return fmt.Errorf("leases.price_tinybars_per_minute %q must be a whole number of tinybars, as a string",
			l.PriceTinybarsPerMinute)
	}
	if l.MinMinutes <= 0 || l.MaxMinutes < l.MinMinutes {
		return errors.New("leases.min_minutes must be positive and no larger than leases.max_minutes")
	}
	if l.MaxTotalMinutes < l.MaxMinutes {
		return errors.New("leases.max_total_minutes must be at least leases.max_minutes, or no session could reach its own maximum")
	}
	if l.CAKeyPath == "" {
		return errors.New("leases.ca_key_path must be set — it is where this node's SSH certificate authority lives")
	}
	switch l.Tunnel.Mode {
	case TunnelQuick:
	case "named", "off":
		// Removed, not renamed. Said plainly so a config written when either
		// existed fails with a way forward rather than a bare "invalid value".
		return fmt.Errorf("leases.tunnel.mode %q has been removed — leases are published only through a "+
			"Cloudflare quick tunnel; set leases.tunnel.mode to %q", l.Tunnel.Mode, TunnelQuick)
	default:
		return fmt.Errorf("leases.tunnel.mode %q must be %q", l.Tunnel.Mode, TunnelQuick)
	}
	if l.Egress.ProxyImage == "" {
		return errors.New("leases.egress.proxy_image must be set — lease containers reach the network only through it")
	}
	switch l.PaymentMode {
	case "direct":
		// Removed, not renamed. Said plainly so a node configured before the
		// change fails with a way forward rather than a bare "invalid value".
		return fmt.Errorf("leases.payment_mode %q has been removed — interactive time is sold only as metered "+
			"sessions that refund unused credit; set leases.payment_mode to %q and re-run "+
			"`cleargate-node setup --enable-leases` so the node has a key to publish and pay refunds with",
			l.PaymentMode, PaymentSession)
	case PaymentSession:
		// A chunk longer than a session may run would sell more time in one
		// payment than the node offers at all, which is exactly the exposure
		// chunking exists to bound. The chunk is also what the container is
		// provisioned for, so it has to sit inside the same bounds.
		if l.SessionChunkSeconds < l.MinMinutes*60 {
			return fmt.Errorf("leases.session_chunk_seconds (%d) must be at least leases.min_minutes (%d minutes)",
				l.SessionChunkSeconds, l.MinMinutes)
		}
		if l.SessionChunkSeconds > l.MaxMinutes*60 {
			return fmt.Errorf("leases.session_chunk_seconds (%d) must not exceed leases.max_minutes (%d minutes)",
				l.SessionChunkSeconds, l.MaxMinutes)
		}
		if l.LowCreditThresholdSeconds >= l.SessionChunkSeconds {
			return fmt.Errorf("leases.low_credit_threshold_seconds (%d) must be less than leases.session_chunk_seconds (%d), "+
				"or every session would be low on credit the moment it opened",
				l.LowCreditThresholdSeconds, l.SessionChunkSeconds)
		}
	default:
		return fmt.Errorf("leases.payment_mode %q must be %q", l.PaymentMode, PaymentSession)
	}
	if l.PriceTinybarsPerSecond != "" {
		if _, err := strconv.ParseUint(l.PriceTinybarsPerSecond, 10, 64); err != nil {
			return fmt.Errorf("leases.price_tinybars_per_second %q must be a whole number of tinybars, as a string",
				l.PriceTinybarsPerSecond)
		}
	}
	return nil
}

// validate checks the signing sidecar's configuration.
//
// Only meaningful when enabled: a node that never opted in has no key, no
// sidecar and nothing to get wrong.
func (h Hedera) validate() error {
	if !h.Enabled {
		return nil
	}
	if h.Sidecar == "" {
		return errors.New("hedera.sidecar must name the cleargate-hedera command")
	}
	if h.OperatorKeyPath == "" {
		return errors.New("hedera.operator_key_path must be set — it is where this node's operator key lives")
	}
	if h.MirrorURL == "" {
		return errors.New("hedera.mirror_url must be set — the node reads it to verify deposits")
	}
	if h.OperatorAccountID != "" && !isHederaAccountID(h.OperatorAccountID) {
		return fmt.Errorf("hedera.operator_account_id %q is not a Hedera account id like 0.0.1234", h.OperatorAccountID)
	}
	return nil
}

// validate checks the ERC-8004 registration block.
func (i Identity) validate() error {
	// An agent id without the contract it was issued by is unresolvable — the
	// number alone means nothing without knowing which registry minted it.
	if i.AgentID != 0 && i.RegistryContractID == "" {
		return errors.New("identity.agent_id is set but identity.registry_contract_id is not; an agent id is meaningless without the registry that issued it")
	}
	return nil
}

// isHederaAccountID checks the shard.realm.num shape without pulling in a
// Hedera SDK, which this binary must never link (CLAUDE.md invariant 1).
func isHederaAccountID(s string) bool {
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return false
	}
	for _, part := range parts {
		if part == "" {
			return false
		}
		if _, err := strconv.ParseUint(part, 10, 64); err != nil {
			return false
		}
	}
	return true
}
