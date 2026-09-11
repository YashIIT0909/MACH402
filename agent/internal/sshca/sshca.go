// Package sshca is the node's own SSH certificate authority.
//
// It is what makes timed interactive access work without any long-lived
// credential changing hands. The renter generates a keypair on their machine
// and sends only the public half; this package signs it into a certificate that
// is valid for one lease, on one principal, until that lease's paid time runs
// out. Nothing has to be revoked in the normal case — the certificate simply
// stops being valid.
//
// Two custody rules hold here, and they mirror the node never holding a Hedera
// key (CLAUDE.md invariant 1):
//
//   - the CA private key is generated on this machine and never leaves it;
//   - the renter's private key is generated on their machine and never reaches
//     this one.
//
// Signing shells out to ssh-keygen rather than reimplementing the certificate
// format with golang.org/x/crypto/ssh. The format is easy to get subtly wrong,
// ssh-keygen is what sshd's own test suite exercises, and it is already a hard
// dependency of anything running sshd.
package sshca

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// CA is a node-local certificate authority backed by a keypair on disk.
type CA struct {
	// keyPath is the private half. Mode 0600, never copied, never logged.
	keyPath string
	// publicKey is the authorized line the container's sshd trusts via
	// TrustedUserCAKeys. Only this half is ever sent anywhere.
	publicKey string
}

// Ensure loads the CA at keyPath, generating it if it is not there yet.
//
// Generation is idempotent and happens once, at `setup --enable-leases`. If the
// key were regenerated on every start, every certificate already issued would
// stop working mid-lease.
func Ensure(ctx context.Context, keyPath, comment string) (*CA, error) {
	if keyPath == "" {
		return nil, errors.New("no CA key path configured")
	}
	if err := os.MkdirAll(filepath.Dir(keyPath), 0o700); err != nil {
		return nil, fmt.Errorf("create CA directory: %w", err)
	}

	if _, err := os.Stat(keyPath); errors.Is(err, os.ErrNotExist) {
		// -N "" leaves the key unencrypted: the node has to be able to sign
		// without a human present, so a passphrase it would have to store
		// beside the key buys nothing.
		cmd := exec.CommandContext(ctx, "ssh-keygen",
			"-q", "-t", "ed25519", "-f", keyPath, "-N", "", "-C", comment)
		if output, err := cmd.CombinedOutput(); err != nil {
			return nil, fmt.Errorf("generate SSH CA: %w: %s", err, strings.TrimSpace(string(output)))
		}
		if err := os.Chmod(keyPath, 0o600); err != nil {
			return nil, fmt.Errorf("secure CA key: %w", err)
		}
	} else if err != nil {
		return nil, fmt.Errorf("stat CA key: %w", err)
	}

	pub, err := os.ReadFile(keyPath + ".pub")
	if err != nil {
		return nil, fmt.Errorf("read CA public key: %w — delete %s and re-run setup to regenerate the pair", err, keyPath)
	}

	return &CA{keyPath: keyPath, publicKey: strings.TrimSpace(string(pub))}, nil
}

// PublicKey is the CA's public half, in authorized_keys form. This is what goes
// into a lease container as TrustedUserCAKeys, and it is the only part of the
// CA that ever leaves this process.
func (c *CA) PublicKey() string { return c.publicKey }

// Sign mints a certificate for one lease.
//
// The scoping is the whole point:
//
//   - identity is the lease id, so a certificate can be traced to what paid for it;
//   - principal is the lease id too, and the container's sshd is configured to
//     accept only that principal, so a certificate minted for one lease cannot
//     open a shell on another;
//   - validity ends when the lease's paid time does. Extending a lease re-signs
//     a fresh certificate with a later expiry rather than mutating this one, so
//     expiry is the primary revocation mechanism and nothing has to be actively
//     revoked in the normal case.
//
// A force-stopped lease needs no revocation either: the container is killed, so
// there is nothing left for a still-valid certificate to authenticate against.
// That is why there is no key revocation list here.
func (c *CA) Sign(ctx context.Context, leaseID, publicKey string, validUntil time.Time) (string, error) {
	if err := ValidatePublicKey(publicKey); err != nil {
		return "", err
	}

	// ssh-keygen writes the certificate beside the public key it was given, so
	// the input needs a directory of its own that goes away afterwards.
	dir, err := os.MkdirTemp("", "cleargate-cert-")
	if err != nil {
		return "", fmt.Errorf("create signing directory: %w", err)
	}
	defer os.RemoveAll(dir)

	keyFile := filepath.Join(dir, "id.pub")
	if err := os.WriteFile(keyFile, []byte(publicKey+"\n"), 0o600); err != nil {
		return "", fmt.Errorf("stage renter public key: %w", err)
	}

	// A whole-minute window, always at least a minute: ssh-keygen's -V takes
	// minute granularity, and a zero-length window would mint a certificate
	// that is already expired.
	minutes := int(time.Until(validUntil).Round(time.Minute) / time.Minute)
	if minutes < 1 {
		minutes = 1
	}

	cmd := exec.CommandContext(ctx, "ssh-keygen",
		"-q",
		"-s", c.keyPath,
		"-I", leaseID,
		"-n", leaseID,
		"-V", fmt.Sprintf("-1m:+%dm", minutes),
		keyFile,
	)
	if output, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("sign certificate: %w: %s", err, strings.TrimSpace(string(output)))
	}

	cert, err := os.ReadFile(filepath.Join(dir, "id-cert.pub"))
	if err != nil {
		return "", fmt.Errorf("read signed certificate: %w", err)
	}
	return strings.TrimSpace(string(cert)), nil
}

// keyTypes are the public key algorithms a renter may present. Everything here
// is either modern or still widely deployed; ssh-dss is deliberately absent.
var keyTypes = []string{
	"ssh-ed25519",
	"ecdsa-sha2-nistp256",
	"ecdsa-sha2-nistp384",
	"ecdsa-sha2-nistp521",
	"sk-ssh-ed25519@openssh.com",
	"sk-ecdsa-sha2-nistp256@openssh.com",
	"ssh-rsa",
}

// maxPublicKeyBytes bounds a submitted key. A 4096-bit RSA key in base64 is
// well under a kilobyte; anything past this is a mistake or an attack.
const maxPublicKeyBytes = 4096

// ValidatePublicKey checks a renter-submitted public key before it is written
// to disk and handed to ssh-keygen.
//
// This is a trust boundary: the string arrives in the body of an unauthenticated
// POST, and it ends up as an argument-adjacent file for a subprocess. A newline
// would let a caller smuggle a second key into the file, and an unrecognized
// algorithm should be refused here rather than at signing time with an opaque
// ssh-keygen error the renter cannot act on.
func ValidatePublicKey(key string) error {
	key = strings.TrimSpace(key)
	if key == "" {
		return errors.New("public_key is required — generate one with `ssh-keygen -t ed25519`")
	}
	if len(key) > maxPublicKeyBytes {
		return fmt.Errorf("public_key is longer than %d bytes", maxPublicKeyBytes)
	}
	if strings.ContainsAny(key, "\n\r\x00") {
		return errors.New("public_key must be a single line — send only the contents of your .pub file")
	}
	if strings.HasPrefix(key, "-----BEGIN") {
		return errors.New("that looks like a private key. Send the public half — the .pub file — and nothing else")
	}

	fields := strings.Fields(key)
	if len(fields) < 2 {
		return errors.New("public_key must look like \"<type> <base64> [comment]\"")
	}
	for _, allowed := range keyTypes {
		if fields[0] == allowed {
			return nil
		}
	}
	return fmt.Errorf("public key type %q is not accepted; use one of %s",
		fields[0], strings.Join(keyTypes, ", "))
}
