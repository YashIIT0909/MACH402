package httpapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/YashIIT0909/ClearGate/agent/internal/runner"
	"github.com/YashIIT0909/ClearGate/agent/internal/x402"
)

// collectPayment runs the 402-challenge-then-verify half of every paid endpoint.
//
// Opening a session and topping one up both go through here, so there is
// exactly one implementation of the order that matters: challenge if unpaid,
// verify against *our* requirements rather than the client's copy of them, and
// never let an unverified payload reach the code that starts work.
//
// amount and description differ per call — an opening chunk may be shorter
// than a top-up — and everything else about the challenge comes from the
// node's own configuration.
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
