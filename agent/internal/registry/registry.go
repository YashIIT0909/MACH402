// Package registry announces this node to the ClearGate registry so it appears
// on the website.
//
// The registry is discovery only. It never receives, holds or forwards funds
// (CLAUDE.md invariant 3), and it has no say in whether a session runs: payments
// go renter -> node, direct. So every failure here is logged and shrugged off. A
// registry outage must never stop a node selling or finishing a paid session.
package registry

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/YashIIT0909/ClearGate/agent/internal/nodespec"
)

// Interval is how often a node re-announces itself. The registry marks a node
// offline after a few missed beats, so this trades listing freshness against
// pointless traffic.
const Interval = 30 * time.Second

// HeartbeatFunc returns the node's current self-description.
type HeartbeatFunc func(context.Context) nodespec.Heartbeat

// Status is what the provider's dashboard shows about this node's listing.
//
// Every failure in this package is logged and shrugged off, which is right —
// but silently. A provider who typed a registry URL with a typo in it has a
// node that sells perfectly well and appears nowhere, and the only evidence is
// a warning in a log file the dashboard is not showing them. So the announcer
// keeps the outcome of its last beat where a caller can read it.
type Status struct {
	// Configured is false when no registry_url is set. That is a supported
	// choice, not a fault: renters who know the node's URL still pay it.
	Configured bool

	// URL is the registry being announced to, if any.
	URL string

	// LastAttempt and LastSuccess are zero until the first beat of each kind.
	// A LastSuccess well behind LastAttempt is a listing going stale.
	LastAttempt time.Time
	LastSuccess time.Time

	// Err is why the most recent beat failed, or empty if it did not.
	Err string

	// Withdrawn is set once the node has told the registry it is stopping.
	Withdrawn bool
}

// Listed reports whether the registry has heard from this node recently enough
// that the website is still offering it. The registry's own rule is 90 seconds
// since the last beat; this mirrors it rather than inventing a second one.
func (s Status) Listed() bool {
	return s.Configured && !s.Withdrawn && !s.LastSuccess.IsZero() &&
		time.Since(s.LastSuccess) < 90*time.Second
}

// Announcer pushes heartbeats to the registry on a timer.
type Announcer struct {
	baseURL   string
	nodeID    string
	token     string
	heartbeat HeartbeatFunc
	http      *http.Client
	log       *slog.Logger

	mu     sync.Mutex
	status Status
}

// New returns an announcer for the registry at baseURL.
func New(baseURL, nodeID, token string, heartbeat HeartbeatFunc, log *slog.Logger) *Announcer {
	return &Announcer{
		baseURL:   strings.TrimRight(baseURL, "/"),
		nodeID:    nodeID,
		token:     token,
		heartbeat: heartbeat,
		http:      &http.Client{Timeout: 15 * time.Second},
		log:       log,
		status:    Status{Configured: baseURL != "", URL: strings.TrimRight(baseURL, "/")},
	}
}

// Status snapshots the outcome of the most recent beat. Safe from any
// goroutine, which it has to be: the dashboard reads it on every redraw while
// Run is writing it on its own timer.
func (a *Announcer) Status() Status {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.status
}

// Run announces immediately, then every Interval until ctx is cancelled.
//
// The first beat is immediate on purpose: a provider who just ran the install
// script should see their node on the website straight away, not a listing
// that appears half a minute later for no visible reason.
func (a *Announcer) Run(ctx context.Context) {
	a.beat(ctx)

	ticker := time.NewTicker(Interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			a.withdraw()
			return
		case <-ticker.C:
			a.beat(ctx)
		}
	}
}

// withdraw tells the registry this node is stopping.
//
// Without it a provider who presses ctrl-c watches their node advertised as
// available for up to another freshness window, which reads as the site
// ignoring them. The heartbeat timeout alone still covers the case this cannot:
// a node that loses power never gets to say anything.
func (a *Announcer) withdraw() {
	// The shutdown context is already cancelled by the time we get here, so this
	// needs its own — brief, because a provider is waiting on their terminal.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := a.post(ctx, "/v1/nodes/"+a.nodeID+"/offline", nil)

	a.mu.Lock()
	a.status.Withdrawn = true
	a.mu.Unlock()

	if err != nil {
		a.log.Warn("could not tell the registry this node is stopping; the listing will time out on its own",
			"registry", a.baseURL, "error", err)
		return
	}
	a.log.Info("withdrawn from the registry", "registry", a.baseURL)
}

func (a *Announcer) beat(ctx context.Context) {
	err := a.post(ctx, "/v1/nodes/heartbeat", a.heartbeat(ctx))

	a.mu.Lock()
	a.status.LastAttempt = time.Now()
	if err == nil {
		a.status.LastSuccess = a.status.LastAttempt
		a.status.Err = ""
	} else {
		a.status.Err = err.Error()
	}
	a.mu.Unlock()

	if err != nil {
		a.log.Warn("registry heartbeat failed; the node still sells sessions normally",
			"registry", a.baseURL, "error", err)
		return
	}
	a.log.Debug("registry heartbeat sent", "registry", a.baseURL)
}

// post sends an authorized request to the registry. A nil body sends none.
func (a *Announcer) post(ctx context.Context, path string, body any) error {
	var payload io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encode request: %w", err)
		}
		payload = bytes.NewReader(encoded)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.baseURL+path, payload)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Authorization", "Bearer "+a.token)

	response, err := a.http.Do(req)
	if err != nil {
		return fmt.Errorf("post %s: %w", path, err)
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("registry answered %s", response.Status)
	}
	return nil
}
