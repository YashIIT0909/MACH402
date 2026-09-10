package runner

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/YashIIT0909/ClearGate/agent/internal/config"
	"github.com/YashIIT0909/ClearGate/agent/internal/fetch"
)

// workDir is where a job's script lands and where the container starts.
const workDir = "/work"

// outDir is the writable mount whose contents come back as the artifact.
const outDir = "/out"

// dataDir is where a renter's dataset is staged. The node downloads it; the
// container, which has no network, only ever reads it.
const dataDir = "/data"

// jobLabel marks every container and volume ClearGate creates, so orphans left
// by a crash can be found and removed without touching anything else on the box.
const jobLabel = "cleargate.job"

// Runner owns every job container on this node.
type Runner struct {
	docker  *Docker
	cfg     config.Config
	gpu     GPU
	log     *slog.Logger
	fetcher *fetch.Fetcher
	events  *eventBroker

	mu   sync.RWMutex
	jobs map[string]*Job
}

// New builds a Runner and reports what sandbox it will actually provide.
func New(ctx context.Context, cfg config.Config, log *slog.Logger) (*Runner, error) {
	docker, err := NewDocker(cfg.DockerHost)
	if err != nil {
		return nil, err
	}
	if err := docker.Ping(ctx); err != nil {
		return nil, fmt.Errorf("cannot reach the Docker daemon at %s: %w", cfg.DockerHost, err)
	}

	gpu := DetectGPU(ctx, docker, cfg.GPUEnabled)
	if gpu.Available {
		log.Info("gpu enabled", "model", gpu.Model, "vram_mb", gpu.VRAMMb)
	} else {
		// Say this loudly: an operator who thinks they are renting out a GPU and
		// is actually selling CPU time will find out from an angry renter.
		log.Warn("running in CPU-fallback mode", "reason", gpu.Reason)
	}

	run := &Runner{
		docker: docker,
		cfg:    cfg,
		gpu:    gpu,
		log:    log,
		fetcher: fetch.New(fetch.Options{
			MaxBytes:      int64(cfg.Dataset.MaxMB) << 20,
			Timeout:       time.Duration(cfg.Dataset.TimeoutSeconds) * time.Second,
			AllowHTTP:     cfg.Dataset.AllowHTTP,
			AllowPrivate:  cfg.Dataset.AllowPrivate,
			HostAllowlist: cfg.Dataset.HostAllowlist,
		}),
		events: newEventBroker(),
		jobs:   make(map[string]*Job),
	}

	// Job state lives in memory, so nothing here survives a restart. Anything
	// still labelled as ours is therefore an orphan from a crash or a kill
	// during the artifact retention window, and would otherwise sit on the
	// provider's disk forever holding another renter's data.
	run.sweepOrphans(ctx)

	return run, nil
}

// sweepOrphans removes ClearGate containers and volumes left over from a
// previous process. Called once at startup, never while jobs are live.
func (r *Runner) sweepOrphans(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()

	containers, err := r.docker.ListContainersByLabel(ctx, jobLabel)
	if err != nil {
		r.log.Warn("could not list orphaned containers", "error", err)
	}
	for _, container := range containers {
		if err := r.docker.RemoveContainer(ctx, container.ID); err != nil {
			r.log.Warn("could not remove orphaned container", "container", container.ID, "error", err)
			continue
		}
		r.log.Info("removed orphaned container", "container", container.ID[:12])
	}

	volumes, err := r.docker.ListVolumesByLabel(ctx, jobLabel)
	if err != nil {
		r.log.Warn("could not list orphaned volumes", "error", err)
	}
	for _, volume := range volumes {
		if err := r.docker.RemoveVolume(ctx, volume); err != nil {
			r.log.Warn("could not remove orphaned volume", "volume", volume, "error", err)
			continue
		}
		r.log.Info("removed orphaned volume", "volume", volume)
	}
}

// GPU reports the node's actual GPU capability, for GET /v1/specs.
func (r *Runner) GPU() GPU { return r.gpu }

// Get looks up a job by id.
func (r *Runner) Get(id string) (*Job, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	job, ok := r.jobs[id]
	return job, ok
}

// ValidateSpec rejects a job before any payment is verified, so a renter never
// pays for work this node was always going to refuse.
func (r *Runner) ValidateSpec(spec Spec) error {
	return r.ValidateSpecContext(context.Background(), spec)
}

// ValidateSpecContext checks a job spec before the renter is asked to pay.
//
// Everything rejected here costs the renter nothing: no challenge is issued, no
// signature is made, nothing reaches the chain. That is why the dataset URL is
// preflighted here rather than at download time — a typo, a 404 or an oversized
// file must not be discovered after settlement, when the money has moved and
// there are no refunds.
func (r *Runner) ValidateSpecContext(ctx context.Context, spec Spec) error {
	if spec.Image == "" {
		return errors.New("image is required")
	}
	if spec.RequireGPU && !r.gpu.Available {
		return fmt.Errorf("this job requires a GPU, but this node is in CPU-fallback mode (%s)", r.gpu.Reason)
	}
	if !r.cfg.AllowsImage(spec.Image) {
		return fmt.Errorf("image %q is not in this node's allowlist (%s)",
			spec.Image, strings.Join(r.cfg.ImageAllowlist, ", "))
	}
	if spec.Script != nil {
		if spec.Script.Filename == "" {
			return errors.New("script.filename is required when a script is supplied")
		}
		if path.Base(spec.Script.Filename) != spec.Script.Filename {
			return fmt.Errorf("script.filename %q must be a bare filename, not a path", spec.Script.Filename)
		}
		if _, err := base64.StdEncoding.DecodeString(spec.Script.ContentBase64); err != nil {
			return fmt.Errorf("script.content_base64 is not valid base64: %w", err)
		}
	}
	if spec.Timeout < 0 {
		return errors.New("timeout_seconds cannot be negative")
	}
	if spec.Dataset != nil {
		if spec.Dataset.URL == "" {
			return errors.New("dataset.url is required when a dataset is supplied")
		}
		if spec.Dataset.SHA256 != "" && !isHex64(spec.Dataset.SHA256) {
			return errors.New("dataset.sha256 must be 64 hex characters")
		}
		if _, err := r.fetcher.Preflight(ctx, spec.Dataset.URL); err != nil {
			return fmt.Errorf("dataset is not usable: %w", err)
		}
	}
	return nil
}

// isHex64 reports whether s is a sha256 digest in hex.
func isHex64(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, c := range s {
		switch {
		case c >= '0' && c <= '9', c >= 'a' && c <= 'f', c >= 'A' && c <= 'F':
		default:
			return false
		}
	}
	return true
}

// Start creates and starts a job container, returning as soon as it is running.
//
// It must return quickly: the caller settles payment immediately afterwards and
// the signed payload expires (CLAUDE.md invariant 4). Supervision, timeout and
// cleanup all continue in the background.
func (r *Runner) Start(ctx context.Context, id, token string, spec Spec) (*Job, error) {
	if err := r.ValidateSpec(spec); err != nil {
		return nil, err
	}

	job := newJob(id, token, spec, r.gpu.Available)
	r.mu.Lock()
	r.jobs[id] = job
	r.mu.Unlock()

	labels := map[string]string{jobLabel: id}
	volumes := []string{
		"cleargate-work-" + id,
		"cleargate-out-" + id,
		"cleargate-data-" + id,
	}

	for _, volume := range volumes {
		if err := r.docker.CreateVolume(ctx, volume, labels); err != nil {
			r.cleanup(context.WithoutCancel(ctx), "", volumes)
			job.finish(StatusFailed, nil, err)
			return job, err
		}
	}

	job.setStaging("preparing")
	r.events.publish(Event{Kind: EventJobStaging, JobID: id, Status: StatusStaging, Detail: "preparing"})

	// Everything slow — the image pull and the dataset download — happens in the
	// background, after this returns. The caller settles payment the moment we
	// return, and the renter's signed payload expires in maxTimeoutSeconds;
	// waiting for a multi-gigabyte download here would forfeit the payment.
	//
	// The context is detached from the request (a renter hanging up must not
	// abandon work they have paid for) but cancellable, so Kill can stop a job
	// that is still downloading. Settlement can fail while staging is under way,
	// and when it does the node must stop immediately rather than finish
	// fetching gigabytes for a payment that never landed.
	stageCtx, cancelStage := context.WithCancel(context.WithoutCancel(ctx))
	job.setCancelStage(cancelStage)
	go r.stageAndRun(stageCtx, job, spec, volumes, labels)

	return job, nil
}

// stageAndRun pulls the image, stages the dataset, and starts the container.
//
// It runs after settlement, so a failure here means the renter has paid for a
// job that never ran. That is why ValidateSpec preflights the dataset URL
// before the 402 is ever issued: by the time we get here, the URL has already
// been proved reachable, allowed, and within the size limit.
func (r *Runner) stageAndRun(ctx context.Context, job *Job, spec Spec, volumes []string, labels map[string]string) {
	fail := func(err error) {
		// A cancelled context means the job was killed, not that staging broke.
		// Report it as the kill it was and leave the terminal state alone.
		if ctx.Err() != nil {
			r.log.Info("job staging cancelled", "job", job.ID)
			job.finish(StatusKilled, nil, errors.New("job was stopped while staging"))
			r.cleanup(context.WithoutCancel(ctx), "", volumes)
			return
		}
		r.log.Error("job staging failed", "job", job.ID, "error", err)
		job.publishLog("stderr", "staging failed: "+err.Error())
		job.finish(StatusFailed, nil, err)
		r.events.publish(Event{
			Kind: EventJobFinished, JobID: job.ID, Status: StatusFailed, Detail: err.Error(),
		})
		r.cleanup(ctx, "", volumes)
	}

	if !r.docker.HasImage(ctx, spec.Image) {
		job.setStaging("pulling image " + spec.Image)
		job.publishLog("stdout", "pulling image "+spec.Image+" — this happens once per image")
		r.events.publish(Event{
			Kind: EventJobStaging, JobID: job.ID, Status: StatusStaging, Detail: "pulling " + spec.Image,
		})
		if err := r.docker.PullImage(ctx, spec.Image); err != nil {
			fail(fmt.Errorf("pull %s: %w", spec.Image, err))
			return
		}
	}

	containerID, err := r.docker.CreateContainer(ctx, "cleargate-"+job.ID,
		r.containerRequest(spec, volumes[0], volumes[1], volumes[2], labels))
	if err != nil {
		fail(err)
		return
	}

	// From here on a failure has a container to clean up too.
	failWithContainer := func(err error) {
		if ctx.Err() != nil {
			r.log.Info("job staging cancelled", "job", job.ID)
			job.finish(StatusKilled, nil, errors.New("job was stopped while staging"))
		} else {
			r.log.Error("job staging failed", "job", job.ID, "error", err)
			job.publishLog("stderr", "staging failed: "+err.Error())
			job.finish(StatusFailed, nil, err)
			r.events.publish(Event{
				Kind: EventJobFinished, JobID: job.ID, Status: StatusFailed, Detail: err.Error(),
			})
		}
		r.cleanup(context.WithoutCancel(ctx), containerID, volumes)
	}

	if spec.Script != nil {
		tarball, tarErr := scriptTar(spec.Script)
		if tarErr != nil {
			failWithContainer(tarErr)
			return
		}
		if err := r.docker.PutArchive(ctx, containerID, workDir, tarball); err != nil {
			failWithContainer(err)
			return
		}
	}

	if spec.Dataset != nil {
		if err := r.stageDataset(ctx, job, spec.Dataset, containerID); err != nil {
			failWithContainer(err)
			return
		}
	}

	// Last chance to notice a kill before the container consumes anything.
	if ctx.Err() != nil {
		failWithContainer(ctx.Err())
		return
	}

	if err := r.docker.StartContainer(ctx, containerID); err != nil {
		failWithContainer(err)
		return
	}

	// Staging is over. Supervision runs on its own detached context so that a
	// later Kill cancels the container, not the supervisor watching it — and
	// releasing the staging context here keeps it from outliving its purpose.
	runCtx := context.WithoutCancel(ctx)
	job.abortStage()

	job.setRunning(containerID, volumes[1])
	r.log.Info("job started", "job", job.ID, "image", spec.Image,
		"gpu", r.gpu.Available, "container", containerID[:12])
	r.events.publish(Event{
		Kind: EventJobStarted, JobID: job.ID, Status: StatusRunning, Detail: spec.Image,
	})

	go r.streamLogs(runCtx, job, containerID)
	go r.supervise(runCtx, job, containerID, volumes, r.timeoutFor(spec))
}

// stageDataset downloads the renter's dataset to a host temp directory,
// optionally unpacks it, and uploads the result into the container's /data
// volume before the container starts.
//
// The download and the extraction both happen on the host. The container never
// gets network access and never needs tar or unzip in its image.
func (r *Runner) stageDataset(ctx context.Context, job *Job, dataset *Dataset, containerID string) error {
	tempDir, err := os.MkdirTemp("", "cleargate-data-"+job.ID+"-")
	if err != nil {
		return fmt.Errorf("create staging directory: %w", err)
	}
	defer os.RemoveAll(tempDir)

	filename := fetch.FilenameFor(dataset.URL, dataset.Filename)
	downloadPath := filepath.Join(tempDir, filename)

	job.setStaging("downloading dataset")
	job.publishLog("stdout", "downloading dataset "+filename)
	r.events.publish(Event{
		Kind: EventJobStaging, JobID: job.ID, Status: StatusStaging, Detail: "downloading " + filename,
	})

	written, sum, err := r.fetcher.Download(ctx, dataset.URL, downloadPath, dataset.SHA256,
		func(progress fetch.Progress) {
			detail := "downloading dataset " + fetch.HumanBytes(progress.Downloaded)
			if progress.Total > 0 {
				detail += " / " + fetch.HumanBytes(progress.Total)
			}
			job.setStaging(detail)
			job.publishLog("stdout", detail)
			r.events.publish(Event{
				Kind: EventJobStaging, JobID: job.ID, Status: StatusStaging, Detail: detail,
			})
		})
	if err != nil {
		return err
	}
	job.publishLog("stdout", fmt.Sprintf("downloaded %s (sha256 %s)", fetch.HumanBytes(written), sum))

	// Where the container will actually read from.
	payloadDir := tempDir
	if !dataset.NoExtract && fetch.IsArchive(filename) {
		job.setStaging("extracting dataset")
		job.publishLog("stdout", "extracting "+filename)

		extracted := filepath.Join(tempDir, "extracted")
		if err := os.MkdirAll(extracted, 0o755); err != nil {
			return err
		}
		if err := fetch.Extract(downloadPath, extracted, int64(r.cfg.Dataset.MaxMB)<<20); err != nil {
			return err
		}
		// The archive itself is not shipped into the container: the renter
		// asked for its contents, and sending both doubles the upload.
		if err := os.Remove(downloadPath); err != nil {
			return err
		}
		payloadDir = extracted
	}

	job.setStaging("copying dataset into the sandbox")
	if err := r.uploadDir(ctx, containerID, payloadDir); err != nil {
		return fmt.Errorf("copy dataset into the container: %w", err)
	}

	job.publishLog("stdout", "dataset ready at "+dataDir)
	return nil
}

// uploadDir streams a host directory into the container's /data volume.
//
// The tar is generated on the fly through an io.Pipe rather than buffered:
// datasets are gigabytes, and PutArchive already accepts a reader.
func (r *Runner) uploadDir(ctx context.Context, containerID, sourceDir string) error {
	reader, writer := io.Pipe()

	go func() {
		tarWriter := tar.NewWriter(writer)
		err := filepath.WalkDir(sourceDir, func(pathname string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			relative, err := filepath.Rel(sourceDir, pathname)
			if err != nil {
				return err
			}
			if relative == "." {
				return nil
			}

			info, err := entry.Info()
			if err != nil {
				return err
			}
			// Regular files and directories only — the extractor already
			// refused links, and nothing else belongs in a dataset.
			if !info.Mode().IsRegular() && !info.IsDir() {
				return nil
			}

			header, err := tar.FileInfoHeader(info, "")
			if err != nil {
				return err
			}
			header.Name = filepath.ToSlash(relative)
			if info.IsDir() {
				header.Name += "/"
			}
			if err := tarWriter.WriteHeader(header); err != nil {
				return err
			}
			if info.IsDir() {
				return nil
			}

			file, err := os.Open(pathname)
			if err != nil {
				return err
			}
			defer file.Close()
			_, err = io.Copy(tarWriter, file)
			return err
		})
		if err == nil {
			err = tarWriter.Close()
		}
		writer.CloseWithError(err)
	}()

	defer reader.Close()
	return r.docker.PutArchive(ctx, containerID, dataDir, reader)
}

// Kill stops a running job. The renter's own token authorizes this.
func (r *Runner) Kill(ctx context.Context, job *Job) error {
	if job.Status().IsTerminal() {
		return nil
	}

	// Stop staging first. A job that is still pulling an image or downloading a
	// dataset has no container to signal, and cancelling is the only thing that
	// stops it — otherwise it would finish the download and start regardless.
	job.abortStage()

	containerID := job.container()
	if containerID == "" {
		// Killed before the container existed. stageAndRun sees the cancelled
		// context and cleans up its volumes; recording the terminal state here
		// means the renter and the dashboard both see it immediately.
		job.finish(StatusKilled, nil, errors.New("job was stopped before it started"))
		return nil
	}

	if err := r.docker.KillContainer(ctx, containerID, "SIGKILL"); err != nil {
		return err
	}
	job.finish(StatusKilled, nil, nil)
	return nil
}

// Artifact streams the job's output directory as a tar. The volume is wiped
// after a successful download, so output never lingers on the provider's disk.
func (r *Runner) Artifact(ctx context.Context, job *Job, w io.Writer) error {
	containerID := job.container()
	if containerID == "" {
		return errors.New("job produced no container")
	}
	if !job.Status().IsTerminal() {
		return errors.New("job is still running; artifacts are only available once it finishes")
	}

	stream, err := r.docker.GetArchive(ctx, containerID, outDir)
	if err != nil {
		return fmt.Errorf("read %s from container: %w", outDir, err)
	}
	defer stream.Close()

	limit := r.cfg.Limits.MaxArtifactMB * 1024 * 1024
	written, err := io.Copy(w, io.LimitReader(stream, limit))
	if err != nil {
		return fmt.Errorf("stream artifact: %w", err)
	}
	if written == limit {
		return fmt.Errorf("artifact exceeded the node's %d MB limit and was truncated", r.cfg.Limits.MaxArtifactMB)
	}
	return nil
}

// containerRequest builds the sandbox. Every restriction here is deliberate.
func (r *Runner) containerRequest(spec Spec, workVolume, outVolume, dataVolume string, labels map[string]string) CreateContainerRequest {
	env := make([]string, 0, len(spec.Env))
	for key, value := range spec.Env {
		env = append(env, key+"="+value)
	}

	host := HostConfig{
		// No network at all. The job computes; it does not phone home.
		NetworkMode: "none",
		Memory:      r.cfg.Limits.MemoryMB * 1024 * 1024,
		NanoCPUs:    int64(r.cfg.Limits.CPUCores) * 1_000_000_000,
		PidsLimit:   r.cfg.Limits.PidsLimit,
		// Read-only root with exactly two writable mounts: the working
		// directory the renter's script is uploaded into, and the output
		// directory whose contents come back as the artifact.
		//
		// Both are volumes rather than tmpfs because Docker refuses to upload
		// an archive into a container whose rootfs is read-only — a tmpfs
		// declared here does not exist until the container starts, and the
		// script has to be in place before that.
		ReadonlyRootfs: true,
		//
		// /data holds the renter's dataset. It is read-write rather than
		// read-only for one mechanical reason: Docker refuses PutArchive into a
		// read-only mount, and the dataset must be uploaded before the container
		// starts. Nothing else depends on it being writable, and the volume is
		// destroyed with the job.
		Mounts: []Mount{
			{Type: "volume", Source: workVolume, Target: workDir},
			{Type: "volume", Source: outVolume, Target: outDir},
			{Type: "volume", Source: dataVolume, Target: dataDir},
		},
		Tmpfs: map[string]string{
			"/tmp": "rw,size=256m",
		},
		// The reaper removes containers explicitly, after collecting the
		// artifact — auto-remove would delete the output before download.
		AutoRemove:  false,
		CapDrop:     []string{"ALL"},
		SecurityOpt: []string{"no-new-privileges"},
	}

	if r.gpu.Available {
		host.DeviceRequests = []DeviceRequest{{
			Driver:       "nvidia",
			Count:        -1, // all GPUs
			Capabilities: [][]string{{"gpu"}},
		}}
	}

	return CreateContainerRequest{
		Image:      spec.Image,
		Cmd:        spec.Cmd,
		Env:        env,
		WorkingDir: workDir,
		Labels:     labels,
		HostConfig: host,
	}
}

func (r *Runner) timeoutFor(spec Spec) time.Duration {
	seconds := spec.Timeout
	if seconds <= 0 || seconds > r.cfg.Limits.MaxSeconds {
		seconds = r.cfg.Limits.MaxSeconds
	}
	return time.Duration(seconds) * time.Second
}

// streamLogs demultiplexes container output into the job's broker until the
// stream ends.
func (r *Runner) streamLogs(ctx context.Context, job *Job, containerID string) {
	stream, err := r.docker.Logs(ctx, containerID, true)
	if err != nil {
		r.log.Warn("log stream unavailable", "job", job.ID, "error", err)
		return
	}
	defer stream.Close()

	var partial []byte
	for {
		frame, err := readLogFrame(stream)
		if err != nil {
			if len(partial) > 0 {
				job.logs.publish(LogLine{Stream: "stdout", Text: string(partial)})
			}
			return
		}

		partial = append(partial, frame.Data...)
		for {
			index := bytes.IndexByte(partial, '\n')
			if index < 0 {
				break
			}
			job.logs.publish(LogLine{Stream: frame.Stream, Text: string(partial[:index])})
			partial = partial[index+1:]
		}
	}
}

// supervise waits for the container, enforces the wall-clock cap, and reaps.
func (r *Runner) supervise(ctx context.Context, job *Job, containerID string, volumes []string, timeout time.Duration) {
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	type result struct {
		code int
		err  error
	}
	done := make(chan result, 1)
	go func() {
		code, err := r.docker.WaitContainer(waitCtx, containerID)
		done <- result{code: code, err: err}
	}()

	select {
	case res := <-done:
		if res.err != nil && !job.Status().IsTerminal() {
			job.finish(StatusFailed, nil, res.err)
			break
		}
		code := res.code
		if code == 0 {
			job.finish(StatusSucceeded, &code, nil)
		} else {
			job.finish(StatusFailed, &code, fmt.Errorf("container exited with code %d", code))
		}

	case <-waitCtx.Done():
		r.log.Warn("job hit its wall-clock limit", "job", job.ID, "timeout", timeout)
		killCtx, killCancel := context.WithTimeout(ctx, 30*time.Second)
		_ = r.docker.KillContainer(killCtx, containerID, "SIGKILL")
		killCancel()
		job.finish(StatusTimeout, nil, fmt.Errorf("job exceeded its %s limit", timeout))
	}

	job.setArtifactReady(true)
	r.log.Info("job finished", "job", job.ID, "status", job.Status())
	r.events.publish(Event{
		Kind:   EventJobFinished,
		JobID:  job.ID,
		Status: job.Status(),
		Detail: job.Image(),
	})

	// Give the renter a window to collect the artifact, then wipe everything.
	go func() {
		timer := time.NewTimer(artifactRetention)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-ctx.Done():
		}
		r.cleanup(context.WithoutCancel(ctx), containerID, volumes)
		job.setArtifactReady(false)
		r.log.Info("job reaped", "job", job.ID)
		r.events.publish(Event{Kind: EventJobReaped, JobID: job.ID, Status: job.Status()})
	}()
}

// artifactRetention is how long output survives after a job ends. Long enough
// for a renter to download, short enough that a provider's disk is not a
// long-term store for other people's data.
const artifactRetention = 15 * time.Minute

// cleanup removes a job's container and every volume it owned. Volumes can only
// be removed once the container referencing them is gone, so the order matters.
func (r *Runner) cleanup(ctx context.Context, containerID string, volumes []string) {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	if containerID != "" {
		if err := r.docker.RemoveContainer(ctx, containerID); err != nil {
			r.log.Warn("could not remove container", "container", containerID, "error", err)
		}
	}
	for _, volume := range volumes {
		if err := r.docker.RemoveVolume(ctx, volume); err != nil {
			r.log.Warn("could not remove volume", "volume", volume, "error", err)
		}
	}
}

// scriptTar wraps a renter's script in a tar for upload into the container.
// Going through the archive API rather than a shell command means the filename
// and contents are never interpreted by a shell.
func scriptTar(script *Script) (io.Reader, error) {
	content, err := base64.StdEncoding.DecodeString(script.ContentBase64)
	if err != nil {
		return nil, fmt.Errorf("decode script: %w", err)
	}

	var buffer bytes.Buffer
	writer := tar.NewWriter(&buffer)
	header := &tar.Header{
		Name:    script.Filename,
		Mode:    0o755,
		Size:    int64(len(content)),
		ModTime: time.Now(),
	}
	if err := writer.WriteHeader(header); err != nil {
		return nil, fmt.Errorf("write tar header: %w", err)
	}
	if _, err := writer.Write(content); err != nil {
		return nil, fmt.Errorf("write tar body: %w", err)
	}
	if err := writer.Close(); err != nil {
		return nil, fmt.Errorf("close tar: %w", err)
	}
	return &buffer, nil
}
