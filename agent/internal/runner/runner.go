package runner

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/YashIIT0909/ClearGate/agent/internal/config"
)

// legacyJobLabel marks containers and volumes made by builds that also sold
// batch jobs. Nothing creates them any more, but the startup sweep still
// removes any left behind, because each one may be holding a renter's data.
const legacyJobLabel = "cleargate.job"

// Runner owns every lease container on this node.
type Runner struct {
	docker *Docker
	cfg    config.Config
	gpu    GPU
	log    *slog.Logger
	events *eventBroker

	mu sync.RWMutex

	// leaseImageCUDA is whether the configured lease image can actually compute
	// on a GPU. Decided once at startup and read-only afterwards.
	leaseImageCUDA bool

	// leases holds every lease this process has seen, and activeLease names the
	// one currently holding the node's single lease slot. V1 rents to one
	// renter at a time (implementation.md §9); both are guarded by mu.
	leases      map[string]*Lease
	activeLease string
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
		events: newEventBroker(),
		leases: make(map[string]*Lease),
	}

	run.sweepOrphans(ctx)

	// Lease state lives in memory, so anything still labelled as a lease at
	// startup is an orphan: a container with a live sshd that nothing is
	// metering any more.
	if cfg.Leases.Enabled {
		run.sweepLeaseOrphans(ctx)

		// A working card on the host does not mean a renter can use it. Say so
		// loudly if the lease image cannot: a provider who thinks they are
		// renting out a GPU and is selling CPU time will find out from an angry
		// renter otherwise — the same failure the CPU-fallback warning exists
		// to prevent.
		run.leaseImageCUDA = run.detectLeaseImageCUDA(ctx)
		switch {
		case run.leaseImageCUDA:
			log.Info("lease image is CUDA-capable", "image", cfg.Leases.Image)
		case gpu.Available:
			log.Warn("this node has a usable GPU but its lease image has no CUDA runtime; "+
				"sessions will be sold as CPU-only until it is rebuilt",
				"image", cfg.Leases.Image,
				"fix", "make lease-image")
		default:
			log.Info("sessions will be CPU-only", "image", cfg.Leases.Image)
		}
	}

	return run, nil
}

// sweepOrphans removes containers and volumes left by a build that sold batch
// jobs. Called once at startup.
func (r *Runner) sweepOrphans(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()

	containers, err := r.docker.ListContainersByLabel(ctx, legacyJobLabel)
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

	volumes, err := r.docker.ListVolumesByLabel(ctx, legacyJobLabel)
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
