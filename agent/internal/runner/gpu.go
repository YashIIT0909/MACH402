package runner

import (
	"context"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// GPU describes what this node can actually offer a renter.
type GPU struct {
	// Available is true only when a GPU is present *and* the nvidia container
	// runtime is installed. A GPU the daemon cannot pass through is worthless
	// to a renter, so both must hold.
	Available bool
	Model     string
	VRAMMb    int
	// Reason explains an unavailable GPU, so the operator can fix it.
	Reason string
}

// DetectGPU decides whether this node runs jobs on a GPU or falls back to CPU.
//
// Both halves are checked because they fail independently: a laptop can have an
// RTX card with no container toolkit installed, and a server can have the
// toolkit with the card claimed by another process.
func DetectGPU(ctx context.Context, docker *Docker, requested bool) GPU {
	if !requested {
		return GPU{Reason: "gpu_enabled is false in config; running in CPU-fallback mode"}
	}

	model, vram, err := querySMI(ctx)
	if err != nil {
		return GPU{Reason: "nvidia-smi unavailable: " + err.Error()}
	}

	info, err := docker.Info(ctx)
	if err != nil {
		return GPU{Model: model, VRAMMb: vram, Reason: "docker info failed: " + err.Error()}
	}
	if _, ok := info.Runtimes["nvidia"]; !ok {
		installed := make([]string, 0, len(info.Runtimes))
		for name := range info.Runtimes {
			installed = append(installed, name)
		}
		return GPU{
			Model:  model,
			VRAMMb: vram,
			Reason: "docker has no \"nvidia\" runtime (found: " + strings.Join(installed, ", ") +
				"); install the NVIDIA Container Toolkit to rent this GPU out",
		}
	}

	return GPU{Available: true, Model: model, VRAMMb: vram}
}

// querySMI reads the first GPU's model and memory from nvidia-smi.
func querySMI(ctx context.Context) (model string, vramMb int, err error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "nvidia-smi",
		"--query-gpu=name,memory.total", "--format=csv,noheader,nounits")
	output, err := cmd.Output()
	if err != nil {
		return "", 0, err
	}

	line, _, _ := strings.Cut(strings.TrimSpace(string(output)), "\n")
	name, memory, found := strings.Cut(line, ",")
	if !found {
		return strings.TrimSpace(line), 0, nil
	}

	vram, convErr := strconv.Atoi(strings.TrimSpace(memory))
	if convErr != nil {
		vram = 0
	}
	return strings.TrimSpace(name), vram, nil
}
