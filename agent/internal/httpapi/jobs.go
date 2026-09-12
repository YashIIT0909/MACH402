package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/YashIIT0909/ClearGate/agent/internal/hcs"
	"github.com/YashIIT0909/ClearGate/agent/internal/receipts"
	"github.com/YashIIT0909/ClearGate/agent/internal/runner"
	"github.com/YashIIT0909/ClearGate/agent/internal/x402"
)

// maxJobSpecBytes bounds the request body. A job spec is small; anything larger
// is a mistake or an attack.
const maxJobSpecBytes = 1 << 20

// handleCreateJob is the x402-gated endpoint that sells compute.
//
// The order below is non-negotiable (CLAUDE.md invariant 4):
//
//  1. validate the spec, preflight the dataset -> 400, and costs nothing
//  2. no payment header                        -> 402 challenge
//  3. payment header present                   -> /verify
//  4. verified                                 -> accept the job, create volumes
//  5. job accepted                             -> /settle immediately
//  6. settled                                  -> write the receipt, return the token
//  7. in the background                        -> pull image, stage dataset, run
//
// Settlement happens the moment the job is accepted, never after the work
// finishes: the client's signed payload expires at maxTimeoutSeconds, and both
// a dataset download and a training run outlive that window many times over.
//
// Step 1 carries the weight that step 7 cannot. Everything after settlement is
// unrefundable, so anything knowable in advance — an image off the allowlist, a
// GPU this node does not have, a dataset URL that 404s or is too large — is
// rejected before the renter is ever asked to pay.
func (s *Server) handleCreateJob(w http.ResponseWriter, r *http.Request) {
	spec, err := decodeJobSpec(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	// Reject unrunnable work before taking money for it. This also preflights
	// the dataset URL, which is a network round trip, so it uses the request
	// context and is bounded.
	validateCtx, cancelValidate := context.WithTimeout(r.Context(), 45*time.Second)
	err = s.runner.ValidateSpecContext(validateCtx, spec)
	cancelValidate()
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	// An operator who has paused the node keeps serving /health and /v1/specs
	// but sells nothing, so a renter is told to come back rather than being
	// charged for a job that will not start.
	if s.paused.Load() {
		w.Header().Set("Retry-After", "60")
		writeError(w, http.StatusServiceUnavailable,
			"this node is not accepting new jobs right now; nothing was charged")
		return
	}

	payload, requirements, ok := s.collectPayment(w, r, s.cfg.PriceTinybars, s.jobDescription())
	if !ok {
		return
	}

	// Verified. Start the work, then settle — in that order, and quickly.
	jobID := newJobID()
	token := newAccessToken()

	job, err := s.runner.Start(r.Context(), jobID, token, spec)
	if err != nil {
		// Nothing was settled, so the renter has not been charged.
		s.log.Error("job failed to start", "job", jobID, "error", err)
		writeError(w, http.StatusInternalServerError, "could not start the job: "+err.Error())
		return
	}

	// Settle on a context detached from the request: a renter disconnecting
	// mid-flight must not abandon a payment for work already underway.
	settleCtx, settleCancel := context.WithTimeout(context.WithoutCancel(r.Context()), 60*time.Second)
	defer settleCancel()

	settlement, err := s.fac.Settle(settleCtx, x402.SettleRequest{
		X402Version:         x402.Version,
		PaymentPayload:      *payload,
		PaymentRequirements: *requirements,
	})
	if err != nil || !settlement.Success {
		s.emit(runner.Event{
			Kind: runner.EventSettleFailed, JobID: jobID, Detail: reasonOf(settlement),
		})
		s.abandonUnpaidJob(settleCtx, job, jobID, settlement, err)
		s.respondWithChallengeFor(w, r, settlementFailureMessage(settlement, err),
			s.cfg.PriceTinybars, s.jobDescription())
		return
	}

	s.emit(runner.Event{
		Kind:        runner.EventSettled,
		JobID:       jobID,
		Payer:       settlement.Payer,
		Transaction: settlement.Transaction,
		Tinybars:    requirements.Amount,
	})

	receipt := receipts.Receipt{
		JobID:          jobID,
		Transaction:    settlement.Transaction,
		Payer:          settlement.Payer,
		PayTo:          requirements.PayTo,
		AmountTinybars: requirements.Amount,
		Asset:          requirements.Asset,
		Network:        requirements.Network,
	}
	if err := s.receipts.Append(receipt); err != nil {
		// The money moved; failing to log it locally is serious but must not
		// cost the renter a job they already paid for.
		s.log.Error("could not write receipt", "job", jobID, "transaction", settlement.Transaction, "error", err)
	}
	// And to the provider's public audit topic, if they opted in. After the
	// local append and never blocking on it: receipts.jsonl is the record of
	// account, this is the copy nobody can edit afterwards.
	s.publishAudit(hcs.AuditMessage{
		Kind:           hcs.KindJob,
		JobID:          jobID,
		Transaction:    settlement.Transaction,
		Payer:          settlement.Payer,
		PayTo:          requirements.PayTo,
		AmountTinybars: requirements.Amount,
		Asset:          requirements.Asset,
		Network:        requirements.Network,
	})

	s.log.Info("job paid",
		"job", jobID,
		"payer", settlement.Payer,
		"amount_tinybars", requirements.Amount,
		"transaction", settlement.Transaction,
	)

	if err := x402.WriteSettlement(w, settlement); err != nil {
		s.log.Error("could not attach settlement header", "error", err)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"job_id":          jobID,
		"token":           token,
		"status":          job.Status(),
		"transaction":     settlement.Transaction,
		"payer":           settlement.Payer,
		"amount_tinybars": requirements.Amount,
	})
}

// abandonUnpaidJob kills work that was started against a payment that then
// failed to settle. The renter is not charged and the node does not do free work.
func (s *Server) abandonUnpaidJob(ctx context.Context, job *runner.Job, jobID string, settlement *x402.SettleResponse, err error) {
	s.log.Error("settlement failed after the job started; killing it",
		"job", jobID,
		"error", err,
		"reason", reasonOf(settlement),
	)
	if killErr := s.runner.Kill(ctx, job); killErr != nil {
		s.log.Error("could not kill the unpaid job", "job", jobID, "error", killErr)
	}
}

func reasonOf(settlement *x402.SettleResponse) string {
	if settlement == nil {
		return ""
	}
	return settlement.ErrorReason
}

// settlementFailureMessage turns a settle failure into something the renter —
// and the provider reading the logs — can act on.
func settlementFailureMessage(settlement *x402.SettleResponse, err error) string {
	if settlement != nil && settlement.ErrorReason != "" {
		message := "settlement failed: " + settlement.ErrorReason
		if settlement.ErrorMessage != "" {
			message += " (" + settlement.ErrorMessage + ")"
		}
		// The provider has not associated the settlement token. This is a node
		// misconfiguration, not the renter's fault, so say so explicitly.
		if strings.Contains(settlement.ErrorReason, "TOKEN_NOT_ASSOCIATED_TO_ACCOUNT") ||
			strings.Contains(settlement.ErrorMessage, "TOKEN_NOT_ASSOCIATED_TO_ACCOUNT") {
			message += " — this node's account has not associated the settlement token; " +
				"the provider must associate it before it can be paid"
		}
		return message
	}
	if err != nil {
		return "settlement failed: " + err.Error()
	}
	return "settlement failed"
}

// collectPayment runs the 402-challenge-then-verify half of every paid endpoint.
//
// It is shared by jobs and leases so there is exactly one implementation of the
// order that matters: challenge if unpaid, verify against *our* requirements
// rather than the client's copy of them, and never let an unverified payload
// reach the code that starts work.
//
// amount and description differ per endpoint — a job is a flat fee, a lease is
// priced by the minutes being bought — and everything else about the challenge
// comes from the node's own configuration.
//
// It returns ok=false having already written a response.
func (s *Server) collectPayment(
	w http.ResponseWriter,
	r *http.Request,
	amount, description string,
) (*x402.PaymentPayload, *x402.PaymentRequirements, bool) {
	payload, present, err := x402.DecodePaymentHeader(r)
	if err != nil {
		s.respondWithChallengeFor(w, r, "malformed payment header: "+err.Error(), amount, description)
		return nil, nil, false
	}
	if !present {
		s.emit(runner.Event{Kind: runner.EventChallenged, Detail: r.URL.Path})
		s.respondWithChallengeFor(w, r, "Payment required", amount, description)
		return nil, nil, false
	}

	// The payload's `accepted` block is attacker-controlled, so the requirements
	// verified against are rebuilt here from this node's own configuration.
	requirements, err := s.requirements(r, amount, description)
	if err != nil {
		s.log.Error("cannot build payment requirements", "error", err)
		writeError(w, http.StatusServiceUnavailable, "facilitator unreachable; try again shortly")
		return nil, nil, false
	}

	verifyCtx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	verification, err := s.fac.Verify(verifyCtx, x402.VerifyRequest{
		X402Version:         x402.Version,
		PaymentPayload:      *payload,
		PaymentRequirements: *requirements,
	})
	if err != nil {
		s.log.Error("verify failed", "error", err)
		s.respondWithChallengeFor(w, r, "payment verification failed: "+err.Error(), amount, description)
		return nil, nil, false
	}
	if !verification.IsValid {
		reason := verification.InvalidReason
		if verification.InvalidMessage != "" {
			reason += ": " + verification.InvalidMessage
		}
		s.emit(runner.Event{Kind: runner.EventRejected, Detail: reason, Payer: verification.Payer})
		s.respondWithChallengeFor(w, r, "payment invalid: "+reason, amount, description)
		return nil, nil, false
	}

	s.emit(runner.Event{Kind: runner.EventVerified, Payer: verification.Payer})
	return payload, requirements, true
}

func (s *Server) jobDescription() string {
	return "One GPU job on ClearGate node " + s.cfg.NodeID
}

// requirements builds this node's payment requirements for the current request.
func (s *Server) requirements(r *http.Request, amount, description string) (*x402.PaymentRequirements, error) {
	challenge, err := s.buildChallenge(r, "", amount, description)
	if err != nil {
		return nil, err
	}
	if len(challenge.Accepts) == 0 {
		return nil, errors.New("challenge has no payment options")
	}
	return &challenge.Accepts[0], nil
}

func (s *Server) buildChallenge(r *http.Request, reason, amount, description string) (*x402.PaymentRequired, error) {
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	return x402.BuildChallenge(ctx, s.fac, x402.ChallengeConfig{
		PayTo:             s.cfg.PayTo,
		Amount:            amount,
		Asset:             s.cfg.Asset,
		Network:           s.cfg.Network,
		MaxTimeoutSeconds: s.cfg.MaxTimeoutSeconds,
		Description:       description,
		ServiceName:       "ClearGate",
		MimeType:          "application/json",
	}, s.resourceURL(r), reason)
}

// resourceURL is the absolute URL the client requested. It goes into the
// challenge's resource.url and must match what the client actually asked for.
func (s *Server) resourceURL(r *http.Request) string {
	if s.cfg.PublicURL != "" {
		return strings.TrimRight(s.cfg.PublicURL, "/") + r.URL.Path
	}

	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if forwarded := r.Header.Get("X-Forwarded-Proto"); forwarded != "" {
		scheme = forwarded
	}
	return fmt.Sprintf("%s://%s%s", scheme, r.Host, r.URL.Path)
}

func (s *Server) respondWithChallengeFor(w http.ResponseWriter, r *http.Request, reason, amount, description string) {
	challenge, err := s.buildChallenge(r, reason, amount, description)
	if err != nil {
		s.log.Error("cannot build challenge", "error", err)
		writeError(w, http.StatusServiceUnavailable, "facilitator unreachable; try again shortly")
		return
	}
	if err := x402.WriteChallenge(w, challenge); err != nil {
		s.log.Error("cannot write challenge", "error", err)
	}
}

func decodeJobSpec(r *http.Request) (runner.Spec, error) {
	var spec runner.Spec
	decoder := json.NewDecoder(io.LimitReader(r.Body, maxJobSpecBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&spec); err != nil {
		return runner.Spec{}, fmt.Errorf("invalid job spec: %w", err)
	}
	return spec, nil
}
