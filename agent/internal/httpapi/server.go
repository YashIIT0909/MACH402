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

	// sessions is true when this node sells metered, refundable interactive
	// time rather than direct-paid leases (leases.payment_mode: session). The
	// /v1/sessions routes answer 404 without it.
	sessions bool
	// sidecar pays session refunds, and is present only when
	// leases.self_settle is on. A nil sidecar means the node meters and
	// publishes what it owes but cannot return it itself.
	sidecar *hedera.Sidecar

	// paused stops the node selling new jobs without stopping the ones already
	// running. The provider's dashboard toggles it; a renter sees a 503 with a
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
// sidecar may be nil, which means the node meters and publishes what it owes
// but cannot pay a refund itself — leases.self_settle off. That is a weaker
// offer, not a broken one, and it is the caller's choice to make rather than
// something this silently upgrades.
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

// EnableLeases attaches the two things a lease needs that a job does not: the
// node's SSH certificate authority, and the tunnel that carries a renter's
// connection in through NAT.
//
// It is a separate call rather than a New parameter because leasing is opt-in
// per provider, and a node that never opted in must behave exactly as it did
// before leases existed — including answering 404 on the /v1/leases routes.
func (s *Server) EnableLeases(ca *sshca.CA, tunnels *tunnel.Manager) {
	s.ca = ca
	s.tunnel = tunnels
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

// SetPaused stops or resumes accepting new jobs. Running jobs are unaffected.
func (s *Server) SetPaused(paused bool) { s.paused.Store(paused) }

// Paused reports whether the node is currently refusing new jobs.
func (s *Server) Paused() bool { return s.paused.Load() }

// Handler returns the routed handler.
//
// Each route's payment status is stated here on purpose: every new endpoint has
// to declare whether it is free, x402-gated, or job-token-gated.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	// free
	mux.HandleFunc("GET /health", s.handleHealth)
	mux.HandleFunc("GET /v1/specs", s.handleSpecs)

	// x402-gated
	mux.HandleFunc("POST /v1/jobs", s.handleCreateJob)
	mux.HandleFunc("POST /v1/leases", s.handleCreateLease)
	mux.HandleFunc("POST /v1/leases/{id}/extend", s.handleExtendLease)

	// job-token-gated
	mux.HandleFunc("GET /v1/jobs/{id}", s.handleJobState)
	mux.HandleFunc("GET /v1/jobs/{id}/logs", s.handleJobLogs)
	mux.HandleFunc("GET /v1/jobs/{id}/artifact", s.handleJobArtifact)
	mux.HandleFunc("POST /v1/jobs/{id}/stop", s.handleJobStop)

	// lease-token-gated. Reading and stopping a lease are both free: charging
	// for the poll that decides whether to buy another slice would be absurd,
	// and charging to stop would punish a renter for releasing the machine.
	mux.HandleFunc("GET /v1/leases/{id}", s.handleLeaseState)
	mux.HandleFunc("POST /v1/leases/{id}/stop", s.handleLeaseStop)

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

// newJobID returns a short, unguessable job identifier.
func newJobID() string {
	var raw [8]byte
	_, _ = rand.Read(raw[:])
	return hex.EncodeToString(raw[:])
}

// newLeaseID returns a short, unguessable lease identifier.
//
// It doubles as the SSH certificate principal, so it has to be unguessable for
// the same reason the job token does: it is part of what scopes a renter's
// access to their own session.
func newLeaseID() string {
	var raw [8]byte
	_, _ = rand.Read(raw[:])
	return "lease" + hex.EncodeToString(raw[:])
}

// newAccessToken mints the bearer token that scopes a renter to their own
// purchase: one job's logs and artifact, or one lease's state and stop button.
// 32 bytes of randomness, issued only once the work exists.
func newAccessToken() string {
	var raw [32]byte
	_, _ = rand.Read(raw[:])
	return hex.EncodeToString(raw[:])
}

// bearerToken pulls a job token out of the Authorization header, or the
// `token` query parameter for EventSource, which cannot set headers.
func bearerToken(r *http.Request) string {
	header := r.Header.Get("Authorization")
	if after, found := strings.CutPrefix(header, "Bearer "); found {
		return strings.TrimSpace(after)
	}
	return r.URL.Query().Get("token")
}

// authorizeJob resolves a job and checks the caller's token.
//
// A wrong token and an unknown job both answer 404: whether a given job exists
// is not something an unauthorized caller gets to learn.
func (s *Server) authorizeJob(w http.ResponseWriter, r *http.Request) (*runner.Job, bool) {
	id := r.PathValue("id")
	job, found := s.runner.Get(id)
	if !found || !job.AuthorizedBy(bearerToken(r)) {
		writeError(w, http.StatusNotFound, "no such job, or the token does not authorize it")
		return nil, false
	}
	return job, true
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
// in-flight artifact downloads are not cut off.
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
