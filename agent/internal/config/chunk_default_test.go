package config

import "testing"

// A node that names no chunk size sells a whole session in one payment.
func TestChunkDefaultsToTheLongestSessionSold(t *testing.T) {
	leases := Leases{Enabled: true, MaxMinutes: 120}
	leases.applyDefaults("/etc/mach402")

	if leases.SessionChunkSeconds != 120*60 {
		t.Fatalf("chunk is %ds, want the whole %d-minute maximum", leases.SessionChunkSeconds, leases.MaxMinutes)
	}
	if leases.LowCreditThresholdSeconds >= leases.SessionChunkSeconds {
		t.Fatalf("low-credit runway %ds must stay under the chunk", leases.LowCreditThresholdSeconds)
	}
}

// A provider who wants the narrower exposure still gets exactly what they set.
func TestAnExplicitChunkIsLeftAlone(t *testing.T) {
	leases := Leases{Enabled: true, MaxMinutes: 120, SessionChunkSeconds: 300}
	leases.applyDefaults("/etc/mach402")

	if leases.SessionChunkSeconds != 300 {
		t.Fatalf("chunk is %ds, want the 300s the provider asked for", leases.SessionChunkSeconds)
	}
}
