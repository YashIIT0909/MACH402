// Package runner starts and supervises lease containers.
//
// It speaks the Docker HTTP API directly over the unix socket rather than
// pulling in the Docker SDK: the agent ships as a single small static binary,
// and the handful of endpoints used here do not justify the dependency weight.
package runner

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// dockerAPIVersion is pinned so a daemon upgrade cannot silently change
// response shapes underneath the agent.
const dockerAPIVersion = "v1.44"

// Docker is a minimal client for the local Docker daemon.
type Docker struct {
	http *http.Client
	// host is the scheme-and-authority used in request URLs. For a unix socket
	// this is a placeholder; the dialer ignores it.
	host string
}

// NewDocker connects to a daemon at dockerHost, e.g. "unix:///var/run/docker.sock"
// or "tcp://127.0.0.1:2375".
func NewDocker(dockerHost string) (*Docker, error) {
	switch {
	case strings.HasPrefix(dockerHost, "unix://"):
		socket := strings.TrimPrefix(dockerHost, "unix://")
		transport := &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				var dialer net.Dialer
				return dialer.DialContext(ctx, "unix", socket)
			},
		}
		return &Docker{
			http: &http.Client{Transport: transport},
			host: "http://docker",
		}, nil

	case strings.HasPrefix(dockerHost, "tcp://"):
		return &Docker{
			http: &http.Client{},
			host: "http://" + strings.TrimPrefix(dockerHost, "tcp://"),
		}, nil

	default:
		return nil, fmt.Errorf("unsupported docker_host %q: use unix:// or tcp://", dockerHost)
	}
}

func (d *Docker) urlFor(path string, query url.Values) string {
	u := d.host + "/" + dockerAPIVersion + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	return u
}

func (d *Docker) do(ctx context.Context, method, path string, query url.Values, body any) (*http.Response, error) {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("encode request: %w", err)
		}
		reader = strings.NewReader(string(raw))
	}

	req, err := http.NewRequestWithContext(ctx, method, d.urlFor(path, query), reader)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := d.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("docker %s %s: %w", method, path, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		defer resp.Body.Close()
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("docker %s %s returned %d: %s", method, path, resp.StatusCode, strings.TrimSpace(string(msg)))
	}
	return resp, nil
}

func (d *Docker) doJSON(ctx context.Context, method, path string, query url.Values, body, out any) error {
	resp, err := d.do(ctx, method, path, query, body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if out == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode docker response: %w", err)
	}
	return nil
}

// Info is the subset of GET /info the agent cares about.
type Info struct {
	ServerVersion string         `json:"ServerVersion"`
	Runtimes      map[string]any `json:"Runtimes"`
	Driver        string         `json:"Driver"`
	NCPU          int            `json:"NCPU"`
	MemTotal      int64          `json:"MemTotal"`
	Labels        []string       `json:"Labels"`
	Extra         map[string]any `json:"-"`
}

// Ping checks the daemon is reachable.
func (d *Docker) Ping(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	resp, err := d.do(ctx, http.MethodGet, "/_ping", nil, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return nil
}

// Info reports daemon capabilities, including installed runtimes.
func (d *Docker) Info(ctx context.Context) (*Info, error) {
	var info Info
	if err := d.doJSON(ctx, http.MethodGet, "/info", nil, nil, &info); err != nil {
		return nil, err
	}
	return &info, nil
}

// PullImage fetches an image, streaming until the pull completes. Docker
// streams progress as newline-delimited JSON and only reports failure inside
// that stream, so the body must be drained and inspected, not discarded.
func (d *Docker) PullImage(ctx context.Context, image string) error {
	query := url.Values{"fromImage": {image}}
	resp, err := d.do(ctx, http.MethodPost, "/images/create", query, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	decoder := json.NewDecoder(resp.Body)
	for {
		var event struct {
			Error string `json:"error"`
		}
		if err := decoder.Decode(&event); err != nil {
			if err == io.EOF {
				return nil
			}
			return fmt.Errorf("pull %s: %w", image, err)
		}
		if event.Error != "" {
			return fmt.Errorf("pull %s: %s", image, event.Error)
		}
	}
}

// HasImage reports whether an image is already present locally.
func (d *Docker) HasImage(ctx context.Context, image string) bool {
	resp, err := d.do(ctx, http.MethodGet, "/images/"+url.PathEscape(image)+"/json", nil, nil)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return true
}

// ImageEnv returns the environment baked into an image's config.
//
// Used to tell a CUDA-capable lease image from a plain one without starting a
// container: the NVIDIA container images declare NVIDIA_DRIVER_CAPABILITIES and
// CUDA_VERSION here, and a base with neither cannot compute on a GPU no matter
// how many devices are passed into it.
func (d *Docker) ImageEnv(ctx context.Context, image string) ([]string, error) {
	var out struct {
		Config struct {
			Env []string `json:"Env"`
		} `json:"Config"`
	}
	if err := d.doJSON(ctx, http.MethodGet, "/images/"+url.PathEscape(image)+"/json", nil, nil, &out); err != nil {
		return nil, err
	}
	return out.Config.Env, nil
}

// DeviceRequest asks for GPU passthrough. Only ever populated when the nvidia
// runtime is actually installed.
type DeviceRequest struct {
	Driver       string     `json:"Driver"`
	Count        int        `json:"Count"`
	Capabilities [][]string `json:"Capabilities"`
}

// Mount attaches a volume into the container.
type Mount struct {
	Type   string `json:"Type"`
	Source string `json:"Source"`
	Target string `json:"Target"`
}

// HostConfig is the sandbox. Every field here is a containment decision:
// no network, capped memory and CPU, capped process count, and a read-only
// root filesystem with exactly one writable mount (CLAUDE.md invariant 6).
type HostConfig struct {
	NetworkMode    string          `json:"NetworkMode"`
	Memory         int64           `json:"Memory"`
	NanoCPUs       int64           `json:"NanoCpus"`
	PidsLimit      int64           `json:"PidsLimit"`
	ReadonlyRootfs bool            `json:"ReadonlyRootfs"`
	Mounts         []Mount         `json:"Mounts"`
	DeviceRequests []DeviceRequest `json:"DeviceRequests,omitempty"`
	AutoRemove     bool            `json:"AutoRemove"`
	CapDrop        []string        `json:"CapDrop"`
	// CapAdd stays as close to empty as it can for leases: sshd needs a handful of capabilities to drop privileges into the
	// renter's account, and nothing beyond those is ever granted.
	CapAdd      []string          `json:"CapAdd,omitempty"`
	SecurityOpt []string          `json:"SecurityOpt"`
	Tmpfs       map[string]string `json:"Tmpfs,omitempty"`
}

// CreateContainerRequest is the body of POST /containers/create.
type CreateContainerRequest struct {
	Image      string            `json:"Image"`
	Cmd        []string          `json:"Cmd,omitempty"`
	Env        []string          `json:"Env,omitempty"`
	WorkingDir string            `json:"WorkingDir,omitempty"`
	Labels     map[string]string `json:"Labels,omitempty"`
	HostConfig HostConfig        `json:"HostConfig"`
	// NetworkingConfig attaches the container to a user-defined network at
	// creation time. Attaching afterwards would leave a window in which the
	// container is on the default bridge with unrestricted egress.
	NetworkingConfig *NetworkingConfig `json:"NetworkingConfig,omitempty"`
}

// NetworkingConfig names the networks a container joins at creation.
type NetworkingConfig struct {
	EndpointsConfig map[string]EndpointConfig `json:"EndpointsConfig"`
}

// EndpointConfig is one network attachment. Aliases are the names other
// containers on the same network can resolve it by.
type EndpointConfig struct {
	Aliases []string `json:"Aliases,omitempty"`
}

type createContainerResponse struct {
	ID       string   `json:"Id"`
	Warnings []string `json:"Warnings"`
}

// CreateContainer creates a container and returns its id.
func (d *Docker) CreateContainer(ctx context.Context, name string, req CreateContainerRequest) (string, error) {
	var out createContainerResponse
	query := url.Values{}
	if name != "" {
		query.Set("name", name)
	}
	if err := d.doJSON(ctx, http.MethodPost, "/containers/create", query, req, &out); err != nil {
		return "", err
	}
	return out.ID, nil
}

// StartContainer starts a created container.
func (d *Docker) StartContainer(ctx context.Context, id string) error {
	return d.doJSON(ctx, http.MethodPost, "/containers/"+id+"/start", nil, nil, nil)
}

// KillContainer sends a signal to a running container.
func (d *Docker) KillContainer(ctx context.Context, id, signal string) error {
	query := url.Values{"signal": {signal}}
	return d.doJSON(ctx, http.MethodPost, "/containers/"+id+"/kill", query, nil, nil)
}

// RemoveContainer deletes a container and its anonymous volumes.
func (d *Docker) RemoveContainer(ctx context.Context, id string) error {
	query := url.Values{"v": {"1"}, "force": {"1"}}
	return d.doJSON(ctx, http.MethodDelete, "/containers/"+id, query, nil, nil)
}

// WaitContainer blocks until the container exits and returns its exit code.
func (d *Docker) WaitContainer(ctx context.Context, id string) (int, error) {
	var out struct {
		StatusCode int `json:"StatusCode"`
		Error      *struct {
			Message string `json:"Message"`
		} `json:"Error"`
	}
	if err := d.doJSON(ctx, http.MethodPost, "/containers/"+id+"/wait", nil, nil, &out); err != nil {
		return -1, err
	}
	if out.Error != nil && out.Error.Message != "" {
		return out.StatusCode, fmt.Errorf("container wait: %s", out.Error.Message)
	}
	return out.StatusCode, nil
}

// PauseContainer freezes every process in a container through the cgroup
// freezer. Used when a lease's paid time lapses: the renter's work is still
// there, using memory but no CPU, until they either top up or the grace period
// runs out. Killing on the first missed payment would throw away work someone
// was in the middle of.
func (d *Docker) PauseContainer(ctx context.Context, id string) error {
	return d.doJSON(ctx, http.MethodPost, "/containers/"+id+"/pause", nil, nil, nil)
}

// UnpauseContainer thaws a frozen container after a lease is extended.
func (d *Docker) UnpauseContainer(ctx context.Context, id string) error {
	return d.doJSON(ctx, http.MethodPost, "/containers/"+id+"/unpause", nil, nil, nil)
}

// ContainerIP returns the container's address on a named network.
//
// The tunnel dials this directly rather than a published host port. A lease
// container lives on an internal network with no route off the box, and the
// host can still reach it because traffic from the host to its own bridge is
// delivered locally rather than forwarded — which is exactly the traffic the
// internal flag blocks.
func (d *Docker) ContainerIP(ctx context.Context, id, network string) (string, error) {
	var out struct {
		NetworkSettings struct {
			Networks map[string]struct {
				IPAddress string `json:"IPAddress"`
			} `json:"Networks"`
		} `json:"NetworkSettings"`
	}
	if err := d.doJSON(ctx, http.MethodGet, "/containers/"+id+"/json", nil, nil, &out); err != nil {
		return "", err
	}
	endpoint, ok := out.NetworkSettings.Networks[network]
	if !ok || endpoint.IPAddress == "" {
		return "", fmt.Errorf("container has no address on network %s", network)
	}
	return endpoint.IPAddress, nil
}

// CreateNetwork creates a user-defined bridge network.
//
// internal is the containment primitive leases are built on: an internal
// network has no route to anything outside itself, so a container on it reaches
// the internet only through something dual-homed that we control.
func (d *Docker) CreateNetwork(ctx context.Context, name string, internal bool, labels map[string]string) error {
	body := map[string]any{
		"Name":     name,
		"Driver":   "bridge",
		"Internal": internal,
		"Labels":   labels,
	}
	return d.doJSON(ctx, http.MethodPost, "/networks/create", nil, body, nil)
}

// ConnectNetwork attaches an existing container to another network. Used to
// give the egress proxy a second leg on the default bridge, so it — and only it
// — can reach the outside world.
func (d *Docker) ConnectNetwork(ctx context.Context, network, containerID string, aliases []string) error {
	body := map[string]any{
		"Container":      containerID,
		"EndpointConfig": EndpointConfig{Aliases: aliases},
	}
	return d.doJSON(ctx, http.MethodPost, "/networks/"+url.PathEscape(network)+"/connect", nil, body, nil)
}

// RemoveNetwork deletes a network. Every container on it must be gone first.
func (d *Docker) RemoveNetwork(ctx context.Context, name string) error {
	return d.doJSON(ctx, http.MethodDelete, "/networks/"+url.PathEscape(name), nil, nil, nil)
}

// ListNetworksByLabel returns every network carrying a label, for the orphan
// sweep at startup.
func (d *Docker) ListNetworksByLabel(ctx context.Context, label string) ([]string, error) {
	query := url.Values{"filters": {fmt.Sprintf(`{"label":[%q]}`, label)}}
	var out []struct {
		Name string `json:"Name"`
	}
	if err := d.doJSON(ctx, http.MethodGet, "/networks", query, nil, &out); err != nil {
		return nil, err
	}
	names := make([]string, 0, len(out))
	for _, network := range out {
		names = append(names, network.Name)
	}
	return names, nil
}

// CreateVolume creates a named volume, such as a lease's workspace.
func (d *Docker) CreateVolume(ctx context.Context, name string, labels map[string]string) error {
	body := map[string]any{"Name": name, "Labels": labels}
	return d.doJSON(ctx, http.MethodPost, "/volumes/create", nil, body, nil)
}

// RemoveVolume deletes a volume. Output is wiped once downloaded.
func (d *Docker) RemoveVolume(ctx context.Context, name string) error {
	query := url.Values{"force": {"1"}}
	return d.doJSON(ctx, http.MethodDelete, "/volumes/"+url.PathEscape(name), query, nil, nil)
}

// PutArchive uploads a tar into the container filesystem at path. Used to place
// the renter's script before start, so no shell interpolation is ever involved.
func (d *Docker) PutArchive(ctx context.Context, id, path string, tarball io.Reader) error {
	query := url.Values{"path": {path}, "noOverwriteDirNonDir": {"1"}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, d.urlFor("/containers/"+id+"/archive", query), tarball)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-tar")

	resp, err := d.http.Do(req)
	if err != nil {
		return fmt.Errorf("docker put archive: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("docker put archive returned %d: %s", resp.StatusCode, strings.TrimSpace(string(msg)))
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	return nil
}

// GetArchive streams a directory out of the container as a tar. The caller owns
// closing the returned reader.
func (d *Docker) GetArchive(ctx context.Context, id, path string) (io.ReadCloser, error) {
	query := url.Values{"path": {path}}
	resp, err := d.do(ctx, http.MethodGet, "/containers/"+id+"/archive", query, nil)
	if err != nil {
		return nil, err
	}
	return resp.Body, nil
}

// Logs opens a log stream. The caller owns closing the returned reader.
func (d *Docker) Logs(ctx context.Context, id string, follow bool) (io.ReadCloser, error) {
	query := url.Values{
		"stdout": {"1"},
		"stderr": {"1"},
	}
	if follow {
		query.Set("follow", "1")
	}
	resp, err := d.do(ctx, http.MethodGet, "/containers/"+id+"/logs", query, nil)
	if err != nil {
		return nil, err
	}
	return resp.Body, nil
}

// LogFrame is one demultiplexed chunk of container output.
type LogFrame struct {
	// Stream is "stdout" or "stderr".
	Stream string
	Data   []byte
}

// readLogFrame reads one frame from Docker's multiplexed log stream.
//
// Without a TTY, Docker prefixes every chunk with an 8-byte header: one byte of
// stream id, three padding bytes, then a big-endian uint32 length. Reading the
// stream as plain text corrupts output with these headers, which is why this
// exists rather than an io.Copy.
func readLogFrame(r io.Reader) (LogFrame, error) {
	var header [8]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return LogFrame{}, err
	}

	stream := "stdout"
	if header[0] == 2 {
		stream = "stderr"
	}

	length := binary.BigEndian.Uint32(header[4:8])
	if length == 0 {
		return LogFrame{Stream: stream}, nil
	}
	// A corrupt header must not turn into an unbounded allocation.
	if length > 1<<20 {
		length = 1 << 20
	}

	data := make([]byte, length)
	if _, err := io.ReadFull(r, data); err != nil {
		return LogFrame{}, err
	}
	return LogFrame{Stream: stream, Data: data}, nil
}

// containerSummary is the subset of GET /containers/json used by the sweeper.
type containerSummary struct {
	ID    string   `json:"Id"`
	Names []string `json:"Names"`
	State string   `json:"State"`
}

// ListContainersByLabel returns every container, running or not, carrying a label.
func (d *Docker) ListContainersByLabel(ctx context.Context, label string) ([]containerSummary, error) {
	query := url.Values{
		"all":     {"1"},
		"filters": {fmt.Sprintf(`{"label":[%q]}`, label)},
	}
	var out []containerSummary
	if err := d.doJSON(ctx, http.MethodGet, "/containers/json", query, nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// ListVolumesByLabel returns every volume carrying a label.
func (d *Docker) ListVolumesByLabel(ctx context.Context, label string) ([]string, error) {
	query := url.Values{"filters": {fmt.Sprintf(`{"label":[%q]}`, label)}}
	var out struct {
		Volumes []struct {
			Name string `json:"Name"`
		} `json:"Volumes"`
	}
	if err := d.doJSON(ctx, http.MethodGet, "/volumes", query, nil, &out); err != nil {
		return nil, err
	}
	names := make([]string, 0, len(out.Volumes))
	for _, volume := range out.Volumes {
		names = append(names, volume.Name)
	}
	return names, nil
}
