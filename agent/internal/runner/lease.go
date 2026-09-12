package runner

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/YashIIT0909/ClearGate/agent/internal/sshca"
)

// A lease is the inverse of a job.
//
// A job sends the renter's code to the provider's machine and runs it in a box
// with no network at all. A lease sends nothing: the renter gets a shell and a
// Jupyter server on the provider's GPU for the time they paid for, and their
// code and data never leave their own machine.
//
// That inversion moves the risk. Nothing untrusted is uploaded, but somebody
// the provider has never met has an interactive session on their hardware and
// their IP address. So the containment here is stricter than the job sandbox
// in the places that matter for a live shell — see leaseContainerRequest and
// the egress proxy — rather than looser.
const (
	// leaseLabel marks every container, volume and network a lease creates, so
	// the startup sweep can find orphans from a crash without touching anything
	// else on the provider's box.
	leaseLabel = "cleargate.lease"

	// workspaceDir is the lease's writable home. It survives a pause and is
	// destroyed at reap.
	workspaceDir = "/workspace"

	// sshPort and jupyterPort are what the lease image listens on inside its
	// own network namespace. They are never published to the host.
	sshPort     = 22
	jupyterPort = 8888

	// egressPort is where the deny-by-default forward proxy listens. The lease
	// container's HTTP_PROXY points here, and this is the only path off the
	// internal network.
	egressPort = 3128
)

// LeaseStatus is a point in a lease's life.
//
//	provisioning -> active -> paused -> active
//	                      \        \-> expired
//	                       \-> stopped | failed
//
// paused is the freeze that happens when paid time lapses: the container's
// processes are frozen rather than killed, so a renter who is mid-task and slow
// to pay does not lose their work outright.
type LeaseStatus string

const (
	LeaseProvisioning LeaseStatus = "provisioning"
	LeaseActive       LeaseStatus = "active"
	LeasePaused       LeaseStatus = "paused"
	LeaseStopped      LeaseStatus = "stopped"
	LeaseExpired      LeaseStatus = "expired"
	LeaseFailed       LeaseStatus = "failed"
)

// IsTerminal reports whether a lease is over and its resources are gone.
func (s LeaseStatus) IsTerminal() bool {
	switch s {
	case LeaseStopped, LeaseExpired, LeaseFailed:
		return true
	default:
		return false
	}
}

// LeaseSpec is what a renter POSTs to /v1/leases.
type LeaseSpec struct {
	// Minutes is the first slice of time being bought. Extensions buy more.
	Minutes int `json:"minutes"`

	// PublicKey is the renter's own SSH public key, generated on their machine.
	// Only the public half ever travels: the node signs it into a certificate
	// scoped to this one lease and never sees the private half, exactly as the
	// node never holds a Hedera key (CLAUDE.md invariant 1).
	PublicKey string `json:"public_key"`

	// RequireGPU refuses a CPU-fallback node before any payment is asked for.
	RequireGPU bool `json:"require_gpu,omitempty"`
}

// Lease is one timed interactive session and its live state.
type Lease struct {
	ID string

	mu           sync.RWMutex
	status       LeaseStatus
	token        string
	containerID  string
	proxyID      string
	network      string
	volume       string
	containerIP  string
	jupyterToken string
	// publicKey is the renter's own SSH public key. It is kept so an extension
	// can re-sign against the same identity if the renter does not send a new
	// one; the private half was never here to keep.
	publicKey    string
	gpu          bool
	createdAt    time.Time
	expiresAt    time.Time
	sliceMinutes int
	paidMinutes  int
	pausedAt     *time.Time
	err          string

	// Escrow fields, set only on a lease sold as a session
	// (leases.payment_mode: escrow). A session IS a lease — same container,
	// same certificate, same freeze-and-reap — differing only in how it was
	// paid for and therefore in what happens when it ends.
	//
	// sessionID is the contract's key; empty on a direct-paid lease, which is
	// how the rest of the code tells the two apart.
	sessionID string
	// depositTx is the renter's openSession transaction, kept for the receipt
	// and the audit trail.
	depositTx string
	// settleState tracks whether the on-chain split has happened yet. A reaped
	// session that is still unsettled is money sitting in the contract that
	// belongs to someone, so the sweep can act on it.
	settleState SettleState
	// paidSeconds is the duration the CONTRACT says was bought, which is the
	// only authority on it. Kept in seconds rather than derived from paidMinutes
	// because a top-up is compared against it exactly: rounding to minutes would
	// make a legitimate top-up look like no change at all.
	paidSeconds int64
}

// SettleState is where an escrow session stands with the contract.
type SettleState string

const (
	// SettleNotApplicable is a direct-paid lease: settled at the facilitator
	// the moment it started, with nothing on-chain left to close.
	SettleNotApplicable SettleState = ""
	// SettlePending means the session is over and the contract still holds the
	// deposit. Either party may close it; `settle` is permissionless.
	SettlePending SettleState = "pending"
	// SettleDone means the split has been executed on-chain.
	SettleDone SettleState = "done"
)

// LeaseState is the read-only view returned by GET /v1/leases/:id.
//
// The renter's CLI polls this to decide when to buy another slice, so
// seconds_remaining is the field that actually drives auto-extension.
type LeaseState struct {
	LeaseID          string      `json:"lease_id"`
	Status           LeaseStatus `json:"status"`
	CreatedAt        string      `json:"created_at"`
	ExpiresAt        string      `json:"expires_at"`
	SecondsRemaining int64       `json:"seconds_remaining"`
	PaidMinutes      int         `json:"paid_minutes"`
	GPU              bool        `json:"gpu"`
	Error            *string     `json:"error"`
}

// State snapshots the lease for an API response.
func (l *Lease) State() LeaseState {
	l.mu.RLock()
	defer l.mu.RUnlock()

	state := LeaseState{
		LeaseID:     l.ID,
		Status:      l.status,
		CreatedAt:   l.createdAt.UTC().Format(time.RFC3339),
		ExpiresAt:   l.expiresAt.UTC().Format(time.RFC3339),
		PaidMinutes: l.paidMinutes,
		GPU:         l.gpu,
	}
	if remaining := time.Until(l.expiresAt); remaining > 0 {
		state.SecondsRemaining = int64(remaining.Seconds())
	}
	if l.err != "" {
		message := l.err
		state.Error = &message
	}
	return state
}

// MarkEscrow records that this lease was paid for through the escrow contract.
//
// Called once, right after the deposit is verified. From here on the lease
// behaves identically to a direct-paid one except at the end, where the
// contract has a split to perform.
func (l *Lease) MarkEscrow(sessionID, depositTx string, expiresAt time.Time, paidSeconds int64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.sessionID = sessionID
	l.depositTx = depositTx
	l.settleState = SettlePending
	l.paidSeconds = paidSeconds
	// The on-chain record is authoritative about when the paid time ends: the
	// contract computes the split from its own startTime, so a node clock that
	// disagrees would freeze a container early or late.
	l.expiresAt = expiresAt
}

// SessionID is the escrow session backing this lease, or "" for a direct-paid
// lease. This is what distinguishes the two everywhere else in the code.
func (l *Lease) SessionID() string {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.sessionID
}

// DepositTransaction is the renter's openSession transaction id.
func (l *Lease) DepositTransaction() string {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.depositTx
}

// SettleState reports whether the on-chain split still needs doing.
func (l *Lease) SettleState() SettleState {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.settleState
}

// MarkSettled records that the contract has performed the split.
//
// Idempotent, and it must be: `settle` is permissionless, so the renter's
// clean-exit call and this node's self-settle can both succeed in either order
// — the contract's one-shot guard sorts that out, and this only records what
// already happened.
func (l *Lease) MarkSettled() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.settleState != SettleNotApplicable {
		l.settleState = SettleDone
	}
}

// ExtendPaidUntil moves a session's expiry after a verified top-up.
//
// Unlike ExtendLease this takes an absolute time rather than minutes, because
// the contract's `startTime + duration` is the authority on when a session ends
// and the node mirrors it rather than recomputing it.
func (l *Lease) ExtendPaidUntil(expiresAt time.Time, paidSeconds int64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.expiresAt = expiresAt
	l.paidSeconds = paidSeconds
	l.paidMinutes = int((paidSeconds + 59) / 60)
}

// PaidSeconds is the duration the contract says was bought, in seconds.
//
// Zero on a direct-paid lease, which has no on-chain duration at all.
func (l *Lease) PaidSeconds() int64 {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.paidSeconds
}

// Status reads the current status.
func (l *Lease) Status() LeaseStatus {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.status
}

// ExpiresAt is when the currently paid slice runs out.
func (l *Lease) ExpiresAt() time.Time {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.expiresAt
}

// JupyterToken is the secret that authorizes a browser or a Colab local runtime
// against this lease's Jupyter server. It is returned to the renter once, at
// purchase.
func (l *Lease) JupyterToken() string {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.jupyterToken
}

// SSHAddr and JupyterAddr are the container's addresses on its own network, for
// the tunnel to point at.
func (l *Lease) SSHAddr() string {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return fmt.Sprintf("%s:%d", l.containerIP, sshPort)
}

func (l *Lease) JupyterAddr() string {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return fmt.Sprintf("%s:%d", l.containerIP, jupyterPort)
}

// PublicKey is the renter's SSH public key, as submitted. Only ever the public
// half — the private key stayed on the renter's machine.
func (l *Lease) PublicKey() string {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.publicKey
}

// AuthorizedBy reports whether a bearer token may act on this lease. Constant
// time, so a caller cannot discover the token one byte at a time.
func (l *Lease) AuthorizedBy(token string) bool {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return constantTimeEqual(l.token, token)
}

func (l *Lease) setStatus(status LeaseStatus) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.status.IsTerminal() {
		return
	}
	l.status = status
}

func (l *Lease) fail(err error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.status.IsTerminal() {
		return
	}
	l.status = LeaseFailed
	if err != nil {
		l.err = err.Error()
	}
}

// LeasesEnabled reports whether this node sells interactive access at all.
func (r *Runner) LeasesEnabled() bool { return r.cfg.Leases.Enabled }

// LeaseImage is the runtime image a lease container runs.
func (r *Runner) LeaseImage() string { return r.cfg.Leases.Image }

// LeaseGPU reports whether a lease container can actually compute on this
// node's GPU.
//
// It is deliberately narrower than GPU(), which describes the *host*. A node can
// have a working card, the nvidia runtime, and a lease image built from a base
// with no CUDA toolkit — in which case a renter gets a container that lists a
// device and cannot run a kernel on it. Both halves have to hold before this
// node sells GPU time interactively.
func (r *Runner) LeaseGPU() bool { return r.gpu.Available && r.leaseImageCUDA }

// detectLeaseImageCUDA decides whether the configured lease image is one a GPU
// can be used from.
//
// It reads the image's declared environment rather than starting a container:
// this runs at boot on every start, and a probe container would add tens of
// seconds to it. The NVIDIA base images all declare these, and an image
// declaring neither has no CUDA runtime to link against.
func (r *Runner) detectLeaseImageCUDA(ctx context.Context) bool {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	env, err := r.docker.ImageEnv(ctx, r.cfg.Leases.Image)
	if err != nil {
		// The image is missing or unreadable. ValidateLeaseSpec reports that
		// properly; assuming "no CUDA" here just means the node does not
		// advertise something it cannot back up.
		r.log.Warn("could not inspect the lease image; assuming it has no CUDA",
			"image", r.cfg.Leases.Image, "error", err)
		return false
	}

	for _, entry := range env {
		name, value, _ := strings.Cut(entry, "=")
		switch name {
		case "CUDA_VERSION":
			return true
		case "NVIDIA_DRIVER_CAPABILITIES":
			// "utility" alone is enough for nvidia-smi and nothing else, which
			// is exactly the trap: the renter sees their card listed and every
			// kernel launch fails.
			if strings.Contains(value, "compute") {
				return true
			}
		}
	}
	return false
}

// GetLease looks up a lease by id.
func (r *Runner) GetLease(id string) (*Lease, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	lease, ok := r.leases[id]
	return lease, ok
}

// ActiveLease returns the lease currently holding this node's single lease
// slot, if any.
//
// V1 runs one lease per node at a time. Concurrent leases would need either
// per-lease published ports and more tunnel ingress rules, or a local reverse
// proxy keyed by lease id, and neither is worth building before the first one
// works (implementation.md §9).
func (r *Runner) ActiveLease() (*Lease, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.activeLease == "" {
		return nil, false
	}
	lease, ok := r.leases[r.activeLease]
	return lease, ok
}

// ListLeases snapshots every lease this node knows about.
func (r *Runner) ListLeases() []LeaseState {
	r.mu.RLock()
	leases := make([]*Lease, 0, len(r.leases))
	for _, lease := range r.leases {
		leases = append(leases, lease)
	}
	r.mu.RUnlock()

	states := make([]LeaseState, 0, len(leases))
	for _, lease := range leases {
		states = append(states, lease.State())
	}
	return states
}

// ValidateLeaseSpec rejects a lease before the renter is asked to pay.
//
// Everything refused here is free: no challenge is issued and nothing reaches
// the chain. The image check matters more for leases than it does for jobs —
// a lease settles only after the container is up and reachable, so there is no
// staging phase to hide a multi-gigabyte pull in. If the image is not already
// on the box, the honest answer is to say so now rather than to make a renter
// wait past their payload's expiry.
func (r *Runner) ValidateLeaseSpec(ctx context.Context, spec LeaseSpec) error {
	leases := r.cfg.Leases
	if !leases.Enabled {
		return errors.New("this node does not offer interactive leases")
	}
	if spec.Minutes < leases.MinMinutes || spec.Minutes > leases.MaxMinutes {
		return fmt.Errorf("minutes must be between %d and %d on this node", leases.MinMinutes, leases.MaxMinutes)
	}
	if err := sshca.ValidatePublicKey(spec.PublicKey); err != nil {
		return err
	}
	if spec.RequireGPU && !r.gpu.Available {
		return fmt.Errorf("this lease requires a GPU, but this node is in CPU-fallback mode (%s)", r.gpu.Reason)
	}
	// The host having a card is not enough. A lease image built from a base with
	// no CUDA runtime produces a container that lists the device and fails every
	// kernel launch, and a renter would only discover that after paying. Refuse
	// it here, where refusing is free.
	if spec.RequireGPU && !r.leaseImageCUDA {
		return fmt.Errorf("this lease requires a GPU, but this node's lease image %q has no CUDA runtime, "+
			"so a container on it cannot compute on the card; the provider needs to rebuild it with `make lease-image`",
			leases.Image)
	}
	if active, ok := r.ActiveLease(); ok && !active.Status().IsTerminal() {
		return errors.New("this node already has an active lease; it rents to one renter at a time")
	}
	if !r.docker.HasImage(ctx, leases.Image) {
		return fmt.Errorf("this node's lease image %q is not built yet; the provider needs to run `make lease-image`",
			leases.Image)
	}
	if !r.docker.HasImage(ctx, leases.Egress.ProxyImage) {
		return fmt.Errorf("this node's egress proxy image %q is not built yet; the provider needs to run `make lease-image`",
			leases.Egress.ProxyImage)
	}
	return nil
}

// ValidateLeaseExtension checks a top-up before the renter is asked to pay for
// it, so a slice that would exceed this node's total cap is refused for free
// rather than after the money has moved.
func (r *Runner) ValidateLeaseExtension(lease *Lease, spec LeaseSpec) error {
	leases := r.cfg.Leases
	if spec.Minutes < leases.MinMinutes || spec.Minutes > leases.MaxMinutes {
		return fmt.Errorf("minutes must be between %d and %d on this node", leases.MinMinutes, leases.MaxMinutes)
	}
	if err := sshca.ValidatePublicKey(spec.PublicKey); err != nil {
		return err
	}

	lease.mu.RLock()
	status, paid := lease.status, lease.paidMinutes
	lease.mu.RUnlock()

	if status.IsTerminal() {
		return fmt.Errorf("this lease is already %s and cannot be extended", status)
	}
	if paid+spec.Minutes > leases.MaxTotalMinutes {
		return fmt.Errorf("this node caps a lease at %d minutes total; %d have been bought already",
			leases.MaxTotalMinutes, paid)
	}
	return nil
}

// StartLease provisions a lease container and returns once it is serving.
//
// It is called after payment verification and before settlement, so everything
// here is on the renter's clock: the signed payload expires at
// maxTimeoutSeconds. That is why the image is required to be present already
// (see ValidateLeaseSpec) and why nothing slow happens in this path.
//
// If any step fails the caller must not settle. Nothing is charged for a lease
// that never came up.
func (r *Runner) StartLease(ctx context.Context, id, token, caPublicKey string, spec LeaseSpec) (*Lease, error) {
	if err := r.ValidateLeaseSpec(ctx, spec); err != nil {
		return nil, err
	}

	jupyterToken, err := randomHex(32)
	if err != nil {
		return nil, fmt.Errorf("mint a Jupyter token: %w", err)
	}

	lease := &Lease{
		ID:           id,
		status:       LeaseProvisioning,
		token:        token,
		network:      "cleargate-lease-" + id,
		volume:       "cleargate-workspace-" + id,
		jupyterToken: jupyterToken,
		publicKey:    strings.TrimSpace(spec.PublicKey),
		// What the renter can actually use, not what the host has.
		gpu:          r.LeaseGPU(),
		createdAt:    time.Now(),
		expiresAt:    time.Now().Add(time.Duration(spec.Minutes) * time.Minute),
		sliceMinutes: spec.Minutes,
		paidMinutes:  spec.Minutes,
	}

	// Claim the single lease slot before doing anything expensive, so two
	// renters racing for the same node cannot both be provisioned.
	r.mu.Lock()
	if r.activeLease != "" {
		if existing, ok := r.leases[r.activeLease]; ok && !existing.Status().IsTerminal() {
			r.mu.Unlock()
			return nil, errors.New("this node already has an active lease; it rents to one renter at a time")
		}
	}
	r.leases[id] = lease
	r.activeLease = id
	r.mu.Unlock()

	if err := r.provisionLease(ctx, lease, caPublicKey, spec); err != nil {
		lease.fail(err)
		// Detached so cleanup still runs if the renter hung up mid-provision.
		r.reapLeaseResources(context.WithoutCancel(ctx), lease)
		r.releaseLeaseSlot(id)
		return nil, err
	}

	lease.setStatus(LeaseActive)
	r.events.publish(Event{
		Kind: EventLeaseStarted, LeaseID: id, Detail: fmt.Sprintf("%d minutes", spec.Minutes),
	})
	r.log.Info("lease started", "lease", id, "minutes", spec.Minutes, "gpu", lease.gpu)
	return lease, nil
}

// provisionLease builds the whole isolated environment for one lease: an
// internal network, the egress proxy that is its only way out, the lease
// container itself, and a check that Jupyter is actually answering.
func (r *Runner) provisionLease(ctx context.Context, lease *Lease, caPublicKey string, spec LeaseSpec) error {
	labels := map[string]string{leaseLabel: lease.ID}

	// Internal: no route to anything outside this network. This is the whole
	// egress story — a renter with a live shell can unset HTTP_PROXY, but there
	// is nothing behind it to reach.
	if err := r.docker.CreateNetwork(ctx, lease.network, true, labels); err != nil {
		return fmt.Errorf("create the lease network: %w", err)
	}
	if err := r.docker.CreateVolume(ctx, lease.volume, labels); err != nil {
		return fmt.Errorf("create the lease workspace: %w", err)
	}
	if err := r.startEgressProxy(ctx, lease, labels); err != nil {
		return err
	}

	containerID, err := r.docker.CreateContainer(ctx, "cleargate-lease-"+lease.ID,
		r.leaseContainerRequest(lease, caPublicKey, spec, labels))
	if err != nil {
		return fmt.Errorf("create the lease container: %w", err)
	}
	lease.mu.Lock()
	lease.containerID = containerID
	lease.mu.Unlock()

	if err := r.docker.StartContainer(ctx, containerID); err != nil {
		return fmt.Errorf("start the lease container: %w", err)
	}

	ip, err := r.docker.ContainerIP(ctx, containerID, lease.network)
	if err != nil {
		return fmt.Errorf("find the lease container's address: %w", err)
	}
	lease.mu.Lock()
	lease.containerIP = ip
	lease.mu.Unlock()

	// Confirm the container is actually serving before the tunnel is pointed at
	// it, so a broken image fails here rather than looking like a tunnel fault.
	if err := r.awaitJupyter(ctx, lease.JupyterAddr()); err != nil {
		return err
	}
	return nil
}

// startEgressProxy brings up the one container that is dual-homed: on the
// lease's internal network, and on the default bridge.
//
// Everything the renter's container can reach off the box goes through here,
// and this proxy denies by default. Interactive access makes egress abuse a
// materially bigger liability than a batch job does — somebody with a live
// terminal can spend the provider's IP reputation in ways a sandboxed script
// cannot — so lease egress is an allowlist, not the blocklist the job flow's
// dataset fetcher uses (implementation.md §3.4).
func (r *Runner) startEgressProxy(ctx context.Context, lease *Lease, labels map[string]string) error {
	name := "cleargate-egress-" + lease.ID

	id, err := r.docker.CreateContainer(ctx, name, CreateContainerRequest{
		Image: r.cfg.Leases.Egress.ProxyImage,
		Env: []string{
			fmt.Sprintf("EGRESS_LISTEN=:%d", egressPort),
			"EGRESS_ALLOWLIST=" + strings.Join(r.cfg.Leases.Egress.Allowlist, ","),
		},
		Labels: labels,
		HostConfig: HostConfig{
			NetworkMode: lease.network,
			// Small: it forwards bytes and matches hostnames, nothing else.
			Memory:      256 * 1024 * 1024,
			NanoCPUs:    1_000_000_000,
			PidsLimit:   256,
			CapDrop:     []string{"ALL"},
			SecurityOpt: []string{"no-new-privileges"},
			// The proxy holds no state a restart would lose, and a read-only
			// root is free here in a way it is not for a dev container.
			ReadonlyRootfs: true,
			AutoRemove:     false,
		},
		NetworkingConfig: &NetworkingConfig{
			EndpointsConfig: map[string]EndpointConfig{
				lease.network: {Aliases: []string{"egress"}},
			},
		},
	})
	if err != nil {
		return fmt.Errorf("create the egress proxy: %w", err)
	}
	lease.mu.Lock()
	lease.proxyID = id
	lease.mu.Unlock()

	// The second leg. Attached before start, so the proxy is never running
	// without a way out and never briefly bridging the lease network to it.
	if err := r.docker.ConnectNetwork(ctx, "bridge", id, nil); err != nil {
		return fmt.Errorf("give the egress proxy a route out: %w", err)
	}
	if err := r.docker.StartContainer(ctx, id); err != nil {
		return fmt.Errorf("start the egress proxy: %w", err)
	}
	return nil
}

// leaseContainerRequest builds the interactive sandbox.
//
// It differs from the job sandbox in exactly the ways an interactive session
// forces, and no further:
//
//   - It has a network, because a lease is useless if a renter cannot install a
//     package. That network is internal and proxied (see startEgressProxy).
//   - Its root filesystem is writable, because `pip install` and `apt-get` are
//     the normal use of a rented dev box. The job sandbox's read-only root is
//     not "practical" here in the sense implementation.md §3.3 means it.
//   - It keeps capped memory, CPU and process count, drops every capability but
//     the handful sshd needs to accept a login, and refuses privilege gain.
//
// Root inside this container is not root on the host: providers running leases
// are directed to enable user-namespace remapping in the daemon, which is what
// makes an in-container root shell safe to hand a stranger.
func (r *Runner) leaseContainerRequest(lease *Lease, caPublicKey string, spec LeaseSpec, labels map[string]string) CreateContainerRequest {
	limits := r.cfg.Leases.Limits
	proxy := fmt.Sprintf("http://egress:%d", egressPort)

	env := []string{
		"JUPYTER_TOKEN=" + lease.jupyterToken,
		// The CA's public half, which the container's sshd trusts via
		// TrustedUserCAKeys. The private half never leaves this machine.
		"SSH_CA_PUBKEY=" + caPublicKey,
		// The only principal this container's sshd will accept. A certificate
		// minted for a different lease carries a different principal and is
		// refused here even though it was signed by the same CA.
		"LEASE_PRINCIPAL=" + lease.ID,
		"LEASE_ID=" + lease.ID,
		"LEASE_EXPIRES_AT=" + lease.expiresAt.UTC().Format(time.RFC3339),
		// Both spellings: tooling is split on which it reads.
		"HTTP_PROXY=" + proxy,
		"HTTPS_PROXY=" + proxy,
		"http_proxy=" + proxy,
		"https_proxy=" + proxy,
		"NO_PROXY=localhost,127.0.0.1,egress",
		"no_proxy=localhost,127.0.0.1,egress",
	}

	host := HostConfig{
		NetworkMode: lease.network,
		Memory:      limits.MemoryMB * 1024 * 1024,
		NanoCPUs:    int64(limits.CPUCores) * 1_000_000_000,
		PidsLimit:   limits.PidsLimit,
		Mounts: []Mount{
			{Type: "volume", Source: lease.volume, Target: workspaceDir},
		},
		AutoRemove: false,
		CapDrop:    []string{"ALL"},
		// Exactly what sshd needs to bind its port, chroot its privilege
		// separation directory and drop into the login user — plus what a
		// package manager needs to unpack files. Nothing to do with the network
		// stack, no ability to load modules, no device access.
		CapAdd: []string{
			"CHOWN", "DAC_OVERRIDE", "FOWNER", "FSETID",
			"SETGID", "SETUID", "SYS_CHROOT", "KILL", "NET_BIND_SERVICE",
		},
		SecurityOpt: []string{"no-new-privileges"},
	}

	// The card is passed through whenever the host has one, even if the image
	// turns out to have no CUDA runtime — nvidia-smi still works there, which is
	// worth having. What LeaseGPU gates is whether this node *sells* the GPU:
	// `require_gpu` is refused before the 402, and the spec does not advertise
	// one, so nobody pays for a card they cannot compute on.
	if r.gpu.Available {
		host.DeviceRequests = []DeviceRequest{{
			Driver:       "nvidia",
			Count:        -1,
			Capabilities: [][]string{{"gpu"}},
		}}
	}

	return CreateContainerRequest{
		Image:      r.cfg.Leases.Image,
		Env:        env,
		WorkingDir: workspaceDir,
		Labels:     labels,
		HostConfig: host,
		NetworkingConfig: &NetworkingConfig{
			EndpointsConfig: map[string]EndpointConfig{lease.network: {}},
		},
	}
}

// awaitJupyter waits for the lease container to answer on its Jupyter port.
//
// This is the node's own check, before the tunnel is involved at all. It
// separates "the image is broken" from "the tunnel did not come up", which are
// the same symptom to a renter and completely different problems to a provider.
func (r *Runner) awaitJupyter(ctx context.Context, addr string) error {
	client := &http.Client{Timeout: 5 * time.Second}
	deadline := time.Now().Add(leaseReadyTimeout)

	var lastErr error
	for time.Now().Before(deadline) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+addr+"/api", nil)
		if err != nil {
			return err
		}
		response, err := client.Do(req)
		if err == nil {
			_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
			response.Body.Close()
			return nil
		}
		lastErr = err

		select {
		case <-time.After(time.Second):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return fmt.Errorf("the lease container never started serving Jupyter: %w", lastErr)
}

// leaseReadyTimeout bounds provisioning. It has to fit inside the renter's
// signed payment payload lifetime with room for the tunnel check and settlement
// afterwards, which is why the lease image must already be on the box.
const leaseReadyTimeout = 90 * time.Second

// ExtendLease pushes a lease's expiry out by another paid slice.
//
// A frozen lease is thawed here rather than in a separate call: paying is what
// resumption means, and a renter who has just bought more time should find their
// session where they left it.
func (r *Runner) ExtendLease(ctx context.Context, lease *Lease, spec LeaseSpec) (Extension, error) {
	minutes := spec.Minutes

	lease.mu.Lock()
	if lease.status.IsTerminal() {
		lease.mu.Unlock()
		return Extension{}, fmt.Errorf("this lease is already %s", lease.status)
	}
	if lease.paidMinutes+minutes > r.cfg.Leases.MaxTotalMinutes {
		remaining := r.cfg.Leases.MaxTotalMinutes - lease.paidMinutes
		lease.mu.Unlock()
		return Extension{}, fmt.Errorf("this node caps a lease at %d minutes total; %d remain, so %d cannot be bought",
			r.cfg.Leases.MaxTotalMinutes, remaining, minutes)
	}

	// Everything needed to put the lease back exactly as it was, in case the
	// payment for this slice does not settle.
	previous := Extension{
		minutes:      minutes,
		expiresAt:    lease.expiresAt,
		sliceMinutes: lease.sliceMinutes,
		publicKey:    lease.publicKey,
		status:       lease.status,
		pausedAt:     lease.pausedAt,
	}

	// A renter may present a fresh key on an extension; the certificate about
	// to be minted is for whatever they sent now, so record that as theirs.
	lease.publicKey = strings.TrimSpace(spec.PublicKey)

	// Extend from now when the lease has already lapsed, and from the old
	// expiry when it has not — otherwise a renter who extends early silently
	// loses the time they had left.
	base := lease.expiresAt
	if base.Before(time.Now()) {
		base = time.Now()
	}
	lease.expiresAt = base.Add(time.Duration(minutes) * time.Minute)
	lease.paidMinutes += minutes
	lease.sliceMinutes = minutes
	wasPaused := lease.status == LeasePaused
	containerID := lease.containerID
	lease.pausedAt = nil
	lease.status = LeaseActive
	newExpiry := lease.expiresAt
	lease.mu.Unlock()

	if wasPaused && containerID != "" {
		if err := r.docker.UnpauseContainer(ctx, containerID); err != nil {
			r.log.Warn("could not thaw the extended lease", "lease", lease.ID, "error", err)
		}
	}

	r.events.publish(Event{
		Kind: EventLeaseExtended, LeaseID: lease.ID, Detail: fmt.Sprintf("+%d minutes", minutes),
	})
	r.log.Info("lease extended", "lease", lease.ID, "minutes", minutes, "expires_at", newExpiry)
	return previous, nil
}

// Extension is what a lease looked like before a slice was added to it.
//
// An extension is applied before its payment settles, for the same reason a job
// starts before its payment settles: the renter's signed payload expires, and
// what they are buying has to be in place first. Unlike a job, though, the thing
// applied here is trivially reversible — so when settlement fails, it is
// reversed rather than left as free time.
type Extension struct {
	minutes      int
	expiresAt    time.Time
	sliceMinutes int
	publicKey    string
	status       LeaseStatus
	pausedAt     *time.Time
}

// RevertExtension undoes an extension whose payment did not settle.
//
// The lease goes back to the expiry, slice size and identity it had a moment
// ago. If it had already been frozen, it is frozen again: a renter whose
// payment failed is exactly where they were before they tried, which is
// mid-grace-period with the container's memory intact.
func (r *Runner) RevertExtension(ctx context.Context, lease *Lease, previous Extension) {
	lease.mu.Lock()
	if lease.status.IsTerminal() {
		lease.mu.Unlock()
		return
	}
	lease.expiresAt = previous.expiresAt
	lease.sliceMinutes = previous.sliceMinutes
	lease.publicKey = previous.publicKey
	lease.paidMinutes -= previous.minutes
	lease.status = previous.status
	lease.pausedAt = previous.pausedAt
	refreeze := previous.status == LeasePaused
	containerID := lease.containerID
	lease.mu.Unlock()

	if refreeze && containerID != "" {
		if err := r.docker.PauseContainer(ctx, containerID); err != nil {
			r.log.Warn("could not re-freeze a lease whose extension did not settle",
				"lease", lease.ID, "error", err)
		}
	}
	r.log.Info("extension reverted; nothing was charged", "lease", lease.ID, "minutes", previous.minutes)
}

// PauseLease freezes a lease whose paid time has lapsed.
//
// Freezing, not killing: the container's processes stop consuming CPU but keep
// their memory and their open files, so a renter who is mid-training run and
// slow to pay gets their session back when they extend. The grace period after
// this is what eventually turns a frozen lease into a reaped one.
// ResumeLease thaws a frozen container without buying time.
//
// The direct-payment path thaws inside ExtendLease, because there the act of
// paying and the act of extending are one operation. An escrow session pays at
// the contract before it ever reaches the node, so by the time the node hears
// about it the only thing left to do is unfreeze — which is this.
//
// A renter mid-task who was slow to top up gets their work back exactly as
// they left it: pause is the cgroup freezer, not a kill.
func (r *Runner) ResumeLease(ctx context.Context, lease *Lease) error {
	lease.mu.Lock()
	if lease.status != LeasePaused {
		lease.mu.Unlock()
		return nil
	}
	containerID := lease.containerID
	lease.pausedAt = nil
	lease.status = LeaseActive
	lease.mu.Unlock()

	if containerID == "" {
		return nil
	}
	if err := r.docker.UnpauseContainer(ctx, containerID); err != nil {
		return fmt.Errorf("thaw the session container: %w", err)
	}
	r.log.Info("session resumed after a top-up", "lease", lease.ID)
	return nil
}

func (r *Runner) PauseLease(ctx context.Context, lease *Lease) error {
	lease.mu.Lock()
	if lease.status != LeaseActive {
		lease.mu.Unlock()
		return nil
	}
	containerID := lease.containerID
	now := time.Now()
	lease.pausedAt = &now
	lease.status = LeasePaused
	lease.mu.Unlock()

	if containerID != "" {
		if err := r.docker.PauseContainer(ctx, containerID); err != nil {
			return fmt.Errorf("freeze the lease container: %w", err)
		}
	}

	r.events.publish(Event{
		Kind: EventLeasePaused, LeaseID: lease.ID, Detail: "paid time lapsed",
	})
	r.log.Info("lease frozen; extend to resume", "lease", lease.ID)
	return nil
}

// StopLease ends a lease at the renter's request and releases the node's lease
// slot for the next renter.
//
// Time already bought is not refunded — forward payment is what makes refunds
// unnecessary by design — but stopping does end the meter, so a renter who is
// done stops paying for the slices they would otherwise have auto-extended.
func (r *Runner) StopLease(ctx context.Context, lease *Lease, status LeaseStatus) {
	lease.mu.Lock()
	if lease.status.IsTerminal() {
		lease.mu.Unlock()
		return
	}
	lease.status = status
	lease.mu.Unlock()

	r.reapLeaseResources(ctx, lease)
	r.releaseLeaseSlot(lease.ID)

	r.events.publish(Event{Kind: EventLeaseEnded, LeaseID: lease.ID, Detail: string(status)})
	r.log.Info("lease ended", "lease", lease.ID, "status", status)
}

// PausedFor reports how long a lease has been frozen, or 0 if it is not.
func (l *Lease) PausedFor() time.Duration {
	l.mu.RLock()
	defer l.mu.RUnlock()
	if l.pausedAt == nil {
		return 0
	}
	return time.Since(*l.pausedAt)
}

// OverdueBy reports how far past its paid expiry an active lease is.
func (l *Lease) OverdueBy() time.Duration {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return time.Since(l.expiresAt)
}

// SliceMinutes is the size of the last slice bought, which is what a "missed
// extension" is measured in.
func (l *Lease) SliceMinutes() int {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.sliceMinutes
}

// reapLeaseResources destroys everything a lease owned.
//
// Order matters and is the reverse of creation: containers hold references to
// the network and the volume, so both containers go first. The workspace volume
// is destroyed rather than retained — unlike a job artifact, nobody is coming
// back to download it, and a provider's disk must not become a long-term store
// for other people's data.
func (r *Runner) reapLeaseResources(ctx context.Context, lease *Lease) {
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()

	lease.mu.RLock()
	containerID, proxyID, network, volume := lease.containerID, lease.proxyID, lease.network, lease.volume
	lease.mu.RUnlock()

	for _, id := range []string{containerID, proxyID} {
		if id == "" {
			continue
		}
		// A frozen container cannot be removed until it is thawed, and a lease
		// reaped out of the grace period is frozen by definition.
		_ = r.docker.UnpauseContainer(ctx, id)
		if err := r.docker.RemoveContainer(ctx, id); err != nil {
			r.log.Warn("could not remove a lease container", "lease", lease.ID, "container", id, "error", err)
		}
	}
	if volume != "" {
		if err := r.docker.RemoveVolume(ctx, volume); err != nil {
			r.log.Warn("could not remove the lease workspace", "lease", lease.ID, "volume", volume, "error", err)
		}
	}
	if network != "" {
		if err := r.docker.RemoveNetwork(ctx, network); err != nil {
			r.log.Warn("could not remove the lease network", "lease", lease.ID, "network", network, "error", err)
		}
	}
}

// releaseLeaseSlot frees the node's single lease slot, if this lease held it.
func (r *Runner) releaseLeaseSlot(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.activeLease == id {
		r.activeLease = ""
	}
}

// sweepLeaseOrphans removes lease containers, volumes and networks left over
// from a previous process.
//
// Lease state lives in memory like job state does, so anything still labelled
// as ours at startup is an orphan from a crash — and an orphaned lease is worse
// than an orphaned job: it is a container with a live sshd on it that nothing
// is now metering or reaping.
func (r *Runner) sweepLeaseOrphans(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()

	containers, err := r.docker.ListContainersByLabel(ctx, leaseLabel)
	if err != nil {
		r.log.Warn("could not list orphaned lease containers", "error", err)
	}
	for _, container := range containers {
		_ = r.docker.UnpauseContainer(ctx, container.ID)
		if err := r.docker.RemoveContainer(ctx, container.ID); err != nil {
			r.log.Warn("could not remove an orphaned lease container", "container", container.ID, "error", err)
			continue
		}
		r.log.Info("removed an orphaned lease container", "container", container.ID[:12])
	}

	volumes, err := r.docker.ListVolumesByLabel(ctx, leaseLabel)
	if err != nil {
		r.log.Warn("could not list orphaned lease volumes", "error", err)
	}
	for _, volume := range volumes {
		if err := r.docker.RemoveVolume(ctx, volume); err != nil {
			r.log.Warn("could not remove an orphaned lease volume", "volume", volume, "error", err)
		}
	}

	networks, err := r.docker.ListNetworksByLabel(ctx, leaseLabel)
	if err != nil {
		r.log.Warn("could not list orphaned lease networks", "error", err)
	}
	for _, network := range networks {
		if err := r.docker.RemoveNetwork(ctx, network); err != nil {
			r.log.Warn("could not remove an orphaned lease network", "network", network, "error", err)
		}
	}
}

// randomHex returns n bytes of randomness, hex encoded. Used for the Jupyter
// token, which is the only thing standing between a lease's notebook server and
// anyone who learns its hostname.
func randomHex(n int) (string, error) {
	raw := make([]byte, n)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw), nil
}
