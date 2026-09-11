package sshca

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A renter-submitted public key is an unauthenticated string that ends up in a
// file handed to a subprocess. Everything refused here is refused before it
// gets anywhere near ssh-keygen.
func TestValidatePublicKeyRejectsHostileInput(t *testing.T) {
	cases := map[string]string{
		"empty":               "",
		"private key":         "-----BEGIN OPENSSH PRIVATE KEY-----\nabc\n-----END",
		"smuggled second key": "ssh-ed25519 AAAAC3Nz first\nssh-ed25519 AAAAC3Nz second",
		"carriage return":     "ssh-ed25519 AAAAC3Nz\rmore",
		"null byte":           "ssh-ed25519 AAAA\x00C3Nz",
		"no key material":     "ssh-ed25519",
		"weak algorithm":      "ssh-dss AAAAB3NzaC1kc3M",
		"unknown algorithm":   "totally-made-up AAAAC3Nz",
		"absurdly long":       "ssh-ed25519 " + strings.Repeat("A", maxPublicKeyBytes),
	}
	for name, key := range cases {
		if err := ValidatePublicKey(key); err == nil {
			t.Errorf("%s should have been rejected", name)
		}
	}
}

func TestValidatePublicKeyAcceptsRealKeys(t *testing.T) {
	cases := []string{
		"ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIKk renter@laptop",
		"ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIKk",
		"ecdsa-sha2-nistp256 AAAAE2VjZHNhLXNoYTItbmlzdHAyNTY= renter@laptop",
		"sk-ssh-ed25519@openssh.com AAAAGnNrLXNzaC1lZDI1NTE5 yubikey",
		"  ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIKk  ",
	}
	for _, key := range cases {
		if err := ValidatePublicKey(key); err != nil {
			t.Errorf("%q should have been accepted: %v", key, err)
		}
	}
}

// The end-to-end custody claim: a CA generated locally signs a key it was only
// ever given the public half of, and the certificate that comes out is scoped
// to one lease and expires on its own.
func TestSignScopesTheCertificateToOneLease(t *testing.T) {
	requireSSHKeygen(t)

	dir := t.TempDir()
	ca, err := Ensure(context.Background(), filepath.Join(dir, "ca"), "cleargate-test")
	if err != nil {
		t.Fatalf("generate CA: %v", err)
	}
	if !strings.HasPrefix(ca.PublicKey(), "ssh-ed25519 ") {
		t.Fatalf("CA public key looks wrong: %q", ca.PublicKey())
	}

	// The renter's key, generated on "their" machine. The CA only ever sees the
	// public half below.
	renterKey := filepath.Join(dir, "renter")
	run(t, "ssh-keygen", "-q", "-t", "ed25519", "-f", renterKey, "-N", "", "-C", "renter")
	renterPub := strings.TrimSpace(readFile(t, renterKey+".pub"))

	cert, err := ca.Sign(context.Background(), "leasedeadbeef", renterPub, time.Now().Add(30*time.Minute))
	if err != nil {
		t.Fatalf("sign: %v", err)
	}

	certPath := filepath.Join(dir, "issued-cert.pub")
	writeFile(t, certPath, cert+"\n")
	details := run(t, "ssh-keygen", "-L", "-f", certPath)

	// The principal is what stops a certificate minted for one lease opening a
	// shell on another, so it is the assertion that matters most here.
	if !strings.Contains(details, "leasedeadbeef") {
		t.Fatalf("certificate should be scoped to the lease principal:\n%s", details)
	}
	if strings.Contains(details, "Valid: forever") {
		t.Fatalf("certificate must expire — expiry is the revocation mechanism:\n%s", details)
	}
}

// Regenerating the CA would invalidate every certificate already issued, so a
// second Ensure has to adopt the existing pair rather than replace it.
func TestEnsureIsIdempotent(t *testing.T) {
	requireSSHKeygen(t)

	path := filepath.Join(t.TempDir(), "ca")
	first, err := Ensure(context.Background(), path, "cleargate-test")
	if err != nil {
		t.Fatalf("first Ensure: %v", err)
	}
	second, err := Ensure(context.Background(), path, "cleargate-test")
	if err != nil {
		t.Fatalf("second Ensure: %v", err)
	}
	if first.PublicKey() != second.PublicKey() {
		t.Fatal("Ensure regenerated the CA, which would invalidate every certificate already issued")
	}
}

func requireSSHKeygen(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("ssh-keygen"); err != nil {
		t.Skip("ssh-keygen is not installed")
	}
}

func run(t *testing.T, name string, args ...string) string {
	t.Helper()
	output, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v: %v: %s", name, args, err, output)
	}
	return string(output)
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(raw)
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
