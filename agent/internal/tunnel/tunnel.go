// Package tunnel gets a renter to a lease container on a machine that has no
// public address.
//
// A provider's box is almost always behind NAT: that is the same assumption
// that makes the registry heartbeat push rather than poll. Paying is a call to
// the node's own API, but using a session means the renter's notebook has to
// reach *into* the provider's network.
//
// The answer is cloudflared, run by the node, so the provider's machine only
// ever makes outbound connections: no port forwarding, no public IP, and no
// always-on bastion VM for us to own and pay for. There is one mode, a quick
// tunnel: `cloudflared tunnel --url`, which needs no Cloudflare account at all
// and hands back a random HTTP-only hostname per lease. Jupyter works; SSH does
// not, because a quick tunnel carries no TCP.
package tunnel

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/YashIIT0909/MACH402/agent/internal/config"
)

// Target is where the tunnel should currently point: the addresses of the one
// active lease's container, as seen from this host.
type Target struct {
	LeaseID string
	// SSHAddr and JupyterAddr are host:port on the lease container's own
	// network. They are container IPs rather than published host ports on
	// purpose: a lease container sits on an internal Docker network with no
	// route out, and publishing ports would mean allocating and tracking host
	// ports for something only cloudflared ever dials.
	SSHAddr     string
	JupyterAddr string
}

// Endpoints is what a renter is told to connect to.
type Endpoints struct {
	// SSHHost is always empty: a quick tunnel carries HTTP only, so there is no
	// SSH route to advertise. It stays in the shape the lease API returns.
	SSHHost string
	// JupyterURL is the base URL of the lease's Jupyter server, without the
	// token — the token is minted per lease and returned separately.
	JupyterURL string
	// Mode echoes which transport produced these, so a client can explain what
	// the renter can and cannot do.
	Mode string
}

// Manager owns this node's cloudflared process.
//
// A quick tunnel cannot be reconfigured once it is running, so it is started
// for each lease and stopped when that lease ends.
type Manager struct {
	cfg config.Tunnel
	log *slog.Logger

	mu       sync.Mutex
	cmd      *exec.Cmd
	stopped  chan struct{}
	target   *Target
	quickURL string
}

// New builds a manager.
func New(cfg config.Tunnel, log *slog.Logger) *Manager {
	return &Manager{cfg: cfg, log: log}
}

// Mode reports which transport this manager is configured for.
func (m *Manager) Mode() string { return m.cfg.Mode }

// Endpoints reports where the current lease is published, for a caller that did
// not do the publishing — an extension re-states the connection details without
// touching the tunnel, because the lease behind them has not moved.
func (m *Manager) Endpoints() Endpoints {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.target == nil {
		return Endpoints{Mode: m.cfg.Mode}
	}
	return Endpoints{Mode: config.TunnelQuick, JupyterURL: m.quickURL}
}

// PointAt starts a quick tunnel for one lease's container and returns the
// addresses a renter should use.
func (m *Manager) PointAt(ctx context.Context, target Target) (Endpoints, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.pointQuickLocked(ctx, target)
}

// Clear withdraws the current lease's tunnel. A quick tunnel's process is the
// ingress, so withdrawing it means stopping it.
func (m *Manager) Clear(_ context.Context) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.target = nil
	m.quickURL = ""
	m.stopLocked()
}

// Stop shuts the tunnel down entirely. Called when the node itself is stopping.
func (m *Manager) Stop() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stopLocked()
}

// pointQuickLocked starts a throwaway tunnel for one lease's Jupyter server: no
// Cloudflare account, no domain, no API token, at the cost of a hostname that
// changes every lease and no SSH.
func (m *Manager) pointQuickLocked(ctx context.Context, target Target) (Endpoints, error) {
	m.stopLocked()

	cmd := exec.Command(m.binary(),
		"tunnel", "--no-autoupdate",
		"--url", "http://"+target.JupyterAddr,
	)
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return Endpoints{}, fmt.Errorf("capture cloudflared output: %w", err)
	}
	// cloudflared writes its banner to stderr, but be tolerant of a build that
	// logs the URL to stdout instead.
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return Endpoints{}, fmt.Errorf("capture cloudflared output: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return Endpoints{}, fmt.Errorf("start cloudflared: %w — is it installed?", err)
	}
	m.adoptLocked(cmd)

	url, err := m.awaitQuickURL(ctx, io.MultiReader(stderr, stdout))
	if err != nil {
		m.stopLocked()
		return Endpoints{}, err
	}

	// Give Cloudflare edge DNS 3 seconds to propagate before running the reachability check.
	select {
	case <-time.After(3 * time.Second):
	case <-ctx.Done():
		m.stopLocked()
		return Endpoints{}, ctx.Err()
	}

	m.target = &target
	m.quickURL = url
	m.log.Info("quick tunnel up", "lease", target.LeaseID, "url", url)

	return Endpoints{
		Mode:       config.TunnelQuick,
		JupyterURL: url,
		// Deliberately empty: a quick tunnel carries HTTP only, so there is no
		// SSH route to advertise. Saying otherwise would hand a renter a
		// hostname that silently never connects.
		SSHHost: "",
	}, nil
}

// quickURLPattern matches the hostname cloudflared prints for a quick tunnel.
var quickURLPattern = regexp.MustCompile(`https://[a-z0-9-]+\.trycloudflare\.com`)

// quickTunnelTimeout bounds how long a renter waits for a quick tunnel to come
// up before the lease is abandoned unsettled.
const quickTunnelTimeout = 45 * time.Second

// awaitQuickURL reads cloudflared's banner until the assigned hostname appears
// and the connection to Cloudflare's edge is registered.
//
// The output has to keep being drained afterwards, or cloudflared blocks on a
// full pipe and the tunnel dies partway through someone's paid session.
func (m *Manager) awaitQuickURL(ctx context.Context, output io.Reader) (string, error) {
	found := make(chan string, 1)

	go func() {
		scanner := bufio.NewScanner(output)
		scanner.Buffer(make([]byte, 0, 64*1024), 1<<20)
		var assignedURL string
		reported := false

		for scanner.Scan() {
			line := scanner.Text()
			if assignedURL == "" {
				if match := quickURLPattern.FindString(line); match != "" {
					assignedURL = match
				}
			}
			if assignedURL != "" && !reported {
				// Wait until cloudflared registers its edge connection so edge DNS is live.
				if strings.Contains(line, "Registered tunnel connection") || strings.Contains(line, "connection=") {
					found <- assignedURL
					reported = true
				}
			}
			m.log.Debug("cloudflared", "line", line)
		}
		if assignedURL != "" && !reported {
			found <- assignedURL
			reported = true
		} else if !reported {
			close(found)
		}
	}()

	ctx, cancel := context.WithTimeout(ctx, quickTunnelTimeout)
	defer cancel()

	select {
	case url, ok := <-found:
		if !ok {
			return "", errors.New("cloudflared exited before it published a tunnel hostname")
		}
		return url, nil
	case <-ctx.Done():
		return "", errors.New("cloudflared did not publish a tunnel hostname in time")
	}
}

// adoptLocked takes ownership of a started process and watches it exit.
func (m *Manager) adoptLocked(cmd *exec.Cmd) {
	stopped := make(chan struct{})
	m.cmd = cmd
	m.stopped = stopped

	go func() {
		err := cmd.Wait()
		close(stopped)
		// An exit we asked for closes the channel first and is not worth a
		// warning; anything else means a live lease just lost its transport.
		if err != nil {
			m.log.Warn("cloudflared exited", "error", err)
		}
	}()
}

func (m *Manager) stopLocked() {
	if m.cmd == nil {
		return
	}
	cmd, stopped := m.cmd, m.stopped
	m.cmd, m.stopped = nil, nil

	if cmd.Process != nil {
		_ = cmd.Process.Signal(syscall.SIGTERM)
	}
	select {
	case <-stopped:
	case <-time.After(10 * time.Second):
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
	}
}

func (m *Manager) binary() string {
	if m.cfg.Binary == "" {
		return "cloudflared"
	}
	return m.cfg.Binary
}

// Reachable confirms a renter could actually connect before they are charged.
//
// This runs between provisioning and settlement (implementation.md §5), and it
// is the reason a renter is never billed for a lease that never came up. A
// tunnel that has not finished registering and a cloudflared that died on
// startup look identical from here, and both mean the same thing: do not settle.
//
// Any HTTP response counts as reachable. Jupyter answers 302 or 403 without a
// token; what matters is that something on the far end answered at all, rather
// than the connection failing outright.
func (m *Manager) Reachable(ctx context.Context, ep Endpoints) error {
	if ep.JupyterURL == "" {
		return errors.New("no Jupyter endpoint to check")
	}

	client := &http.Client{
		Timeout: 15 * time.Second,
		// A redirect to a login page is proof enough that the far end is alive.
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	deadline := time.Now().Add(reachabilityTimeout)
	var lastErr error
	for attempt := 0; time.Now().Before(deadline); attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, ep.JupyterURL+"/api", nil)
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
		case <-time.After(2 * time.Second):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return fmt.Errorf("the lease never became reachable at %s: %w", ep.JupyterURL, lastErr)
}

// reachabilityTimeout bounds the wait in Reachable. Long enough for a cold
// cloudflared to register with the edge, short enough that the renter's signed
// payment payload has not expired by the time we settle it.
const reachabilityTimeout = 60 * time.Second
