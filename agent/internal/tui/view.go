package tui

import (
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/YashIIT0909/ClearGate/agent/internal/runner"
)

// Colours are adaptive: a provider's terminal may be light or dark, and the
// dashboard has to stay readable in both. Nothing here assumes 24-bit colour.
var (
	styleTitle   = lipgloss.NewStyle().Bold(true)
	styleDim     = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "244", Dark: "245"})
	styleMoney   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.AdaptiveColor{Light: "28", Dark: "42"})
	styleWarn    = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "166", Dark: "214"})
	styleBad     = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "160", Dark: "203"})
	styleGood    = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "28", Dark: "42"})
	styleRunning = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "26", Dark: "39"})
	styleSelect  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.AdaptiveColor{Light: "57", Dark: "141"})
	styleRule    = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "250", Dark: "238"})
)

func (m *Model) View() string {
	if m.quitting {
		if m.serverErr != nil {
			return "node stopped: " + m.serverErr.Error() + "\n"
		}
		return fmt.Sprintf("node stopped. %d settlement(s), %s earned this session.\n",
			m.settlements, formatHBAR(m.earnedTinybars))
	}
	if m.showHelp {
		return m.helpView()
	}

	width := m.width
	if width < 60 {
		width = 60
	}

	var b strings.Builder
	b.WriteString(m.headerView(width))
	b.WriteString(m.rule(width))
	b.WriteString(m.jobsView(width))
	b.WriteString(m.rule(width))
	b.WriteString(m.logsView(width))
	b.WriteString(m.rule(width))
	b.WriteString(m.feedView(width))
	b.WriteString(m.rule(width))
	b.WriteString(m.footerView(width))
	return b.String()
}

func (m *Model) rule(width int) string {
	return styleRule.Render(strings.Repeat("─", width)) + "\n"
}

func (m *Model) headerView(width int) string {
	state := styleGood.Render("accepting")
	if m.server.Paused() {
		state = styleWarn.Render("paused")
	}

	uptime := time.Since(m.startedAt).Round(time.Second)
	left := fmt.Sprintf("%s  %s  %s",
		styleTitle.Render("ClearGate"), m.cfg.NodeID, state)
	right := fmt.Sprintf("agent %s   up %s", m.version, uptime)

	var gpu string
	switch {
	case !m.gpu.Available && m.gpu.Model != "":
		// The card exists but cannot be passed through. Say so every second the
		// dashboard is open: a provider must never believe they are selling GPU
		// time when they are selling CPU time.
		gpu = styleWarn.Render("GPU  " + m.gpu.Model + " present but NOT usable — CPU-fallback mode")
	case !m.gpu.Available:
		gpu = styleWarn.Render("GPU  none — CPU-fallback mode")
	default:
		gpu = fmt.Sprintf("GPU  %s · %d MB · %d%% util · %d/%d MB",
			m.gpu.Model, m.gpu.VRAMMb, m.gpuUtil, m.gpuUsedMB, m.gpu.VRAMMb)
	}

	money := fmt.Sprintf("%s per job → %s     earned %s over %d job(s)",
		formatHBAR(parseTinybars(m.cfg.PriceTinybars)),
		m.cfg.PayTo,
		styleMoney.Render(formatHBAR(m.earnedTinybars)),
		m.settlements)

	return spread(left, right, width) + "\n" +
		"  " + gpu + "\n" +
		"  " + money + "\n"
}

func (m *Model) jobsView(width int) string {
	if len(m.jobs) == 0 {
		return styleDim.Render("  no jobs yet — the node is listening on "+m.cfg.ListenAddr) + "\n"
	}

	rows := m.jobRows()
	var b strings.Builder
	b.WriteString(styleDim.Render(fmt.Sprintf("  %-9s %-10s %-28s %-9s %s",
		"JOB", "STATUS", "IMAGE", "ELAPSED", "DETAIL")) + "\n")

	for i, job := range rows {
		marker := "  "
		line := fmt.Sprintf("%-9s %-10s %-28s %-9s %s",
			short(job.JobID),
			job.Status,
			truncate(job.Image, 28),
			elapsed(job),
			detailOf(job))

		if i == m.selected {
			marker = styleSelect.Render("▸ ")
			line = styleSelect.Render(line)
		} else {
			line = statusStyle(job.Status).Render(line)
		}
		b.WriteString(marker + truncate(line, width) + "\n")
	}
	return b.String()
}

// jobRows limits the table to what fits, keeping the selection visible.
func (m *Model) jobRows() []runner.State {
	capacity := m.height - 18
	if capacity < 3 {
		capacity = 3
	}
	if len(m.jobs) <= capacity {
		return m.jobs
	}
	start := m.selected - capacity/2
	if start < 0 {
		start = 0
	}
	if start+capacity > len(m.jobs) {
		start = len(m.jobs) - capacity
	}
	return m.jobs[start : start+capacity]
}

func (m *Model) logsView(width int) string {
	job, ok := m.selectedJob()
	if !ok {
		return styleDim.Render("  no job selected") + "\n"
	}

	header := styleDim.Render("  logs · " + short(job.JobID))
	capacity := 6
	tail := m.logTail
	if len(tail) > capacity {
		tail = tail[len(tail)-capacity:]
	}

	var b strings.Builder
	b.WriteString(header + "\n")
	if len(tail) == 0 {
		b.WriteString(styleDim.Render("  (no output yet)") + "\n")
		return b.String()
	}
	for _, line := range tail {
		text := truncate(line.Text, width-4)
		if line.Stream == "stderr" {
			b.WriteString("  " + styleBad.Render(text) + "\n")
		} else {
			b.WriteString("  " + text + "\n")
		}
	}
	return b.String()
}

func (m *Model) feedView(width int) string {
	capacity := 5
	feed := m.feed
	if len(feed) > capacity {
		feed = feed[len(feed)-capacity:]
	}
	if len(feed) == 0 {
		return styleDim.Render("  waiting for the first payment…") + "\n"
	}

	var b strings.Builder
	for _, event := range feed {
		b.WriteString("  " + truncate(renderEvent(event), width-4) + "\n")
	}
	return b.String()
}

// renderEvent is where the provider actually watches money arrive, so a
// settlement carries its transaction id in full — that string is what they
// paste into HashScan to confirm it themselves.
func renderEvent(event runner.Event) string {
	stamp := styleDim.Render(event.At.Format("15:04:05"))

	switch event.Kind {
	case runner.EventSettled:
		return fmt.Sprintf("%s %s %s from %s  %s",
			stamp,
			styleMoney.Render("settled"),
			formatHBAR(parseTinybars(event.Tinybars)),
			event.Payer,
			styleDim.Render(event.Transaction))
	case runner.EventSettleFailed:
		return fmt.Sprintf("%s %s %s", stamp, styleBad.Render("settle failed"), event.Detail)
	case runner.EventVerified:
		return fmt.Sprintf("%s %s payer %s", stamp, styleGood.Render("verified"), event.Payer)
	case runner.EventRejected:
		return fmt.Sprintf("%s %s %s", stamp, styleBad.Render("payment rejected"), event.Detail)
	case runner.EventChallenged:
		return fmt.Sprintf("%s %s %s", stamp, styleDim.Render("402 challenge"), event.Detail)
	case runner.EventJobStaging:
		return fmt.Sprintf("%s %s %s  %s", stamp, styleRunning.Render("staging"), short(event.JobID), event.Detail)
	case runner.EventJobStarted:
		return fmt.Sprintf("%s %s %s  %s", stamp, styleRunning.Render("started"), short(event.JobID), event.Detail)
	case runner.EventJobFinished:
		return fmt.Sprintf("%s %s %s  %s", stamp,
			statusStyle(event.Status).Render(string(event.Status)), short(event.JobID), event.Detail)
	case runner.EventJobReaped:
		return fmt.Sprintf("%s %s %s", stamp, styleDim.Render("reaped"), short(event.JobID))
	default:
		return fmt.Sprintf("%s %s %s", stamp, event.Kind, event.Detail)
	}
}

func (m *Model) footerView(width int) string {
	if m.statusFlash != "" && time.Now().Before(m.flashUntil) {
		return "  " + styleWarn.Render(truncate(m.statusFlash, width-4)) + "\n"
	}
	keys := "↑↓ select   x kill job   p pause/resume   ? help   q quit"
	return "  " + styleDim.Render(keys) + "\n"
}

func (m *Model) helpView() string {
	return styleTitle.Render("ClearGate — provider dashboard") + "\n\n" +
		"  ↑ ↓ / j k   select a job\n" +
		"  x           kill the selected job.\n" +
		"              Flat-fee jobs are paid up front, so the renter is NOT\n" +
		"              refunded. Metered leases are where stopping saves money.\n" +
		"  p           pause or resume selling jobs. Running jobs keep running;\n" +
		"              new requests get a 503 instead of a 402, so nobody pays\n" +
		"              for a job this node will not start.\n" +
		"  ?           close this help\n" +
		"  q           stop the node\n\n" +
		styleDim.Render("  Earnings shown here count this session's settlements. The\n"+
			"  authoritative record is receipts.jsonl — `cleargate-node earnings`\n"+
			"  reads it without trusting this display or any website.\n")
}

func statusStyle(status runner.Status) lipgloss.Style {
	switch status {
	case runner.StatusRunning:
		return styleRunning
	case runner.StatusStaging, runner.StatusPending:
		return styleDim
	case runner.StatusSucceeded:
		return styleGood
	case runner.StatusFailed, runner.StatusTimeout, runner.StatusKilled:
		return styleBad
	default:
		return lipgloss.NewStyle()
	}
}

func detailOf(job runner.State) string {
	if job.Stage != "" {
		return job.Stage
	}
	if job.Error != nil && *job.Error != "" {
		return *job.Error
	}
	if job.ExitCode != nil {
		return fmt.Sprintf("exit %d", *job.ExitCode)
	}
	if job.GPU {
		return "gpu"
	}
	return ""
}

func elapsed(job runner.State) string {
	start := startTime(job)
	if start.IsZero() {
		return "—"
	}
	end := time.Now()
	if job.EndedAt != nil {
		if parsed, err := time.Parse(time.RFC3339, *job.EndedAt); err == nil {
			end = parsed
		}
	}
	d := end.Sub(start).Round(time.Second)
	return fmt.Sprintf("%02d:%02d:%02d", int(d.Hours()), int(d.Minutes())%60, int(d.Seconds())%60)
}

// spread puts left and right on one line with the gap between them.
func spread(left, right string, width int) string {
	gap := width - lipgloss.Width(left) - lipgloss.Width(right) - 2
	if gap < 1 {
		gap = 1
	}
	return "  " + left + strings.Repeat(" ", gap) + right
}

// formatHBAR renders tinybars as HBAR using integer arithmetic only. Payment
// amounts are never floats anywhere in this project.
func formatHBAR(tinybars int64) string {
	value := big.NewInt(tinybars)
	perHBAR := big.NewInt(100_000_000)
	whole, frac := new(big.Int).QuoRem(value, perHBAR, new(big.Int))
	if frac.Sign() == 0 {
		return whole.String() + " HBAR"
	}
	fraction := strings.TrimRight(fmt.Sprintf("%08d", frac), "0")
	return whole.String() + "." + fraction + " HBAR"
}
