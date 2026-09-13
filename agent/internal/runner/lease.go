package runner

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/YashIIT0909/ClearGate/agent/internal/sshca"
)

// A lease sends nothing of the renter's to the provider: the renter gets a shell
// and a Jupyter server on the provider's GPU for as long as their credit lasts,
// and their code and data never leave their own machine.
//
// That moves the risk rather than removing it. Nothing untrusted is uploaded,
// but somebody the provider has never met has an interactive session on their
// hardware and their IP address. So the containment here is strict in the
// places that matter for a live shell — see leaseContainerRequest and the
// egress proxy.
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

// LeaseSpec is what a session provisions its container for.
type LeaseSpec struct {
	// Minutes is how long the container is provisioned for: the opening chunk,
	// rounded up. Top-ups extend it through the meter, not through this.
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
	// publicKey is the renter's own SSH public key, kept so the certificate is
	// signed against the identity they presented; the private half was never
	// here to keep.
	publicKey   string
	gpu         bool
	createdAt   time.Time
	expiresAt   time.Time
	paidMinutes int
	pausedAt    *time.Time
	err         string

	// Metering fields, set once the session's opening chunk settles. A session
	// IS a lease — same container, same certificate, same freeze-and-reap —
	// and what it buys is CREDIT: a paid-up balance that the meter burns down
	// second by second while the container is actually running, and whose
	// remainder belongs to the renter the moment they stop.
	//
	// sessionID is the session's key; empty only between provisioning and the
	// opening chunk settling.
	sessionID string
	// payer is the Hedera account the facilitator confirmed paid for this
	// session. It is where a refund goes, so it comes from the settlement
	// rather than from anything the renter asserted in a request body.
	payer string
	// credit is unburned tinybars: exactly what the node owes back if the
	// session stopped right now.
	credit *big.Int
	// burned is cumulative tinybars the provider has actually earned. Kept
	// alongside credit rather than derived, so the audit trail can state both
	// halves of the split without recomputing either.
	burned *big.Int
	// pricePerSecond is the rate credit burns at, fixed for the session's life
	// at the price quoted when it opened.
	pricePerSecond *big.Int
	// lastTickAt is when the meter last ran. Elapsed time is measured from
	// here, not from createdAt, so a paused stretch simply never gets charged.
	lastTickAt time.Time
	// exhausted records that the credit has already hit zero, so expiry is
	// pinned at the moment it did rather than dragged forward by later ticks.
	exhausted bool
	// settleState tracks whether the remainder has been returned yet. A reaped
	// session that is still pending is money the provider is holding that
	// belongs to someone else, so the sweep can act on it.
	settleState SettleState
	// refunded is what was actually paid back, set once by MarkSettled. Kept
	// separately from credit, which MarkSettled zeroes: a caller asking "what
	// did I get back" after the session ended needs this, not "what is owed
	// right now" — which is correctly zero the instant it has been paid.
	refunded *big.Int
}

// SettleState is where a session stands with its renter's remaining credit.
type SettleState string

const (
	// SettleNotApplicable is a lease whose opening chunk has not settled: no
	// money has moved, so nothing is owed back and nothing needs closing.
	SettleNotApplicable SettleState = ""
	// SettlePending means the session is over and the node still holds credit
	// that belongs to the renter.
	SettlePending SettleState = "pending"
	// SettleDone means the remainder has been returned, or there was none.
	SettleDone SettleState = "done"
)

// LeaseState is the container-level view a session's state is built from, and
// what the provider's dashboard lists.
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

// MarkMetered turns this lease into a metered session.
//
// Called once, right after the opening chunk settles at the facilitator. The
// credit is exactly what the facilitator confirmed was paid — never a figure
// recomputed from a price table, because the whole point of the refund trail is
// that the number the node owes back is derived from money that actually moved.
//
// From here on the meter, not the clock, decides when the lease runs out, and
// the remainder belongs to the renter when it ends.
func (l *Lease) MarkMetered(sessionID, payer string, pricePerSecond, credit *big.Int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.sessionID = sessionID
	l.payer = payer
	l.pricePerSecond = new(big.Int).Set(pricePerSecond)
	l.credit = new(big.Int).Set(credit)
	l.burned = big.NewInt(0)
	l.lastTickAt = time.Now()
	l.settleState = SettlePending
	l.syncExpiryLocked()
}

// AddCredit banks another paid chunk. The session's expiry moves out by
// whatever that credit buys at the rate fixed when it opened.
//
// Deliberately additive rather than absolute: a renter who tops up early keeps
// the runway they had left instead of silently losing it.
func (l *Lease) AddCredit(amount *big.Int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.credit == nil {
		return
	}
	l.credit.Add(l.credit, amount)
	l.syncExpiryLocked()
}

// Burn charges the session for the time since the last tick and reports what
// it now owes.
//
// It is the only thing that moves money between the two halves of a session,
// and it only ever runs while the container is actually running: a frozen
// session has its clock reset by Thaw rather than charged for the freeze.
//
// Returns the tinybars just burned and the credit remaining. Credit is clamped
// at zero — a session cannot go into debt, it freezes.
func (l *Lease) Burn(now time.Time) (burned, remaining *big.Int) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.credit == nil || l.pricePerSecond == nil {
		return big.NewInt(0), big.NewInt(0)
	}

	elapsed := now.Sub(l.lastTickAt)
	l.lastTickAt = now
	if elapsed <= 0 {
		return big.NewInt(0), new(big.Int).Set(l.credit)
	}

	// Truncated to whole seconds, so a burn is never charged for a fraction the
	// renter's own arithmetic would not predict. The remainder is not lost: the
	// next tick measures from lastTickAt, which moved by the full elapsed time.
	charge := new(big.Int).Mul(l.pricePerSecond, big.NewInt(int64(elapsed.Seconds())))
	if charge.Cmp(l.credit) > 0 {
		charge = new(big.Int).Set(l.credit)
	}
	l.credit.Sub(l.credit, charge)
	l.burned.Add(l.burned, charge)
	l.syncExpiryLocked()

	return charge, new(big.Int).Set(l.credit)
}

// syncExpiryLocked keeps expiresAt equal to what the credit buys.
//
// This is what lets metering reuse the freeze-and-reap sweep unchanged: the
// sweep asks how far past expiry a lease is, and on a session that question
// means "how long has the credit been at zero". Two schedulers for one event is
// exactly what this codebase does not have.
//
// A no-op until the opening chunk has settled and there is a credit to measure.
// The caller holds l.mu.
func (l *Lease) syncExpiryLocked() {
	if l.pricePerSecond == nil || l.credit == nil {
		return
	}

	if seconds := l.secondsRemainingLocked(); seconds > 0 {
		l.exhausted = false
		l.expiresAt = l.lastTickAt.Add(time.Duration(seconds) * time.Second)
	} else if !l.exhausted {
		// Credit has just run out. Pin expiry at that instant and leave it
		// there: the sweep freezes on how far past expiry a lease is, and a
		// zero-credit session that moved its own expiry forward on every tick
		// would sit at "zero seconds overdue" for ever and never freeze.
		l.exhausted = true
		l.expiresAt = l.lastTickAt
	}

	// paidMinutes is what the lease state reports: minutes bought in total,
	// which is burned plus remaining, rounded up.
	total := new(big.Int).Add(l.burned, l.credit)
	if l.pricePerSecond.Sign() > 0 {
		seconds := new(big.Int).Div(total, l.pricePerSecond).Int64()
		l.paidMinutes = int((seconds + 59) / 60)
	}
}

func (l *Lease) secondsRemainingLocked() int64 {
	if l.credit == nil || l.pricePerSecond == nil || l.pricePerSecond.Sign() <= 0 {
		return 0
	}
	return new(big.Int).Div(l.credit, l.pricePerSecond).Int64()
}

// SecondsRemaining is how much time the unburned credit still buys.
//
// The session's answer to "how long have I got", and computed from money rather
// than read off a clock — which is what makes it agree with the refund figure
// by construction instead of by two pieces of arithmetic happening to match.
func (l *Lease) SecondsRemaining() int64 {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.secondsRemainingLocked()
}

// Credit is what the node owes back if the session stopped right now.
func (l *Lease) Credit() *big.Int {
	l.mu.RLock()
	defer l.mu.RUnlock()
	if l.credit == nil {
		return big.NewInt(0)
	}
	return new(big.Int).Set(l.credit)
}

// Burned is what the provider has earned so far on this session.
func (l *Lease) Burned() *big.Int {
	l.mu.RLock()
	defer l.mu.RUnlock()
	if l.burned == nil {
		return big.NewInt(0)
	}
	return new(big.Int).Set(l.burned)
}

// PricePerSecond is the rate this session's credit burns at.
func (l *Lease) PricePerSecond() *big.Int {
	l.mu.RLock()
	defer l.mu.RUnlock()
	if l.pricePerSecond == nil {
		return big.NewInt(0)
	}
	return new(big.Int).Set(l.pricePerSecond)
}

// SessionID is the metered session backing this lease, or "" before its
// opening chunk has settled.
func (l *Lease) SessionID() string {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.sessionID
}

// Payer is the account that paid for this session, and therefore where its
// refund goes. Taken from the facilitator's settlement, never from a request.
func (l *Lease) Payer() string {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.payer
}

// SettleState reports whether the remainder still needs returning.
func (l *Lease) SettleState() SettleState {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.settleState
}

// Refunded is what was actually paid back to the renter, once settled — zero
// beforehand and zero on a session that never had anything left to return.
//
// Distinct from Credit() on purpose. Credit answers "what is owed right now",
// which is correctly zero the instant it has been paid out — but a caller
// reading the session *after* it ends needs "what was paid out", and nothing
// else on the lease remembers that once the credit itself is zeroed.
func (l *Lease) Refunded() *big.Int {
	l.mu.RLock()
	defer l.mu.RUnlock()
	if l.refunded == nil {
		return big.NewInt(0)
	}
	return new(big.Int).Set(l.refunded)
}

// MarkSettled records that `refunded` has been returned to the renter, and
// zeroes the credit so nothing can be refunded twice.
//
// Idempotent, and it must be: a session can reach its end through the renter
// stopping it and through the sweep reaping it, and both call the same path.
// The amount is recorded even on the second call, so a settle that ran twice
// for any reason still reports the real figure rather than the last caller's.
func (l *Lease) MarkSettled(refunded *big.Int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.settleState == SettleNotApplicable {
		return
	}
	l.settleState = SettleDone
	l.refunded = new(big.Int).Set(refunded)
	if l.credit != nil {
		l.credit.SetInt64(0)
	}
}

// Status reads the current status.
func (l *Lease) Status() LeaseStatus {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.status
}

// ExpiresAt is when the credit currently paid for runs out.
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
// the chain. The image check matters here in particular: a lease settles only
// after the container is up and reachable, so there is no
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
		gpu:         r.LeaseGPU(),
		createdAt:   time.Now(),
		expiresAt:   time.Now().Add(time.Duration(spec.Minutes) * time.Minute),
		paidMinutes: spec.Minutes,
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
// and this proxy denies by default. Somebody with a live terminal can spend the
// provider's IP reputation in ways a sandboxed script cannot, so lease egress
// is an allowlist rather than a blocklist (implementation.md §3.4).
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
// It is looser than a no-network batch sandbox in exactly the ways an
// interactive session forces, and no further:
//
//   - It has a network, because a lease is useless if a renter cannot install a
//     package. That network is internal and proxied (see startEgressProxy).
//   - Its root filesystem is writable, because `pip install` and `apt-get` are
//     the normal use of a rented dev box. A read-only root is not "practical"
//     here in the sense implementation.md §3.3 means it.
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

// PauseLease freezes a lease whose paid time has lapsed.
//
// Freezing, not killing: the container's processes stop consuming CPU but keep
// their memory and their open files, so a renter who is mid-training run and
// slow to top up gets their session back when they do. The grace period after
// this is what eventually turns a frozen lease into a reaped one.
// ResumeLease thaws a frozen container without buying time.
//
// A session's credit is banked separately from the thaw — AddCredit then this —
// so the two steps are distinct.
//
// The meter's clock restarts with the thaw, so the frozen stretch is never
// charged for. A renter who was slow to top up got nothing during it.
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
	lease.lastTickAt = time.Now()
	lease.syncExpiryLocked()
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
	r.log.Info("lease frozen; top up to resume", "lease", lease.ID)
	return nil
}

// StopLease ends a lease at the renter's request and releases the node's lease
// slot for the next renter.
//
// Refunding the unburned credit is the caller's job (httpapi's settleSession),
// done with the lease it already holds: this releases the slot a re-lookup
// would need.
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

// reapLeaseResources destroys everything a lease owned.
//
// Order matters and is the reverse of creation: containers hold references to
// the network and the volume, so both containers go first. The workspace volume
// is destroyed rather than retained — nobody is coming back to download it, and
// a provider's disk must not become a long-term store
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
// Lease state lives in memory, so anything still labelled as ours at startup is
// an orphan from a crash — a container with a live sshd on it that nothing is
// now metering or reaping.
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

// constantTimeEqual compares two tokens without leaking their contents through
// timing. An empty expected token never authorizes anything.
func constantTimeEqual(expected, given string) bool {
	if expected == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(expected), []byte(given)) == 1
}
