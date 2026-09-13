// Command cleargate-egress is the only way out of a lease container's network.
//
// Batch jobs on MACH402 get no network at all: the node downloads a renter's
// dataset itself and mounts it, and the container never has a route anywhere.
// A lease cannot work that way — a renter with a shell has to be able to
// install a package — so the containment moves from "no network" to "one
// network path, and it denies by default".
//
// The mechanism matters as much as the list. The lease container sits on an
// internal Docker network with no route off the host; this proxy is the single
// container attached to both that network and the outside. A renter with root
// in their container can unset HTTP_PROXY, and it gains them nothing: there is
// no other path out to find.
//
// Interactive access is what forces this. A sandboxed script that tries to
// abuse a provider's IP is bounded by what it was uploaded to do; a person at a
// live terminal is not.
package main

import (
	"context"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"time"
)

func main() {
	listen := envOr("EGRESS_LISTEN", ":3128")
	allowlist := parseAllowlist(os.Getenv("EGRESS_ALLOWLIST"))

	if len(allowlist) == 0 {
		// Not an error: a provider may genuinely want a lease with no network
		// at all. Worth saying out loud, because it looks like a broken proxy
		// from inside the container.
		log.Printf("cleargate-egress: empty allowlist — every request will be refused")
	} else {
		log.Printf("cleargate-egress: allowing %s", strings.Join(allowlist, ", "))
	}

	proxy := &proxy{allowlist: allowlist}
	server := &http.Server{
		Addr:              listen,
		Handler:           proxy,
		ReadHeaderTimeout: 30 * time.Second,
	}

	log.Printf("cleargate-egress: listening on %s", listen)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("cleargate-egress: %v", err)
	}
}

type proxy struct {
	allowlist []string
}

func (p *proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	host := hostOf(r)
	if !p.allows(host) {
		log.Printf("refused %s %s", r.Method, host)
		// A plain-language refusal, because the person reading it is at a
		// terminal wondering why pip hung.
		http.Error(w,
			"cleargate: "+host+" is not on this node's egress allowlist. "+
				"Lease containers can reach package and model registries and nothing else.",
			http.StatusForbidden)
		return
	}

	if r.Method == http.MethodConnect {
		p.connect(w, r)
		return
	}
	p.forward(w, r)
}

// connect handles HTTPS. The proxy pipes bytes and never terminates TLS: it
// sees the hostname in the CONNECT line and nothing else. A renter's
// credentials and data stay encrypted end to end, which is the correct
// trade-off — this is a containment boundary, not a monitoring one.
func (p *proxy) connect(w http.ResponseWriter, r *http.Request) {
	upstream, err := net.DialTimeout("tcp", withDefaultPort(r.Host, "443"), 30*time.Second)
	if err != nil {
		http.Error(w, "cleargate: upstream unreachable: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer upstream.Close()

	hijacker, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "cleargate: proxy cannot hijack this connection", http.StatusInternalServerError)
		return
	}
	client, _, err := hijacker.Hijack()
	if err != nil {
		http.Error(w, "cleargate: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer client.Close()

	if _, err := client.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n")); err != nil {
		return
	}

	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(upstream, client); done <- struct{}{} }()
	go func() { _, _ = io.Copy(client, upstream); done <- struct{}{} }()
	<-done
}

// forward handles plain HTTP. Rarer than CONNECT in practice — apt still uses
// it — and handled here rather than refused so a lease container's package
// manager works out of the box.
func (p *proxy) forward(w http.ResponseWriter, r *http.Request) {
	if !r.URL.IsAbs() {
		http.Error(w, "cleargate: expected an absolute URL in a proxied request", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Minute)
	defer cancel()

	outbound, err := http.NewRequestWithContext(ctx, r.Method, r.URL.String(), r.Body)
	if err != nil {
		http.Error(w, "cleargate: "+err.Error(), http.StatusBadRequest)
		return
	}
	copyHeaders(outbound.Header, r.Header)

	response, err := transport.RoundTrip(outbound)
	if err != nil {
		http.Error(w, "cleargate: upstream unreachable: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer response.Body.Close()

	copyHeaders(w.Header(), response.Header)
	w.WriteHeader(response.StatusCode)
	_, _ = io.Copy(w, response.Body)
}

// transport follows no redirects of its own: a 302 goes back to the client so
// the redirect target is checked against the allowlist on its own merits. A
// redirect chain that ends somewhere off the list must not be a way around it.
var transport = &http.Transport{
	Proxy:               nil,
	DialContext:         (&net.Dialer{Timeout: 30 * time.Second}).DialContext,
	TLSHandshakeTimeout: 15 * time.Second,
	MaxIdleConns:        64,
	IdleConnTimeout:     90 * time.Second,
}

// hopByHop headers describe one connection and must not be forwarded to the
// next one.
var hopByHop = []string{
	"Connection", "Proxy-Connection", "Proxy-Authenticate", "Proxy-Authorization",
	"Te", "Trailer", "Transfer-Encoding", "Upgrade", "Keep-Alive",
}

func copyHeaders(dst, src http.Header) {
	for name, values := range src {
		if isHopByHop(name) {
			continue
		}
		for _, value := range values {
			dst.Add(name, value)
		}
	}
}

func isHopByHop(name string) bool {
	for _, header := range hopByHop {
		if strings.EqualFold(name, header) {
			return true
		}
	}
	return false
}

// allows matches a host against the allowlist.
//
// An entry matches the host exactly, or any subdomain of it: "github.com"
// covers "codeload.github.com" but never "notgithub.com". The suffix check is
// anchored on a dot for exactly that reason — a bare strings.HasSuffix would
// let an attacker register the difference.
func (p *proxy) allows(host string) bool {
	host = strings.ToLower(strings.TrimSuffix(hostWithoutPort(host), "."))
	if host == "" {
		return false
	}
	for _, allowed := range p.allowlist {
		if host == allowed || strings.HasSuffix(host, "."+allowed) {
			return true
		}
	}
	return false
}

func parseAllowlist(raw string) []string {
	var out []string
	for _, entry := range strings.Split(raw, ",") {
		entry = strings.ToLower(strings.TrimSpace(entry))
		if entry != "" {
			out = append(out, entry)
		}
	}
	return out
}

// hostOf reads the target host from either form of proxied request: CONNECT
// carries it in the request line, an absolute-URI GET carries it in the URL.
func hostOf(r *http.Request) string {
	if r.Method == http.MethodConnect {
		return r.Host
	}
	if r.URL != nil && r.URL.Host != "" {
		return r.URL.Host
	}
	return r.Host
}

func hostWithoutPort(hostport string) string {
	if host, _, err := net.SplitHostPort(hostport); err == nil {
		return host
	}
	return hostport
}

func withDefaultPort(hostport, port string) string {
	if _, _, err := net.SplitHostPort(hostport); err == nil {
		return hostport
	}
	return net.JoinHostPort(hostport, port)
}

func envOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
