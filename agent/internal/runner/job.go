package runner

import (
	"context"
	"crypto/subtle"
	"sync"
	"time"
)

// Status is a point in the job lifecycle.
//
//	pending -> staging -> running -> succeeded | failed | timeout | killed
//
// staging covers pulling the image and downloading the renter's dataset, which
// happen after settlement: they can take minutes, and the signed payment
// payload expires long before a large download would finish.
//
// Only the runner mutates it, always under the job's lock.
type Status string

const (
	StatusPending   Status = "pending"
	StatusStaging   Status = "staging"
	StatusRunning   Status = "running"
	StatusSucceeded Status = "succeeded"
	StatusFailed    Status = "failed"
	StatusTimeout   Status = "timeout"
	StatusKilled    Status = "killed"
)

// IsTerminal reports whether a job has finished and its artifact is final.
func (s Status) IsTerminal() bool {
	switch s {
	case StatusSucceeded, StatusFailed, StatusTimeout, StatusKilled:
		return true
	default:
		return false
	}
}

// Spec is what a renter asked to run. It arrives in the body of a paid
// POST /v1/jobs.
type Spec struct {
	Image      string            `json:"image"`
	Cmd        []string          `json:"cmd,omitempty"`
	Env        map[string]string `json:"env,omitempty"`
	Script     *Script           `json:"script,omitempty"`
	Dataset    *Dataset          `json:"dataset,omitempty"`
	Timeout    int               `json:"timeout_seconds,omitempty"`
	RequireGPU bool              `json:"require_gpu,omitempty"`
}

// Dataset is training data the node downloads on the renter's behalf and mounts
// at /data.
//
// The node fetches it, never the container: jobs run with no network at all
// (CLAUDE.md invariant 6), so this is the only way data gets in. The URL is
// preflighted before the renter is asked to pay and downloaded after settlement.
type Dataset struct {
	URL string `json:"url"`
	// SHA256, when given, is verified as the file streams in. A mismatch fails
	// the job rather than training on something the renter did not ask for.
	SHA256 string `json:"sha256,omitempty"`
	// Filename overrides the name taken from the URL.
	Filename string `json:"filename,omitempty"`
	// Extract unpacks .tar/.tar.gz/.tgz/.zip into /data. Defaults to true; set
	// NoExtract to keep the archive intact.
	NoExtract bool `json:"no_extract,omitempty"`
}

// Script is an optional file dropped into the container's working directory
// before it starts. Base64 so binary content and newlines survive JSON.
type Script struct {
	Filename      string `json:"filename"`
	ContentBase64 string `json:"content_base64"`
}

// Job is one unit of paid work and its live state.
type Job struct {
	ID string

	mu          sync.RWMutex
	status      Status
	stage       string
	image       string
	gpu         bool
	exitCode    *int
	startedAt   *time.Time
	endedAt     *time.Time
	err         string
	containerID string
	volumeName  string
	artifact    bool

	// token authorizes reading this job's logs and artifact. It is minted at
	// payment and scoped to this job alone.
	token string

	// cancelStage stops the background goroutine that pulls the image and
	// downloads the dataset. Without it a killed job would keep downloading —
	// and then start — after the node decided not to run it.
	cancelStage context.CancelFunc

	logs *logBroker
	done chan struct{}
}

// State is the read-only view returned by GET /v1/jobs/:id.
type State struct {
	JobID         string  `json:"job_id"`
	Status        Status  `json:"status"`
	ExitCode      *int    `json:"exit_code"`
	StartedAt     *string `json:"started_at"`
	EndedAt       *string `json:"ended_at"`
	Error         *string `json:"error"`
	ArtifactReady bool    `json:"artifact_ready"`
	// Stage describes what a staging job is currently doing, e.g.
	// "downloading dataset 240.0 MiB / 1.2 GiB". Empty once running.
	Stage string `json:"stage,omitempty"`
	// Image and GPU let the provider's dashboard show what it is running.
	Image string `json:"image,omitempty"`
	GPU   bool   `json:"gpu,omitempty"`
}

func newJob(id, token string, spec Spec, gpu bool) *Job {
	return &Job{
		ID:     id,
		status: StatusPending,
		image:  spec.Image,
		gpu:    gpu,
		token:  token,
		logs:   newLogBroker(),
		done:   make(chan struct{}),
	}
}

// State snapshots the job for an API response.
func (j *Job) State() State {
	j.mu.RLock()
	defer j.mu.RUnlock()

	state := State{
		JobID:         j.ID,
		Status:        j.status,
		ExitCode:      j.exitCode,
		ArtifactReady: j.artifact,
		Stage:         j.stage,
		Image:         j.image,
		GPU:           j.gpu,
	}
	if j.startedAt != nil {
		formatted := j.startedAt.UTC().Format(time.RFC3339)
		state.StartedAt = &formatted
	}
	if j.endedAt != nil {
		formatted := j.endedAt.UTC().Format(time.RFC3339)
		state.EndedAt = &formatted
	}
	if j.err != "" {
		message := j.err
		state.Error = &message
	}
	return state
}

// Status reads the current status.
func (j *Job) Status() Status {
	j.mu.RLock()
	defer j.mu.RUnlock()
	return j.status
}

// Done is closed when the job reaches a terminal state.
func (j *Job) Done() <-chan struct{} { return j.done }

// publishLog pushes one line into the job's stream. Staging progress goes
// through here so the renter sees the same feed before and after the container
// starts, rather than an unexplained silence after paying.
func (j *Job) publishLog(stream, text string) {
	j.logs.publish(LogLine{Stream: stream, Text: text})
}

// Logs returns the retained output.
func (j *Job) Logs() []LogLine { return j.logs.snapshot() }

// Subscribe returns the backlog and a channel of subsequent lines.
func (j *Job) Subscribe() ([]LogLine, chan LogLine) { return j.logs.subscribe() }

// Unsubscribe releases a subscription.
func (j *Job) Unsubscribe(ch chan LogLine) { j.logs.unsubscribe(ch) }

// AuthorizedBy reports whether a bearer token may read this job. The comparison
// is constant time so a caller cannot discover a token byte by byte.
func (j *Job) AuthorizedBy(token string) bool {
	j.mu.RLock()
	defer j.mu.RUnlock()
	return constantTimeEqual(j.token, token)
}

// setStaging moves the job into staging and records what it is waiting on, so
// the renter's log stream and the provider's dashboard both show progress
// rather than an unexplained pause after payment.
func (j *Job) setStaging(detail string) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.status.IsTerminal() {
		return
	}
	j.status = StatusStaging
	j.stage = detail
}

func (j *Job) setRunning(containerID, volumeName string) {
	j.mu.Lock()
	defer j.mu.Unlock()
	now := time.Now()
	j.status = StatusRunning
	j.stage = ""
	j.containerID = containerID
	j.volumeName = volumeName
	j.startedAt = &now
}

// setCancelStage records how to abort this job's staging goroutine.
func (j *Job) setCancelStage(cancel context.CancelFunc) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.cancelStage = cancel
}

// abortStage stops staging if it is still running, and releases the staging
// context either way. Safe to call more than once.
func (j *Job) abortStage() {
	j.mu.Lock()
	cancel := j.cancelStage
	j.cancelStage = nil
	j.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// Image is the container image this job runs.
func (j *Job) Image() string {
	j.mu.RLock()
	defer j.mu.RUnlock()
	return j.image
}

// finish moves the job to a terminal state exactly once.
func (j *Job) finish(status Status, exitCode *int, err error) {
	j.mu.Lock()
	if j.status.IsTerminal() {
		j.mu.Unlock()
		return
	}
	now := time.Now()
	j.status = status
	j.stage = ""
	j.exitCode = exitCode
	j.endedAt = &now
	if err != nil {
		j.err = err.Error()
	}
	j.mu.Unlock()

	j.logs.close()
	close(j.done)
}

func (j *Job) setArtifactReady(ready bool) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.artifact = ready
}

func (j *Job) container() string {
	j.mu.RLock()
	defer j.mu.RUnlock()
	return j.containerID
}

// constantTimeEqual compares two tokens without leaking their contents through
// timing. An empty expected token never authorizes anything.
func constantTimeEqual(expected, given string) bool {
	if expected == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(expected), []byte(given)) == 1
}
