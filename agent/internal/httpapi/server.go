// Package httpapi serves the node's versioned HTTP API.
//
// Endpoints are versioned under /v1 and old versions keep working: nodes update
// on their own schedule (CLAUDE.md invariant 7).
package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/YashIIT0909/ClearGate/agent/internal/config"
	"github.com/YashIIT0909/ClearGate/agent/internal/hcs"
	"github.com/YashIIT0909/ClearGate/agent/internal/hedera"
	"github.com/YashIIT0909/ClearGate/agent/internal/nodespec"
	"github.com/YashIIT0909/ClearGate/agent/internal/receipts"
	"github.com/YashIIT0909/ClearGate/agent/internal/runner"
	"github.com/YashIIT0909/ClearGate/agent/internal/sshca"
	"github.com/YashIIT0909/ClearGate/agent/internal/tunnel"
	"github.com/YashIIT0909/ClearGate/agent/internal/x402"
)

// Server wires configuration, the runner, the facilitator and the receipt log
// into an http.Handler.
type Server struct {
	cfg      config.Config
	runner   *runner.Runner
	fac      *x402.Facilitator
	receipts *receipts.Log
	log      *slog.Logger
	version  string

	// ca and tunnel are nil unless this node opted into leasing. Both are
	// required for a lease to be sellable: the CA is what authorizes a renter's
	// SSH session, and the tunnel is what lets them reach it through NAT.
	ca     *sshca.CA
	tunnel *tunnel.Manager

	// audit publishes settlements and the running refund-owed trail to the
	// provider's own HCS topic. Every call site publishes unconditionally — a
	// node that never opted in holds a no-op publisher, so the settlement paths
	// do not sprout enabled checks.
	//
	// An interface rather than *hcs.Publisher because what is published is now
	// load-bearing rather than bookkeeping: the burn checkpoints are the whole
	// reason a renter can trust a metered session, so the tests have to be able
	// to read what actually went onto the topic.
	audit auditPublisher

	// sessions is true when this node sells interactive time, which it only
	// ever does as metered, refundable sessions. The /v1/sessions routes answer
	// 404 without it.
	sessions bool
	// sidecar pays session refunds. Nil only in tests and on a node whose
	// sidecar could not be started; such a node meters and publishes what it
	// owes but cannot return it itself.
	sidecar *hedera.Sidecar

	// paused stops the node selling new sessions without stopping the one
	// already running. The provider's dashboard toggles it; a renter sees a 503 with a
	// Retry-After rather than a challenge they would pay and regret.
	paused atomic.Bool
}

// New builds the node's HTTP server.
func New(cfg config.Config, run *runner.Runner, fac *x402.Facilitator, log *slog.Logger, version string) *Server {
	return &Server{
		cfg:      cfg,
		runner:   run,
		fac:      fac,
		receipts: receipts.Open(cfg.ReceiptsPath),
		log:      log,
		version:  version,
	}
}

// auditPublisher is what the settlement paths write their record to.
//
// Satisfied by *hcs.Publisher, including a nil one — Publish guards its own
// receiver, so a node that never opted into HCS holds a working no-op.
type auditPublisher interface {
	Publish(hcs.AuditMessage)
}

// EnableAudit attaches the provider's HCS publisher.
//
// Separate from New for the same reason EnableLeases is: publishing needs a key
// on disk and a funded account, which a node that never opted in does not have.
func (s *Server) EnableAudit(publisher *hcs.Publisher) {
	s.audit = publisher
}

// EnableSessions turns on metered, refundable interactive time.
//
// sidecar pays the refunds. It may be nil, which means the node meters and
// publishes what it owes but cannot pay a refund itself — the refund is then
// logged as owed, with everything needed to pay it by hand.
func (s *Server) EnableSessions(sidecar *hedera.Sidecar) {
	s.sessions = true
	s.sidecar = sidecar
}

// publishAudit records a settlement on the provider's topic, if they have one.
//
// Safe to call unconditionally: a node that did not opt into HCS has a nil
// publisher and this does nothing. It never returns an error, because a failed
// publish must not fail a paid request — see hcs.Publisher.Publish.
func (s *Server) publishAudit(msg hcs.AuditMessage) {
	if s.audit == nil {
		return
	}
	s.audit.Publish(msg)
}

// EnableLeases attaches the two things a lease needs: the node's SSH
// certificate authority, and the tunnel that carries a renter's connection in
// through NAT.
//
// It is a separate call rather than a New parameter because either can fail to
// start, and a node without them must answer 404 on the /v1/sessions routes
// rather than take payment for a session it cannot deliver.
func (s *Server) EnableLeases(ca *sshca.CA, tunnels *tunnel.Manager) {
	s.ca = ca
	s.tunnel = tunnels
}

// Reach reports where renters currently reach this node's leases, and whether
// this node sells leases at all.
//
// For the provider's dashboard: the Jupyter URL a renter is on right now, and
// whether leasing is on at all, without the provider having to go and read a
// config file they wrote once.
func (s *Server) Reach() (tunnel.Endpoints, bool) {
	if s.tunnel == nil {
		return tunnel.Endpoints{}, false
	}
	return s.tunnel.Endpoints(), true
}

// EndLease evicts whatever lease is running right now, from the provider's
// side, and returns the id it ended.
//
// A provider watching a stranger's shell on their own machine needs a way to
// end it that is not "kill the daemon", and this is it: the machine is wanted
// back, or the renter is doing something the provider will not host.
//
// It is deliberately the same sequence the reap branch of sweepLeases runs,
// including settling with the lease it already holds rather than looking it up
// again — StopLease releases the node's single lease slot, so a re-lookup finds
// nothing and refunds nobody. A metered session evicted here is charged for the
// seconds it actually used and refunded the rest, exactly as if the renter had
// stopped it themselves.
func (s *Server) EndLease(ctx context.Context, status runner.LeaseStatus) (string, bool) {
	lease, ok := s.runner.ActiveLease()
	if !ok || lease.Status().IsTerminal() {
		return "", false
	}

	// Charge up to this instant before anything is torn down, so the refund is
	// measured against the meter that has been publishing all along.
	if lease.SessionID() != "" && lease.Status() == runner.LeaseActive {
		lease.Burn(time.Now())
	}

	s.runner.StopLease(ctx, lease, status)
	if s.tunnel != nil {
		s.tunnel.Clear(ctx)
	}
	s.settleSession(ctx, lease)
	return lease.ID, true
}

// ReapLeases runs the background loop that freezes and then reclaims leases
// whose paid time has run out. It returns when ctx is cancelled.
func (s *Server) ReapLeases(ctx context.Context) {
	if s.ca == nil || s.tunnel == nil {
		return
	}
	s.reapLeases(ctx)
}

// emit publishes an event for the provider's dashboard. Events are a display
// concern: receipts.jsonl remains the authoritative record of what was earned.
func (s *Server) emit(event runner.Event) {
	s.runner.Publish(event)
}

// SetPaused stops or resumes selling new sessions. A running one is unaffected.
func (s *Server) SetPaused(paused bool) { s.paused.Store(paused) }

// Paused reports whether the node is currently refusing new sessions.
func (s *Server) Paused() bool { return s.paused.Load() }

// Handler returns the routed handler.
//
// Each route's payment status is stated here on purpose: every new endpoint has
// to declare whether it is free, x402-gated, or session-token-gated.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	// free
	mux.HandleFunc("GET /health", s.handleHealth)
	mux.HandleFunc("GET /v1/specs", s.handleSpecs)

	// x402-gated, and metered rather than forward-paid: each payment buys a
	// chunk of credit the node burns down second by second and refunds the
	// remainder of. The top-up is additionally session-token-gated, because it
	// has to name the session it is crediting.
	mux.HandleFunc("POST /v1/sessions", s.handleCreateSession)
	mux.HandleFunc("POST /v1/sessions/{id}/topup", s.handleTopUpSession)

	// session-token-gated, free. Stopping is free and deliberately so: it is
	// what triggers the renter's refund, and charging for it would be perverse.
	mux.HandleFunc("GET /v1/sessions/{id}", s.handleSessionState)
	mux.HandleFunc("POST /v1/sessions/{id}/stop", s.handleSessionStop)

	// free. The card an ERC-8004 agent id resolves to — see handleAgentCard.
	mux.HandleFunc("GET /.well-known/agent-card.json", s.handleAgentCard)

	// CORS sits inside the logger so preflights show up in a provider's log the
	// same as any other request — a renter whose browser is being refused is a
	// support question, and an invisible OPTIONS makes it unanswerable.
	return s.withLogging(s.withCORS(mux))
}

func (s *Server) withLogging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		recorder := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(recorder, r)
		s.log.Info("request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", recorder.status,
			"duration", time.Since(started).Round(time.Millisecond),
		)
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(status int) {
	s.status = status
	s.ResponseWriter.WriteHeader(status)
}

// Flush lets SSE responses reach the client immediately.
func (s *statusRecorder) Flush() {
	if flusher, ok := s.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":            true,
		"agent_version": s.version,
	})
}

// handleSpecs is free: discovery must not cost money, or agents cannot shop.
func (s *Server) handleSpecs(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.Spec(r.Context()))
}

// Spec is the node's description of itself, as served from /v1/specs and sent
// to the registry. A fee payer the facilitator cannot confirm is left out
// rather than guessed: it must match /supported or the client SDK throws
// before signing (CLAUDE.md invariant 5).
func (s *Server) Spec(ctx context.Context) nodespec.Spec {
	feePayer := ""
	if kind, err := s.fac.Kind(ctx, x402.SchemeExact, s.cfg.Network); err == nil {
		if advertised, ok := kind.FeePayer(); ok {
			feePayer = advertised
		}
	}
	return nodespec.Build(s.cfg, s.version, feePayer, s.runner.GPU(), s.runner.LeaseGPU())
}

// Heartbeat is what this node tells the registry about itself. The registry is
// discovery only and never touches money (CLAUDE.md invariant 3), so this
// carries no payment authority — just an address and a spec.
func (s *Server) Heartbeat(ctx context.Context) nodespec.Heartbeat {
	return nodespec.Heartbeat{
		Spec:      s.Spec(ctx),
		PublicURL: strings.TrimRight(s.cfg.PublicURL, "/"),
		Paused:    s.Paused(),
	}
}

// newLeaseID returns a short, unguessable lease identifier.
//
// It doubles as the SSH certificate principal, so it has to be unguessable for
// the same reason the access token does: it is part of what scopes a renter's
// access to their own session.
func newLeaseID() string {
	var raw [8]byte
	_, _ = rand.Read(raw[:])
	return "lease" + hex.EncodeToString(raw[:])
}

// newAccessToken mints the bearer token that scopes a renter to their own
// purchase: one session's state, top-ups and stop button.
// 32 bytes of randomness, issued only once the work exists.
func newAccessToken() string {
	var raw [32]byte
	_, _ = rand.Read(raw[:])
	return hex.EncodeToString(raw[:])
}

// bearerToken pulls a session token out of the Authorization header, or the
// `token` query parameter for a client that cannot set headers.
func bearerToken(r *http.Request) string {
	header := r.Header.Get("Authorization")
	if after, found := strings.CutPrefix(header, "Bearer "); found {
		return strings.TrimSpace(after)
	}
	return r.URL.Query().Get("token")
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

// Listen serves until the context is cancelled, then shuts down gracefully so
// in-flight requests are not cut off.
func (s *Server) Listen(ctx context.Context) error {
	server := &http.Server{
		Addr:              s.cfg.ListenAddr,
		Handler:           s.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	errs := make(chan error, 1)
	go func() {
		s.log.Info("listening", "addr", s.cfg.ListenAddr)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errs <- err
		}
	}()

	select {
	case err := <-errs:
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
		defer cancel()
		s.log.Info("shutting down")
		return server.Shutdown(shutdownCtx)
	}
}
