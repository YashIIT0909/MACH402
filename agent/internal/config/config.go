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

	// PriceTinybars is the flat price of one job, as a string. Never a float.
	PriceTinybars string `yaml:"price_tinybars"`

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
	// nobody else can repoint node_id at their own machine and collect jobs
	// meant for this one. It is a listing credential, not key material: it
	// cannot move funds, and the node still holds no Hedera key.
	RegistryToken string `yaml:"registry_token"`

	// GPUEnabled asks for GPU passthrough. The runner still refuses unless the
	// nvidia container runtime is actually present, and falls back to CPU.
	GPUEnabled bool `yaml:"gpu_enabled"`

	// ImageAllowlist is the complete set of images this node will run.
	// Arbitrary user-supplied images are out of scope (CLAUDE.md invariant 6).
	ImageAllowlist []string `yaml:"image_allowlist"`

	// Leases configures timed interactive access — an SSH shell and a Jupyter
	// server on this machine's GPU. It is off unless a provider explicitly
	// opted in with `setup --enable-leases`: handing a stranger a live shell is
	// a bigger trust ask than running their sandboxed batch job, and must never
	// be switched on as a side effect of enabling something else.
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

	// Limits cap what one job may consume.
	Limits Limits `yaml:"limits"`

	// Dataset governs downloading renter-supplied dataset URLs.
	Dataset Dataset `yaml:"dataset"`

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

// Dataset is the policy for fetching renter-supplied dataset URLs.
//
// The node downloads these itself, from inside the provider's own network, on
// behalf of a paying stranger. The defaults are therefore deliberately strict:
// https only, no private addresses, and a size cap that cannot fill a disk.
type Dataset struct {
	// MaxMB is the largest dataset this node will fetch, enforced both against
	// the advertised Content-Length and by counting bytes as they arrive.
	MaxMB int `yaml:"max_mb"`

	// TimeoutSeconds bounds a whole download.
	TimeoutSeconds int `yaml:"timeout_seconds"`

	// AllowHTTP permits plaintext http:// URLs. A dataset fetched in the clear
	// can be replaced in transit, so this is off unless an operator opts in.
	AllowHTTP bool `yaml:"allow_http"`

	// AllowPrivate permits private, loopback and link-local addresses. Off by
	// default: leaving it on would let any renter use this node as a proxy into
	// the provider's LAN and cloud metadata service.
	AllowPrivate bool `yaml:"allow_private"`

	// HostAllowlist, when non-empty, restricts datasets to these exact hosts.
	HostAllowlist []string `yaml:"host_allowlist"`
}

// Leases is the policy for timed interactive access to this machine.
//
// A lease is the opposite trade-off from a job: the renter's code never leaves
// their machine, and instead they get a shell on the provider's. That means the
// containment story has to be stronger, not weaker — see Egress below and
// runner.leaseContainerRequest.
type Leases struct {
	// Enabled gates the whole feature, including the /v1/leases routes. A node
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

	// MinMinutes and MaxMinutes bound one purchased slice. A slice that is too
	// short spends more on settlement than on compute; one that is too long
	// pins the node's only lease slot for someone who may have walked away.
	MinMinutes int `yaml:"min_minutes"`
	MaxMinutes int `yaml:"max_minutes"`

	// MaxTotalMinutes caps a lease's whole life across extensions, so a node is
	// never leased out indefinitely by a renter who keeps topping it up.
	MaxTotalMinutes int `yaml:"max_total_minutes"`

	// MissedExtensions is retained so configs written before OverrunSeconds
	// existed still load. It no longer decides anything.
	//
	// It used to set the freeze tolerance to missed_extensions x the slice the
	// renter bought, which with the shipped default of 2 meant a 60-minute
	// lease kept the machine for 180 minutes before it was even frozen, and 190
	// before the container died. That is three times the time sold, given away,
	// and it read to a provider as the node ignoring its own expiry. See
	// OverrunSeconds.
	MissedExtensions int `yaml:"missed_extensions"`

	// OverrunSeconds is how far past expires_at a lease keeps running before it
	// is frozen.
	//
	// Small on purpose. Minutes bought should be minutes delivered: a renter who
	// buys fifteen gets fifteen, and the provider's machine comes back. The
	// tolerance exists only to absorb an extension that is paid for but not yet
	// applied — a wallet prompt the renter is mid-way through approving — not to
	// hand out free time.
	//
	// Freezing rather than killing is still deliberate, and unchanged: the
	// container is paused, not destroyed, so a renter mid-task who is slow to
	// pay gets their work back when they extend. GraceMinutes is how long that
	// frozen state survives before the machine is reclaimed for good.
	OverrunSeconds int `yaml:"overrun_seconds"`

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

	// PaymentMode selects how interactive time is paid for.
	//
	// "direct" is the original flow: a forward transfer per slice through the
	// facilitator, no refunds, which is what every existing node does.
	// "escrow" routes payment through the SessionEscrow contract instead, so a
	// renter who stops early gets their unused time back.
	//
	// Added behind a flag rather than replacing direct, per the project's rule
	// about preferring a config flag over changing working behaviour: every
	// tagged milestone has to stay demoable, and `make smoke` exercises direct.
	PaymentMode string `yaml:"payment_mode"`

	// EscrowContractID is the SessionEscrow deployment, as a contract id or EVM
	// address. Public configuration, not a secret.
	EscrowContractID string `yaml:"escrow_contract_id"`

	// PriceTinybarsPerSecond is the rate the contract settles at. Left empty it
	// is derived from PriceTinybarsPerMinute, rounding up.
	//
	// The contract multiplies this by elapsed seconds, so once a session opens
	// this is the only price that matters — which is why the node converts once
	// at quote time and treats the per-second figure as authoritative from then
	// on, rather than converting in two places that could disagree.
	PriceTinybarsPerSecond string `yaml:"price_tinybars_per_second"`

	// SelfSettle lets the node close an expired session itself, through the
	// sidecar, instead of waiting for the renter to do it.
	//
	// Off by default so the baseline story stays "the node needs no key at all".
	// Turning it on grants no power worth worrying about: settle is
	// permissionless at the contract and calling it early only ever pays the
	// caller's own side LESS. What it buys is that a provider still gets paid
	// when a renter force-quits and never calls settle themselves.
	SelfSettle bool `yaml:"self_settle"`
}

// Lease payment modes. See Leases.PaymentMode.
const (
	PaymentDirect = "direct"
	PaymentEscrow = "escrow"
)

// LeaseLimits caps one lease container. Separate from job Limits because an
// interactive session is sized for a person working, not for one batch script:
// more memory, no wall-clock kill (the lease's paid expiry is the clock).
type LeaseLimits struct {
	MemoryMB    int64 `yaml:"memory_mb"`
	CPUCores    int   `yaml:"cpu_cores"`
	PidsLimit   int64 `yaml:"pids_limit"`
	WorkspaceGB int   `yaml:"workspace_gb"`
}

// Egress is the lease container's network policy.
//
// Batch jobs get no network at all. A lease cannot work that way — a renter has
// to be able to `pip install` — so instead of loosening to the job flow's
// blocklist, lease containers sit on an internal Docker network with no route
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
	// Mode is "named", "quick" or "off".
	//
	//	named  a Cloudflare named tunnel, provisioned for this node by the
	//	       registry using the platform's Cloudflare account. SSH and Jupyter
	//	       both work, on stable hostnames.
	//	quick  `cloudflared tunnel --url`, which needs no Cloudflare account at
	//	       all but gives an HTTP-only, random, per-lease hostname. Jupyter
	//	       works; SSH does not.
	//	off    no tunnel. Only useful when the node already has a public address
	//	       or is being tested on a LAN.
	Mode string `yaml:"mode"`

	// Binary is the cloudflared executable. Looked up on PATH by default.
	Binary string `yaml:"binary"`

	// Token is the named tunnel's credential, fetched once from the registry
	// and cached here. It authorizes running one tunnel and nothing else — it
	// is not a Cloudflare API key, and it cannot move funds.
	Token string `yaml:"token"`

	// SSHHostname, JupyterHostname and APIHostname are what the registry
	// created DNS routes for. Empty until the first successful token fetch.
	SSHHostname     string `yaml:"ssh_hostname"`
	JupyterHostname string `yaml:"jupyter_hostname"`
	APIHostname     string `yaml:"api_hostname"`

	// DerivePublicURL replaces a provider-supplied public_url with the tunnel's
	// own API hostname. A provider who cannot be reached for SSH without a
	// tunnel usually cannot be reached for the x402 API either, so this exists
	// — but it is off by default, because it silently overrides something the
	// provider typed in (implementation.md §8.5).
	DerivePublicURL bool `yaml:"derive_public_url"`

	// ConfigDir holds the generated cloudflared config and the node's tunnel
	// state. Defaults to a directory beside config.yaml.
	ConfigDir string `yaml:"config_dir"`
}

// Limits are the per-job resource caps enforced by the runner.
type Limits struct {
	MaxSeconds    int   `yaml:"max_seconds"`
	MemoryMB      int64 `yaml:"memory_mb"`
	CPUCores      int   `yaml:"cpu_cores"`
	MaxArtifactMB int64 `yaml:"max_artifact_mb"`
	PidsLimit     int64 `yaml:"pids_limit"`
}

// Default returns a config that runs safely out of the box: CPU mode, a short
// allowlist, and conservative caps.
func Default() Config {
	return Config{
		PriceTinybars:     "100000", // 0.001 HBAR
		Asset:             "0.0.0",
		Network:           "hedera:testnet",
		FacilitatorURL:    "https://api.testnet.blocky402.com",
		MaxTimeoutSeconds: 300,
		ListenAddr:        "0.0.0.0:8402",
		GPUEnabled:        false,
		ImageAllowlist: []string{
			"python:3.11-slim",
			"pytorch/pytorch:2.4.1-cuda12.1-cudnn9-runtime",
		},
		Dataset: Dataset{
			MaxMB:          8192,
			TimeoutSeconds: 1800,
		},
		Leases: DefaultLeases(),
		Hedera: DefaultHedera(),
		Limits: Limits{
			MaxSeconds:    900,
			MemoryMB:      4096,
			CPUCores:      2,
			MaxArtifactMB: 512,
			PidsLimit:     512,
		},
		ReceiptsPath: "receipts.jsonl",
		DockerHost:   "unix:///var/run/docker.sock",
		CORS:         DefaultCORS(),
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
		MissedExtensions:       2,
		OverrunSeconds:         30,
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
		// Direct, not escrow: escrow needs a deployed contract and a funded
		// operator key, neither of which a node has until someone opts in.
		PaymentMode: PaymentDirect,
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

// Tunnel modes. See Tunnel.Mode.
const (
	TunnelNamed = "named"
	TunnelQuick = "quick"
	TunnelOff   = "off"
)

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
	// A config written before dataset support existed has a zeroed block; treat
	// that as "unset" rather than "no downloads allowed and no timeout".
	if cfg.Dataset.MaxMB <= 0 {
		cfg.Dataset.MaxMB = Default().Dataset.MaxMB
	}
	if cfg.Dataset.TimeoutSeconds <= 0 {
		cfg.Dataset.TimeoutSeconds = Default().Dataset.TimeoutSeconds
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
	if l.MissedExtensions <= 0 {
		l.MissedExtensions = fallback.MissedExtensions
	}
	// A config written before this existed has it at zero, which would freeze a
	// renter the instant their clock ran out with no room for an in-flight
	// extension. Treat absent as unset, not as "no tolerance at all".
	if l.OverrunSeconds <= 0 {
		l.OverrunSeconds = fallback.OverrunSeconds
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
	if l.Tunnel.ConfigDir == "" {
		l.Tunnel.ConfigDir = "cloudflared"
	}
	if l.PaymentMode == "" {
		l.PaymentMode = fallback.PaymentMode
	}

	if configDir == "" {
		configDir = "."
	}
	l.CAKeyPath = resolveAgainst(configDir, l.CAKeyPath)
	l.Tunnel.ConfigDir = resolveAgainst(configDir, l.Tunnel.ConfigDir)
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
	if _, err := strconv.ParseUint(c.PriceTinybars, 10, 64); err != nil {
		return fmt.Errorf("price_tinybars %q must be a whole number of tinybars, as a string", c.PriceTinybars)
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
	if len(c.ImageAllowlist) == 0 {
		return errors.New("image_allowlist is empty — the node would refuse every job")
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
	if c.Limits.MaxSeconds <= 0 || c.Limits.MemoryMB <= 0 || c.Limits.CPUCores <= 0 {
		return errors.New("limits.max_seconds, limits.memory_mb and limits.cpu_cores must all be positive")
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
	// Escrow verification is a mirror-node read and self-settle is a sidecar
	// call, so escrow mode cannot work without the hedera block turned on.
	// Catching it here means a provider finds out at startup rather than when a
	// renter's deposit cannot be checked.
	if c.Leases.Enabled && c.Leases.PaymentMode == PaymentEscrow && !c.Hedera.Enabled {
		return errors.New("leases.payment_mode is escrow, which needs hedera.enabled — re-run `cleargate-node setup --enable-escrow`")
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
		return errors.New("leases.max_total_minutes must be at least leases.max_minutes, or the first slice could not be bought")
	}
	if l.CAKeyPath == "" {
		return errors.New("leases.ca_key_path must be set — it is where this node's SSH certificate authority lives")
	}
	switch l.Tunnel.Mode {
	case TunnelNamed, TunnelQuick, TunnelOff:
	default:
		return fmt.Errorf("leases.tunnel.mode %q must be one of %q, %q or %q",
			l.Tunnel.Mode, TunnelNamed, TunnelQuick, TunnelOff)
	}
	if l.Egress.ProxyImage == "" {
		return errors.New("leases.egress.proxy_image must be set — lease containers reach the network only through it")
	}
	switch l.PaymentMode {
	case PaymentDirect:
	case PaymentEscrow:
		// Escrow mode with no contract would 402 every renter into depositing
		// nowhere, so this is refused at load rather than at the first sale.
		if l.EscrowContractID == "" {
			return errors.New("leases.payment_mode is escrow, so leases.escrow_contract_id must name the deployed SessionEscrow")
		}
	default:
		return fmt.Errorf("leases.payment_mode %q must be %q or %q", l.PaymentMode, PaymentDirect, PaymentEscrow)
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

// AllowsImage reports whether the node is willing to run an image. The match is
// exact: no prefixes, no wildcards, no "latest" resolution.
func (c Config) AllowsImage(image string) bool {
	for _, allowed := range c.ImageAllowlist {
		if allowed == image {
			return true
		}
	}
	return false
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
