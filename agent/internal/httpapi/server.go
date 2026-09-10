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
	"github.com/YashIIT0909/ClearGate/agent/internal/receipts"
	"github.com/YashIIT0909/ClearGate/agent/internal/runner"
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

	// job-token-gated
	mux.HandleFunc("GET /v1/jobs/{id}", s.handleJobState)
	mux.HandleFunc("GET /v1/jobs/{id}/logs", s.handleJobLogs)
	mux.HandleFunc("GET /v1/jobs/{id}/artifact", s.handleJobArtifact)
	mux.HandleFunc("POST /v1/jobs/{id}/stop", s.handleJobStop)

	return s.withLogging(mux)
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
	gpu := s.runner.GPU()

	spec := map[string]any{
		"node_id":         s.cfg.NodeID,
		"agent_version":   s.version,
		"pay_to":          s.cfg.PayTo,
		"price_tinybars":  s.cfg.PriceTinybars,
		"facilitator_url": s.cfg.FacilitatorURL,
		"network":         s.cfg.Network,
		"asset":           s.cfg.Asset,
		"image_allowlist": s.cfg.ImageAllowlist,
		"gpu": map[string]any{
			"available": gpu.Available,
			"model":     nullableString(gpu.Model),
			"vram_mb":   nullableInt(gpu.VRAMMb),
			"reason":    nullableString(gpu.Reason),
		},
		"limits": map[string]any{
			"max_seconds":     s.cfg.Limits.MaxSeconds,
			"memory_mb":       s.cfg.Limits.MemoryMB,
			"cpu_cores":       s.cfg.Limits.CPUCores,
			"max_artifact_mb": s.cfg.Limits.MaxArtifactMB,
		},
	}

	// Advertise the fee payer too, so a client can pre-build a payment without
	// first triggering a 402.
	if kind, err := s.fac.Kind(r.Context(), x402.SchemeExact, s.cfg.Network); err == nil {
		if feePayer, ok := kind.FeePayer(); ok {
			spec["fee_payer"] = feePayer
		}
	}

	writeJSON(w, http.StatusOK, spec)
}

func nullableString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func nullableInt(n int) any {
	if n == 0 {
		return nil
	}
	return n
}

// newJobID returns a short, unguessable job identifier.
func newJobID() string {
	var raw [8]byte
	_, _ = rand.Read(raw[:])
	return hex.EncodeToString(raw[:])
}

// newJobToken mints the bearer token that authorizes reading one job's logs and
// artifact. 32 bytes of randomness, issued only after payment settles.
func newJobToken() string {
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
