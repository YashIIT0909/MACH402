package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/YashIIT0909/ClearGate/agent/internal/runner"
)

// jobsView is the batch-work screen: the table, what the highlighted job is
// doing, and its live output.
//
// The split is deliberate. A provider looking at this screen is almost always
// answering one of two questions — which of these is stuck, and what is it
// printing — and the old single-stack layout could give the log tail six lines,
// which is not enough to read a stack trace or a training loop.
func (m *Model) jobsView(width, height int) string {
	body := width - 2

	if len(m.jobs) == 0 {
		return indent(panel("jobs", body,
			styleDim.Render("no jobs yet — this node is listening on "+m.cfg.ListenAddr),
			"",
			styleDim.Render("a renter pays POST /v1/jobs and the container starts here."),
			styleDim.Render("nothing runs on an unverified payment, so an empty table on a"),
			styleDim.Render("node that is being probed is the system working."),
		))
	}

	// Roughly half the space to the table, the rest to output, with the detail
	// line between them. Both halves keep a floor so neither vanishes.
	tableRows := max((height-10)/2, 3)
	logRows := max(height-tableRows-11, 3)

	var b strings.Builder
	b.WriteString(panel(fmt.Sprintf("jobs · %d running of %d", m.runningJobs(), len(m.jobs)),
		body, m.jobTable(body-4, tableRows)...))
	b.WriteString(m.jobDetail(body))
	b.WriteString(panel(m.logTitle(), body, m.jobLogs(body-4, logRows)...))
	return indent(b.String())
}

func (m *Model) jobTable(width, rows int) []string {
	header := styleHead.Render(fmt.Sprintf("  %-9s %-10s %-26s %-4s %-9s %s",
		"JOB", "STATUS", "IMAGE", "GPU", "ELAPSED", "DETAIL"))

	out := make([]string, 0, rows+1)
	out = append(out, header)

	window := m.jobWindow(rows)
	for _, job := range window.jobs {
		// The selected row carries the selection colour only. Layering it over
		// the status colour would leave the highlight competing with red for a
		// failed job, which is the row a provider is most often looking at.
		if job.JobID == window.selectedID {
			out = append(out, styleSelect.Render("▸ "+jobLine(job, width-2)))
			continue
		}
		out = append(out, "  "+statusStyle(job.Status).Render(jobLine(job, width-2)))
	}

	if window.more > 0 {
		out = append(out, styleDim.Render(fmt.Sprintf("  … %d more", window.more)))
	}

	// Padded to its allotted height rather than sized to its contents: a table
	// that grew as jobs arrived would push the output pane down the screen
	// under a provider who is reading it.
	for len(out) < rows+1 {
		out = append(out, "")
	}
	return out
}

// jobLine is one table row, unstyled, so the caller decides what colour it
// carries — status on an ordinary row, the selection colour on the current one.
func jobLine(job runner.State, width int) string {
	return truncate(fmt.Sprintf("%-9s %-10s %-26s %-4s %-9s %s",
		short(job.JobID),
		job.Status,
		truncate(job.Image, 26),
		gpuMark(job.GPU),
		elapsed(job),
		detailOf(job)), width)
}

// jobRow is jobLine in its status colour, which is what the Overview's "right
// now" panel wants: no selection there, just what is running.
func jobRow(job runner.State, width int) string {
	return statusStyle(job.Status).Render(jobLine(job, width))
}

type jobWindow struct {
	jobs       []runner.State
	selectedID string
	more       int
}

// jobWindow limits the table to what fits, keeping the selection visible.
func (m *Model) jobWindow(capacity int) jobWindow {
	window := jobWindow{jobs: m.jobs}
	if job, ok := m.selectedJob(); ok {
		window.selectedID = job.JobID
	}
	if len(m.jobs) <= capacity {
		return window
	}

	start := m.selected - capacity/2
	if start < 0 {
		start = 0
	}
	if start+capacity > len(m.jobs) {
		start = len(m.jobs) - capacity
	}
	window.jobs = m.jobs[start : start+capacity]
	window.more = len(m.jobs) - start - capacity
	return window
}

// jobDetail is the one line the table has no column for: the full image
// reference, the exit status, and the error if there was one.
func (m *Model) jobDetail(width int) string {
	job, ok := m.selectedJob()
	if !ok {
		return "\n"
	}

	parts := []string{
		styleDim.Render("job ") + job.JobID,
		styleDim.Render("image ") + job.Image,
	}
	if job.ArtifactReady {
		parts = append(parts, styleGood.Render("artifact ready"))
	}
	if job.ExitCode != nil {
		parts = append(parts, styleDim.Render("exit ")+fmt.Sprint(*job.ExitCode))
	}
	if job.Error != nil && *job.Error != "" {
		parts = append(parts, styleBad.Render(*job.Error))
	}
	return " " + truncate(strings.Join(parts, styleBorder.Render("  ·  ")), width) + "\n"
}

func (m *Model) logTitle() string {
	job, ok := m.selectedJob()
	if !ok {
		return "output"
	}
	return "output · " + short(job.JobID)
}

func (m *Model) jobLogs(width, rows int) []string {
	if _, ok := m.selectedJob(); !ok {
		return []string{styleDim.Render("no job selected")}
	}

	tail := m.logTail
	if len(tail) > rows {
		tail = tail[len(tail)-rows:]
	}
	out := make([]string, 0, rows)
	if len(tail) == 0 {
		out = append(out, styleDim.Render("(no output yet — a staging job has not started its container)"))
	}
	for _, line := range tail {
		text := truncate(line.Text, width)
		if line.Stream == "stderr" {
			text = styleBad.Render(text)
		}
		out = append(out, text)
	}

	// Output grows downward inside a fixed frame, so a new line does not move
	// every border on the screen.
	for len(out) < rows {
		out = append(out, "")
	}
	return out
}

func gpuMark(gpu bool) string {
	if gpu {
		return "gpu"
	}
	return "cpu"
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
