package tui

import (
	"context"
	"os/exec"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// pollGPU asks nvidia-smi for live utilisation.
//
// It shells out rather than linking NVML: the agent is a single static binary
// that must build and run on machines with no CUDA toolchain at all, and a
// missing nvidia-smi has to degrade to "no GPU", never to a link error.
func pollGPU(available bool) tea.Cmd {
	if !available {
		return nil
	}
	return tea.Tick(gpuPollInterval, func(_ time.Time) tea.Msg {
		return queryGPU()
	})
}

func queryGPU() tea.Msg {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	out, err := exec.CommandContext(ctx, "nvidia-smi",
		"--query-gpu=utilization.gpu,memory.used",
		"--format=csv,noheader,nounits").Output()
	if err != nil {
		return gpuMsg{}
	}

	line, _, _ := strings.Cut(strings.TrimSpace(string(out)), "\n")
	utilRaw, memRaw, found := strings.Cut(line, ",")
	if !found {
		return gpuMsg{}
	}

	util, _ := strconv.Atoi(strings.TrimSpace(utilRaw))
	used, _ := strconv.Atoi(strings.TrimSpace(memRaw))
	return gpuMsg{util: util, usedMB: used}
}
