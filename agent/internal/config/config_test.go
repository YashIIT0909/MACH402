package config

import (
	"path/filepath"
	"strings"
	"testing"
)

func validConfig() Config {
	cfg := Default()
	cfg.NodeID = "node_test"
	cfg.PayTo = "0.0.8011510"
	return cfg
}

func TestDefaultConfigIsValidOncePayToIsSet(t *testing.T) {
	if err := validConfig().Validate(); err != nil {
		t.Fatalf("the shipped defaults should be valid: %v", err)
	}
}

func TestValidateRejectsBadPayTo(t *testing.T) {
	for _, payTo := range []string{"", "not-an-account", "0.0", "0.0.x", "0.0.1.2"} {
		cfg := validConfig()
		cfg.PayTo = payTo
		if err := cfg.Validate(); err == nil {
			t.Fatalf("pay_to %q should have been rejected", payTo)
		}
	}
}

// Prices are strings in tinybars end to end. A float here would silently lose
// precision on real amounts, so anything non-integral must be refused.
func TestValidateRejectsNonIntegerPrice(t *testing.T) {
	for _, price := range []string{"0.001", "1e5", "-100", "", "abc"} {
		cfg := validConfig()
		cfg.PriceTinybars = price
		if err := cfg.Validate(); err == nil {
			t.Fatalf("price_tinybars %q should have been rejected", price)
		}
	}
}

func TestValidateRejectsEmptyAllowlist(t *testing.T) {
	cfg := validConfig()
	cfg.ImageAllowlist = nil
	err := cfg.Validate()
	if err == nil {
		t.Fatal("an empty allowlist should be rejected")
	}
	if !strings.Contains(err.Error(), "allowlist") {
		t.Fatalf("error should name the allowlist, got: %v", err)
	}
}

// A node with no registry_url is unlisted, not misconfigured: renters who know
// its URL still pay it directly.
func TestValidateAllowsAnUnlistedNode(t *testing.T) {
	cfg := validConfig()
	cfg.RegistryURL = ""
	cfg.PublicURL = ""
	if err := cfg.Validate(); err != nil {
		t.Fatalf("an unlisted node should be valid: %v", err)
	}
}

// Publishing a listing renters cannot dial is worse than publishing none.
func TestValidateRejectsListingWithoutAnAddressOrToken(t *testing.T) {
	cfg := validConfig()
	cfg.RegistryURL = "http://localhost:4400"
	cfg.RegistryToken = "token"
	cfg.PublicURL = ""
	err := cfg.Validate()
	if err == nil {
		t.Fatal("registry_url without public_url should be rejected")
	}
	if !strings.Contains(err.Error(), "public_url") {
		t.Fatalf("error should name public_url, got: %v", err)
	}

	cfg.PublicURL = "http://localhost:8402"
	cfg.RegistryToken = ""
	if err := cfg.Validate(); err == nil {
		t.Fatal("registry_url without registry_token should be rejected")
	}

	cfg.RegistryToken = "token"
	if err := cfg.Validate(); err != nil {
		t.Fatalf("a fully configured listing should be valid: %v", err)
	}
}

func TestAllowsImageMatchesExactly(t *testing.T) {
	cfg := validConfig()
	cfg.ImageAllowlist = []string{"python:3.11-slim"}

	if !cfg.AllowsImage("python:3.11-slim") {
		t.Fatal("an allowlisted image should be allowed")
	}
	// No prefix or tag fuzziness: "python" must not open the door to
	// "python:latest", and a lookalike registry must not match.
	for _, image := range []string{"python", "python:latest", "evil/python:3.11-slim", "python:3.11-slim-extra"} {
		if cfg.AllowsImage(image) {
			t.Fatalf("image %q should not have matched the allowlist", image)
		}
	}
}

func TestSaveAndLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "config.yaml")
	original := validConfig()
	original.PriceTinybars = "250000"
	original.GPUEnabled = true

	if err := Save(path, original); err != nil {
		t.Fatalf("save: %v", err)
	}

	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if loaded.PayTo != original.PayTo || loaded.PriceTinybars != original.PriceTinybars {
		t.Fatalf("round trip lost data: %+v", loaded)
	}
	if !loaded.GPUEnabled {
		t.Fatal("gpu_enabled did not survive the round trip")
	}
}

// A partial config must still start: an operator editing one field should not
// have to restate every default.
func TestLoadFillsInDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	minimal := "node_id: node_minimal\npay_to: \"0.0.4242\"\n"
	if err := writeFile(path, minimal); err != nil {
		t.Fatalf("write: %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.FacilitatorURL != Default().FacilitatorURL {
		t.Fatalf("facilitator_url should have defaulted, got %q", cfg.FacilitatorURL)
	}
	if cfg.Limits.MaxSeconds != Default().Limits.MaxSeconds {
		t.Fatalf("limits should have defaulted, got %+v", cfg.Limits)
	}
}

func TestLoadMissingFileExplainsSetup(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), "absent.yaml"))
	if err == nil {
		t.Fatal("expected an error for a missing config")
	}
	if !strings.Contains(err.Error(), "setup") {
		t.Fatalf("the error should point at `cleargate-node setup`, got: %v", err)
	}
}

// Leasing is opt-in per provider. A node that never asked for it must not be
// told its lease price is malformed — the whole block is inert until Enabled.
func TestValidateIgnoresLeasingWhenItIsOff(t *testing.T) {
	cfg := validConfig()
	cfg.Leases.Enabled = false
	cfg.Leases.PriceTinybarsPerMinute = "not a number"
	cfg.Leases.Image = ""
	if err := cfg.Validate(); err != nil {
		t.Fatalf("a node with leasing off should not be validated against leasing rules: %v", err)
	}
}

func TestValidateRejectsNonIntegerLeasePrice(t *testing.T) {
	for _, price := range []string{"0.002", "2e5", "-1", "", "abc"} {
		cfg := validConfig()
		cfg.Leases.Enabled = true
		cfg.Leases.PriceTinybarsPerMinute = price
		if err := cfg.Validate(); err == nil {
			t.Fatalf("lease price %q should have been rejected", price)
		}
	}
}

func TestValidateRejectsImpossibleLeaseWindows(t *testing.T) {
	cases := map[string]func(*Leases){
		"max below min":       func(l *Leases) { l.MinMinutes, l.MaxMinutes = 60, 30 },
		"zero minimum":        func(l *Leases) { l.MinMinutes = 0 },
		"total below a slice": func(l *Leases) { l.MaxMinutes, l.MaxTotalMinutes = 120, 60 },
		"unknown tunnel mode": func(l *Leases) { l.Tunnel.Mode = "carrier-pigeon" },
		"no egress proxy":     func(l *Leases) { l.Egress.ProxyImage = "" },
	}
	for name, break_ := range cases {
		cfg := validConfig()
		cfg.Leases.Enabled = true
		break_(&cfg.Leases)
		if err := cfg.Validate(); err == nil {
			t.Fatalf("%s should have been rejected", name)
		}
	}
}

// A zero in a hand-written leases block is a broken setting, not a strict one:
// max_minutes 0 refuses every lease, and grace_minutes 0 reaps a frozen
// container the instant it freezes.
func TestLeaseDefaultsFillInZeroedFields(t *testing.T) {
	leases := Leases{Enabled: true}
	leases.applyDefaults("/etc/cleargate")

	fallback := DefaultLeases()
	if leases.MaxMinutes != fallback.MaxMinutes || leases.GraceMinutes != fallback.GraceMinutes {
		t.Fatalf("zeroed windows should have been filled in, got %+v", leases)
	}
	if leases.Image == "" || leases.Egress.ProxyImage == "" || len(leases.Egress.Allowlist) == 0 {
		t.Fatalf("zeroed images and allowlist should have been filled in, got %+v", leases)
	}
}

// Lease state has to be found relative to config.yaml, not to whatever
// directory the node happened to be started from — a node that regenerated its
// CA on a restart would invalidate every certificate it had already issued.
func TestLeasePathsResolveAgainstTheConfigDirectory(t *testing.T) {
	leases := Leases{Enabled: true}
	leases.applyDefaults("/etc/cleargate")

	if !filepath.IsAbs(leases.CAKeyPath) || !strings.HasPrefix(leases.CAKeyPath, "/etc/cleargate") {
		t.Fatalf("ca_key_path should have resolved under the config directory, got %q", leases.CAKeyPath)
	}
	if !strings.HasPrefix(leases.Tunnel.ConfigDir, "/etc/cleargate") {
		t.Fatalf("tunnel config dir should have resolved under the config directory, got %q", leases.Tunnel.ConfigDir)
	}

	// An operator who gave an absolute path meant it.
	absolute := Leases{Enabled: true, CAKeyPath: "/var/lib/cleargate/ca"}
	absolute.applyDefaults("/etc/cleargate")
	if absolute.CAKeyPath != "/var/lib/cleargate/ca" {
		t.Fatalf("an absolute ca_key_path should be left alone, got %q", absolute.CAKeyPath)
	}
}
