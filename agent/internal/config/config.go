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

	// Limits cap what one job may consume.
	Limits Limits `yaml:"limits"`

	// Dataset governs downloading renter-supplied dataset URLs.
	Dataset Dataset `yaml:"dataset"`

	// ReceiptsPath is the append-only local earnings log.
	ReceiptsPath string `yaml:"receipts_path"`

	// DockerHost is the Docker endpoint. Unix socket by default.
	DockerHost string `yaml:"docker_host"`
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
		Limits: Limits{
			MaxSeconds:    900,
			MemoryMB:      4096,
			CPUCores:      2,
			MaxArtifactMB: 512,
			PidsLimit:     512,
		},
		ReceiptsPath: "receipts.jsonl",
		DockerHost:   "unix:///var/run/docker.sock",
	}
}

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
	if err := cfg.Validate(); err != nil {
		return Config{}, fmt.Errorf("invalid config %s: %w", path, err)
	}
	return cfg, nil
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
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

// Validate rejects configurations that would produce an unpayable challenge or
// an unsafe container.
func (c Config) Validate() error {
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
