// Package tunnel gets a renter to a lease container on a machine that has no
// public address.
//
// A provider's box is almost always behind NAT: that is the same assumption
// that makes the registry heartbeat push rather than poll. For batch jobs it
// does not matter, because the renter only ever talks to the node's own API.
// For a lease it matters completely — an SSH session and a Jupyter server both
// need the renter to reach *into* the provider's network.
//
// The answer is cloudflared, run by the node, so the provider's machine only
// ever makes outbound connections: no port forwarding, no public IP, and no
// always-on bastion VM for us to own and pay for. Two modes:
//
//	named  a tunnel the registry provisioned for this node on the platform's
//	       Cloudflare account, with stable DNS routes. SSH and Jupyter both work.
//	quick  `cloudflared tunnel --url`, which needs no Cloudflare account at all
//	       but hands back a random HTTP-only hostname per run. Jupyter works;
//	       SSH does not, because there is no TCP route.
//
// The design that sits on top of this — a node-local SSH CA issuing per-lease
// certificates — is identical in both modes. Only the transport underneath
// changes.
package tunnel

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/YashIIT0909/ClearGate/agent/internal/config"
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
	// SSHHost is the hostname to run `cloudflared access ssh` against. Empty
	// when the current mode cannot carry SSH.
	SSHHost string
	// JupyterURL is the base URL of the lease's Jupyter server, without the
	// token — the token is minted per lease and returned separately.
	JupyterURL string
	// Mode echoes which transport produced these, so the CLI can explain what
	// the renter can and cannot do.
	Mode string
}

// Manager owns this node's single cloudflared process.
//
// One process per node, not one per lease: reconnecting a tunnel takes seconds
// and flaps the renter's connection, so a named tunnel stays up between leases
// and only its ingress rules are rewritten (implementation.md §9). A quick
// tunnel cannot be reconfigured at all, so that mode does restart per lease.
type Manager struct {
	cfg     config.Tunnel
	apiPort string
	log     *slog.Logger

	mu       sync.Mutex
	cmd      *exec.Cmd
	stopped  chan struct{}
	target   *Target
	quickURL string
}

// New builds a manager. apiAddr is the node's own listen address, used for the
// optional third ingress rule that puts the x402 API itself behind the tunnel.
func New(cfg config.Tunnel, apiAddr string, log *slog.Logger) *Manager {
	port := apiAddr
	if index := strings.LastIndex(apiAddr, ":"); index >= 0 {
		port = apiAddr[index+1:]
	}
	return &Manager{cfg: cfg, apiPort: port, log: log}
}

// Mode reports which transport this manager is configured for.
func (m *Manager) Mode() string { return m.cfg.Mode }

// APIURL is the public address of this node's x402 API through the tunnel, or
// "" when the tunnel does not carry it.
//
// This is the piece that lets a provider behind NAT be reachable for payment at
// all. It is only used as the node's public_url when the operator opted in with
// leases.tunnel.derive_public_url — overriding an address a provider typed in
// themselves should be a choice, not a surprise (implementation.md §8.5).
func (m *Manager) APIURL() string {
	if m.cfg.Mode != config.TunnelNamed || m.cfg.APIHostname == "" {
		return ""
	}
	return "https://" + m.cfg.APIHostname
}

// Endpoints reports where the current lease is published, for a caller that did
// not do the publishing — an extension re-states the connection details without
// touching the tunnel, because the lease behind them has not moved.
func (m *Manager) Endpoints() Endpoints {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.target == nil {
		return Endpoints{Mode: m.cfg.Mode}
	}
	switch m.cfg.Mode {
	case config.TunnelQuick:
		return Endpoints{Mode: config.TunnelQuick, JupyterURL: m.quickURL}
	case config.TunnelNamed:
		return Endpoints{
			Mode:       config.TunnelNamed,
			SSHHost:    m.cfg.SSHHostname,
			JupyterURL: "https://" + m.cfg.JupyterHostname,
		}
	default:
		return Endpoints{
			Mode:       config.TunnelOff,
			SSHHost:    m.target.SSHAddr,
			JupyterURL: "http://" + m.target.JupyterAddr,
		}
	}
}

// Start brings up a named tunnel with no lease attached yet, so the API ingress
// is live from boot and the first lease does not pay for a cold start.
//
// Quick mode starts nothing here: its tunnel is per-lease by necessity.
func (m *Manager) Start(ctx context.Context) error {
	if m.cfg.Mode != config.TunnelNamed {
		return nil
	}
	if m.cfg.Token == "" {
		return errors.New("no tunnel token — the node needs one from the registry before it can accept leases")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.startNamedLocked(ctx)
}

// PointAt makes the tunnel serve one lease's container and returns the
// addresses a renter should use.
func (m *Manager) PointAt(ctx context.Context, target Target) (Endpoints, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	switch m.cfg.Mode {
	case config.TunnelOff:
		// No tunnel: the renter is expected to already have a route to this
		// host, which in practice means a LAN or a test rig.
		m.target = &target
		return Endpoints{
			Mode:       config.TunnelOff,
			SSHHost:    target.SSHAddr,
			JupyterURL: "http://" + target.JupyterAddr,
		}, nil

	case config.TunnelQuick:
		return m.pointQuickLocked(ctx, target)

	case config.TunnelNamed:
		return m.pointNamedLocked(ctx, target)

	default:
		return Endpoints{}, fmt.Errorf("unknown tunnel mode %q", m.cfg.Mode)
	}
}

// Clear withdraws the current lease's ingress.
//
// In named mode the process stays up and the rules become a catch-all 404, so
// the next lease does not wait for a reconnect. In quick mode the process is
// the ingress, so it is stopped.
func (m *Manager) Clear(ctx context.Context) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.target = nil
	m.quickURL = ""

	switch m.cfg.Mode {
	case config.TunnelQuick:
		m.stopLocked()
	case config.TunnelNamed:
		if m.cmd == nil {
			return
		}
		if err := m.writeConfig(nil); err != nil {
			m.log.Warn("could not withdraw tunnel ingress", "error", err)
			return
		}
		m.reloadLocked(ctx)
	}
}

// Stop shuts the tunnel down entirely. Called when the node itself is stopping.
func (m *Manager) Stop() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stopLocked()
}

// pointNamedLocked rewrites the ingress file and asks the running cloudflared
// to reload it. The process is started here if the node came up before its
// token arrived.
func (m *Manager) pointNamedLocked(ctx context.Context, target Target) (Endpoints, error) {
	if m.cfg.SSHHostname == "" || m.cfg.JupyterHostname == "" {
		return Endpoints{}, errors.New("this node has no tunnel hostnames yet; it cannot publish a lease")
	}
	if err := m.writeConfig(&target); err != nil {
		return Endpoints{}, err
	}
	if m.cmd == nil {
		if err := m.startNamedLocked(ctx); err != nil {
			return Endpoints{}, err
		}
	} else {
		m.reloadLocked(ctx)
	}

	m.target = &target
	return Endpoints{
		Mode:       config.TunnelNamed,
		SSHHost:    m.cfg.SSHHostname,
		JupyterURL: "https://" + m.cfg.JupyterHostname,
	}, nil
}

// pointQuickLocked starts a throwaway tunnel for one lease's Jupyter server.
//
// This is the zero-setup path: no Cloudflare account, no domain, no API token.
// It is what makes the feature demoable before the platform's Cloudflare
// plumbing exists (implementation.md §10), at the cost of a hostname that
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

// awaitQuickURL reads cloudflared's banner until the assigned hostname appears.
//
// The output has to keep being drained afterwards, or cloudflared blocks on a
// full pipe and the tunnel dies partway through someone's paid session.
func (m *Manager) awaitQuickURL(ctx context.Context, output io.Reader) (string, error) {
	found := make(chan string, 1)

	go func() {
		scanner := bufio.NewScanner(output)
		scanner.Buffer(make([]byte, 0, 64*1024), 1<<20)
		reported := false
		for scanner.Scan() {
			line := scanner.Text()
			if !reported {
				if match := quickURLPattern.FindString(line); match != "" {
					found <- match
					reported = true
					continue
				}
			}
			m.log.Debug("cloudflared", "line", line)
		}
		if !reported {
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

// startNamedLocked launches cloudflared against this node's token.
func (m *Manager) startNamedLocked(ctx context.Context) error {
	if m.cmd != nil {
		return nil
	}
	if err := m.writeConfig(m.target); err != nil {
		return err
	}

	cmd := exec.Command(m.binary(),
		"tunnel", "--no-autoupdate",
		"--config", m.configPath(),
		"run", "--token", m.cfg.Token,
	)
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("capture cloudflared output: %w", err)
	}
	cmd.Stdout = nil
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start cloudflared: %w — is it installed?", err)
	}
	m.adoptLocked(cmd)

	go m.drain(stderr)
	m.log.Info("named tunnel starting",
		"ssh", m.cfg.SSHHostname, "jupyter", m.cfg.JupyterHostname, "api", m.cfg.APIHostname)

	// cloudflared registers with Cloudflare's edge before it will serve
	// anything. There is no readiness signal worth parsing, so give it a beat
	// rather than have the first request race the first connection — the
	// reachability check in the lease handler is what actually decides whether
	// the renter gets charged.
	select {
	case <-time.After(3 * time.Second):
	case <-ctx.Done():
	}
	return nil
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

func (m *Manager) drain(r io.Reader) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for scanner.Scan() {
		m.log.Debug("cloudflared", "line", scanner.Text())
	}
}

// reloadLocked asks a running cloudflared to re-read its ingress rules. This is
// what keeps a tunnel connected across leases instead of reconnecting for each.
func (m *Manager) reloadLocked(ctx context.Context) {
	if m.cmd == nil || m.cmd.Process == nil {
		return
	}
	if err := m.cmd.Process.Signal(syscall.SIGHUP); err != nil {
		m.log.Warn("could not reload cloudflared ingress; restarting it", "error", err)
		m.stopLocked()
		if err := m.startNamedLocked(ctx); err != nil {
			m.log.Error("could not restart cloudflared", "error", err)
		}
	}
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

func (m *Manager) configPath() string {
	return filepath.Join(m.cfg.ConfigDir, "config.yml")
}

// writeConfig renders cloudflared's ingress rules.
//
// A nil target means "no lease right now": the lease hostnames answer 404
// rather than being left pointed at a container that has been reaped. The
// trailing catch-all is required by cloudflared — a config without one is
// rejected outright.
func (m *Manager) writeConfig(target *Target) error {
	if err := os.MkdirAll(m.cfg.ConfigDir, 0o700); err != nil {
		return fmt.Errorf("create tunnel config directory: %w", err)
	}

	var b strings.Builder
	b.WriteString("# Generated by cleargate-node. Edits are overwritten on the next lease.\n")
	b.WriteString("no-autoupdate: true\n")
	b.WriteString("ingress:\n")

	if target != nil {
		fmt.Fprintf(&b, "  - hostname: %s\n    service: tcp://%s\n", m.cfg.SSHHostname, target.SSHAddr)
		fmt.Fprintf(&b, "  - hostname: %s\n    service: http://%s\n", m.cfg.JupyterHostname, target.JupyterAddr)
	}
	if m.cfg.APIHostname != "" {
		// The x402 API itself, so a provider behind NAT can be paid without
		// port forwarding — the same problem the lease hostnames solve.
		fmt.Fprintf(&b, "  - hostname: %s\n    service: http://127.0.0.1:%s\n", m.cfg.APIHostname, m.apiPort)
	}
	b.WriteString("  - service: http_status:404\n")

	path := m.configPath()
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

// Reachable confirms a renter could actually connect before they are charged.
//
// This runs between provisioning and settlement (implementation.md §5), and it
// is the reason a renter is never billed for a lease that never came up. A
// tunnel that has not finished registering, a cloudflared that died on startup,
// a DNS route the registry failed to create — all of them look identical from
// here, and all of them mean the same thing: do not settle.
//
// Any HTTP response counts as reachable. Jupyter answers 302 or 403 without a
// token and cloudflared answers 502 for a TCP hostname asked to speak HTTP;
// what matters is that something on the far end answered at all, rather than
// the connection failing outright.
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
