package httpapi

import (
	"net/http"
	"strconv"
	"strings"
	"time"
)

// The x402 exchange does not survive a browser without these two names.
//
// A cross-origin response only hands JavaScript a short list of "simple"
// headers unless the server names the rest in Access-Control-Expose-Headers.
// Both halves of the payment conversation travel in headers that are not on
// that list, and the challenge is authoritative *in the header* rather than in
// the body, so without this a browser client can see a 402 and be unable to
// read what it was asked to pay.
var exposedHeaders = strings.Join([]string{
	"PAYMENT-REQUIRED",
	"PAYMENT-RESPONSE",
	// v1 spellings, accepted on input for compatibility and exposed for the
	// same reason: a client library pinned to v1 reads these names.
	"X-PAYMENT-RESPONSE",
}, ", ")

// baseAllowedHeaders is what a browser may send us when it asks for nothing in
// particular.
//
// PAYMENT-SIGNATURE is the payment itself. Authorization carries the lease
// token a renter was handed when they paid, which is what lets the same page
// poll and stop the lease it just bought.
var baseAllowedHeaders = strings.Join([]string{
	"Content-Type",
	"Authorization",
	"PAYMENT-SIGNATURE",
	"X-PAYMENT",
}, ", ")

// allowRequestedHeaders answers a preflight with the headers the browser asked
// for, falling back to the static list when it named none.
//
// Echoing rather than matching against a fixed list, and the reason is a bug
// this cost real time to find. `@x402/fetch` sets `Access-Control-Expose-Headers`
// on the *request* when it retries with a payment attached — a response header
// sent as a request header, which is odd but is what the reference client does.
// It is not a simple header, so a preflight that does not name it fails, and the
// browser then blocks the one request that carries the renter's signature. The
// symptom is a bare `TypeError: Failed to fetch` with a node that is answering
// every probe correctly.
//
// A fixed list means any header a client library adds breaks payment with no
// diagnosable error. Echoing cannot: the browser states what it intends to send
// and we answer for exactly that. It grants nothing either, because this node
// authenticates with bearer tokens a renter holds and never with cookies — a
// header a hostile page could set here, it could equally set from its own
// server.
func allowRequestedHeaders(r *http.Request) string {
	if requested := strings.TrimSpace(r.Header.Get("Access-Control-Request-Headers")); requested != "" {
		return requested
	}
	return baseAllowedHeaders
}

// preflightMaxAge is how long a browser may cache the preflight answer.
//
// Worth setting: without it every POST to /v1/leases costs an extra round trip
// to the node, and a renter extending a lease on a timer pays that repeatedly.
const preflightMaxAge = 24 * time.Hour

// withCORS answers preflights and attaches the origin headers to every
// response.
//
// Deliberately wrapping the whole mux rather than a subset of routes. A browser
// that can read /v1/specs but not POST /v1/leases is a confusing half-state,
// and the policy question — may this origin talk to this node at all — is the
// same for every endpoint.
func (s *Server) withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin == "" {
			// Not a browser request. Nothing to negotiate.
			next.ServeHTTP(w, r)
			return
		}

		allowed, ok := s.allowedOrigin(origin)
		if !ok {
			// Send no CORS headers at all and let the browser refuse. Answering
			// 403 here would be worse: it turns a policy decision into an error
			// the page reports as the node being broken.
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
			return
		}

		header := w.Header()
		header.Set("Access-Control-Allow-Origin", allowed)
		header.Set("Access-Control-Expose-Headers", exposedHeaders)
		// Any answer that depends on the request's Origin must say so, or a
		// shared cache will serve one origin's response to another.
		header.Add("Vary", "Origin")

		// Note what is absent: Access-Control-Allow-Credentials. This node
		// authenticates with bearer tokens a renter holds, never with cookies,
		// so there is no ambient authority for a hostile page to borrow — and
		// that is exactly what makes a wildcard origin safe here.

		if r.Method == http.MethodOptions {
			header.Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
			header.Set("Access-Control-Allow-Headers", allowRequestedHeaders(r))
			header.Set("Access-Control-Max-Age", strconv.Itoa(int(preflightMaxAge.Seconds())))
			header.Add("Vary", "Access-Control-Request-Headers")
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// allowedOrigin decides whether an origin may call this node, and what to echo
// back.
//
// A configured wildcard echoes the caller's own origin rather than "*". The two
// are interchangeable today, but echoing keeps the response correct if this
// node ever does need to allow credentials, where "*" is rejected outright.
func (s *Server) allowedOrigin(origin string) (string, bool) {
	for _, candidate := range s.cfg.CORS.AllowedOrigins {
		if candidate == "*" {
			return origin, true
		}
		if strings.EqualFold(candidate, origin) {
			return origin, true
		}
	}
	return "", false
}
