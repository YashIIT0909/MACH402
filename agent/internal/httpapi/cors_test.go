package httpapi

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/YashIIT0909/ClearGate/agent/internal/config"
)

// corsServer builds the minimum Server needed to exercise the middleware. The
// runner, facilitator and CA are all nil: withCORS reads nothing but config.
func corsServer(origins []string) *Server {
	return &Server{
		cfg: config.Config{CORS: config.CORS{AllowedOrigins: origins}},
		log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}

func corsHandler(s *Server) http.Handler {
	return s.withCORS(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
}

func TestCORSExposesPaymentHeaders(t *testing.T) {
	// The whole reason this middleware exists: a browser that cannot read
	// PAYMENT-REQUIRED cannot see what it is being asked to pay, and one that
	// cannot read PAYMENT-RESPONSE cannot prove it paid.
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v1/sessions", nil)
	request.Header.Set("Origin", "https://cleargate.example")

	corsHandler(corsServer([]string{"*"})).ServeHTTP(recorder, request)

	exposed := recorder.Header().Get("Access-Control-Expose-Headers")
	for _, name := range []string{"PAYMENT-REQUIRED", "PAYMENT-RESPONSE"} {
		if !strings.Contains(exposed, name) {
			t.Errorf("Access-Control-Expose-Headers %q does not expose %s", exposed, name)
		}
	}
}

func TestCORSPreflightAllowsPaymentSignature(t *testing.T) {
	// A preflight that does not allow PAYMENT-SIGNATURE means the browser never
	// sends the payment at all, and the renter sees a silent failure.
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodOptions, "/v1/sessions", nil)
	request.Header.Set("Origin", "https://cleargate.example")
	request.Header.Set("Access-Control-Request-Method", "POST")
	request.Header.Set("Access-Control-Request-Headers", "PAYMENT-SIGNATURE,Authorization")

	corsHandler(corsServer([]string{"*"})).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusNoContent {
		t.Errorf("preflight answered %d, want %d", recorder.Code, http.StatusNoContent)
	}
	allowed := recorder.Header().Get("Access-Control-Allow-Headers")
	if !strings.Contains(allowed, "PAYMENT-SIGNATURE") {
		t.Errorf("Access-Control-Allow-Headers %q does not allow PAYMENT-SIGNATURE", allowed)
	}
	if !strings.Contains(allowed, "Authorization") {
		t.Errorf("Access-Control-Allow-Headers %q does not allow Authorization", allowed)
	}
}

func TestCORSNeverAllowsCredentials(t *testing.T) {
	// The wildcard default is only safe because no cookie ever authenticates
	// anything here. Sending this header would quietly invalidate that.
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/v1/specs", nil)
	request.Header.Set("Origin", "https://anywhere.example")

	corsHandler(corsServer([]string{"*"})).ServeHTTP(recorder, request)

	if got := recorder.Header().Get("Access-Control-Allow-Credentials"); got != "" {
		t.Errorf("Access-Control-Allow-Credentials was sent as %q; it must never be set", got)
	}
}

func TestCORSWildcardEchoesRequestOrigin(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/v1/specs", nil)
	request.Header.Set("Origin", "https://renter.example")

	corsHandler(corsServer([]string{"*"})).ServeHTTP(recorder, request)

	if got := recorder.Header().Get("Access-Control-Allow-Origin"); got != "https://renter.example" {
		t.Errorf("Access-Control-Allow-Origin = %q, want the caller's own origin echoed", got)
	}
	if vary := recorder.Header().Get("Vary"); !strings.Contains(vary, "Origin") {
		t.Errorf("Vary = %q, want it to include Origin so caches do not cross origins", vary)
	}
}

func TestCORSAllowlistRefusesOtherOrigins(t *testing.T) {
	server := corsServer([]string{"https://cleargate.example"})

	allowed := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/v1/specs", nil)
	request.Header.Set("Origin", "https://cleargate.example")
	corsHandler(server).ServeHTTP(allowed, request)
	if got := allowed.Header().Get("Access-Control-Allow-Origin"); got != "https://cleargate.example" {
		t.Errorf("configured origin was not allowed: got %q", got)
	}

	refused := httptest.NewRecorder()
	other := httptest.NewRequest(http.MethodGet, "/v1/specs", nil)
	other.Header.Set("Origin", "https://evil.example")
	corsHandler(server).ServeHTTP(refused, other)
	if got := refused.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("unlisted origin was allowed: got %q", got)
	}
	// The request still runs — the browser is what refuses. Answering 403 would
	// surface a policy decision to the renter as a broken node.
	if refused.Code != http.StatusOK {
		t.Errorf("unlisted origin got status %d, want the request to be served normally", refused.Code)
	}
}

func TestCORSIgnoresNonBrowserRequests(t *testing.T) {
	// Non-browser clients — the smoke test, an agent — send no Origin. It must
	// pass through untouched.
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/v1/specs", nil)

	corsHandler(corsServer([]string{"https://only-this.example"})).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
	if got := recorder.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("a request with no Origin got CORS headers: %q", got)
	}
}

// The paid retry carries a header nobody would predict.
//
// `@x402/fetch` sets `Access-Control-Expose-Headers` on the *request* when it
// re-sends with a payment attached. It is a response header used as a request
// header, which is strange, but it is what the reference client does — and it
// is not a simple header, so a preflight that fails to name it makes the
// browser block the one request carrying the renter's signature. It presents as
// a bare "TypeError: Failed to fetch" against a node answering every other
// probe correctly, which is close to undiagnosable from the outside.
func TestCORSPreflightAllowsWhateverTheClientSends(t *testing.T) {
	requested := "content-type,payment-signature,access-control-expose-headers"

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodOptions, "/v1/sessions", nil)
	request.Header.Set("Origin", "http://localhost:3000")
	request.Header.Set("Access-Control-Request-Method", "POST")
	request.Header.Set("Access-Control-Request-Headers", requested)

	corsHandler(corsServer([]string{"*"})).ServeHTTP(recorder, request)

	allowed := recorder.Header().Get("Access-Control-Allow-Headers")
	for _, name := range strings.Split(requested, ",") {
		if !strings.Contains(strings.ToLower(allowed), name) {
			t.Errorf("Access-Control-Allow-Headers %q does not allow %q, so the browser will "+
				"block the paid request", allowed, name)
		}
	}

	// The answer varies by what was asked for, so it must not be cached across
	// requests that asked for different things. Values, not Get: Vary is sent as
	// repeated headers here and Get would only ever see "Origin".
	if vary := strings.Join(recorder.Header().Values("Vary"), ", "); !strings.Contains(vary, "Access-Control-Request-Headers") {
		t.Errorf("Vary = %q, want it to include Access-Control-Request-Headers", vary)
	}
}

// With nothing requested the static list still stands, so a plain client that
// sends only Content-Type is unaffected by the echoing above.
func TestCORSPreflightFallsBackToTheStaticList(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodOptions, "/v1/sessions", nil)
	request.Header.Set("Origin", "http://localhost:3000")
	request.Header.Set("Access-Control-Request-Method", "POST")

	corsHandler(corsServer([]string{"*"})).ServeHTTP(recorder, request)

	allowed := recorder.Header().Get("Access-Control-Allow-Headers")
	if !strings.Contains(allowed, "PAYMENT-SIGNATURE") {
		t.Errorf("Access-Control-Allow-Headers %q lost the baseline list", allowed)
	}
}
