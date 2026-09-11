package main

import "testing"

// The classic way an allowlist leaks: a bare suffix match lets an attacker
// register the difference. "notgithub.com" ends with "github.com".
func TestAllowlistMatchesOnDomainBoundaries(t *testing.T) {
	p := &proxy{allowlist: parseAllowlist("pypi.org, github.com ,huggingface.co")}

	allowed := []string{
		"pypi.org",
		"pypi.org:443",
		"github.com",
		"codeload.github.com",
		"raw.githubusercontent.com.github.com",
		"PyPI.ORG",
		"huggingface.co.",
	}
	for _, host := range allowed {
		if !p.allows(host) {
			t.Errorf("%q should have been allowed", host)
		}
	}

	refused := []string{
		"notgithub.com",
		"github.com.evil.test",
		"evilgithub.com:443",
		"pypi.org.attacker.test",
		"169.254.169.254",
		"localhost",
		"",
		"xpypi.org",
	}
	for _, host := range refused {
		if p.allows(host) {
			t.Errorf("%q should have been refused", host)
		}
	}
}

// An empty allowlist is a lease with no network, which is a legitimate — if
// unfriendly — configuration. It must not fail open.
func TestEmptyAllowlistRefusesEverything(t *testing.T) {
	p := &proxy{allowlist: parseAllowlist("")}
	for _, host := range []string{"pypi.org", "github.com", "example.test"} {
		if p.allows(host) {
			t.Errorf("an empty allowlist must refuse %q", host)
		}
	}
}
