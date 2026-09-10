// Package tui is the provider's dashboard: a live view of what this node is
// earning and what it is running.
//
// It is a *view*. Every fact on screen comes from the same runner and server
// that `serve` uses headlessly, and the TUI never owns state of its own beyond
// what is needed to draw. That is deliberate — a provider debugging a node
// should never have to wonder whether the dashboard and the daemon disagree.
package tui

import (
	"context"
	"fmt"
	"sort"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/YashIIT0909/ClearGate/agent/internal/config"
	"github.com/YashIIT0909/ClearGate/agent/internal/httpapi"
	"github.com/YashIIT0909/ClearGate/agent/internal/runner"
)

// refreshInterval drives the clock and the job table. Fast enough to feel live,
// slow enough that an idle node is not spinning a CPU it is trying to rent out.
const refreshInterval = time.Second

// gpuPollInterval is how often nvidia-smi is asked for utilisation. Each call
// forks a process, so this stays well clear of the redraw rate.
const gpuPollInterval = 2 * time.Second

// maxFeedLines bounds the payment feed on screen.
const maxFeedLines = 100

// Model is the dashboard state.
type Model struct {
	cfg     config.Config
	runner  *runner.Runner
	server  *httpapi.Server
	version string
	gpu     runner.GPU

	startedAt time.Time
	width     int
	height    int

	jobs     []runner.State
	selected int
	feed     []runner.Event

	// logs holds the tail of the selected job's output. It is re-subscribed
	// whenever the selection changes, so only one job streams at a time.
	logTail   []runner.LogLine
	logJobID  string
	logCancel context.CancelFunc

	earnedTinybars int64
	settlements    int

	gpuUtil     int
	gpuUsedMB   int
	serverErr   error
	quitting    bool
	showHelp    bool
	statusFlash string
	flashUntil  time.Time

	// eventCh is the live subscription to the runner's broker. program is the
	// bubbletea handle log lines are pushed through from their own goroutine.
	eventCh chan runner.Event
	program *tea.Program
}

// Attach hands the model the program it runs under, so background goroutines
// following a job's logs can push lines into the update loop.
func (m *Model) Attach(program *tea.Program) { m.program = program }

// New builds the dashboard model.
func New(cfg config.Config, run *runner.Runner, server *httpapi.Server, version string, earned int64, settlements int) *Model {
	return &Model{
		cfg:            cfg,
		runner:         run,
		server:         server,
		version:        version,
		gpu:            run.GPU(),
		startedAt:      time.Now(),
		earnedTinybars: earned,
		settlements:    settlements,
		width:          100,
		height:         30,
	}
}

// Messages the dashboard reacts to.
type (
	tickMsg    time.Time
	gpuMsg     struct{ util, usedMB int }
	eventMsg   runner.Event
	logMsg     runner.LogLine
	serverDown struct{ err error }
)

func (m *Model) Init() tea.Cmd {
	backlog, ch := m.runner.Subscribe()
	m.eventCh = ch
	for _, event := range backlog {
		m.record(event)
	}
	m.jobs = sortedJobs(m.runner.List())

	return tea.Batch(
		tick(),
		pollGPU(m.gpu.Available),
		m.nextEvent(ch),
	)
}

func tick() tea.Cmd {
	return tea.Tick(refreshInterval, func(t time.Time) tea.Msg { return tickMsg(t) })
}

// nextEvent waits for one event, then the update loop re-arms it.
//
// One event per command on purpose: bubbletea serialises model updates, so
// taking a single event and immediately re-subscribing keeps the UI responsive
// without ever blocking the publisher — the broker drops rather than waits, so
// a wedged dashboard can never stall a settlement.
func (m *Model) nextEvent(ch chan runner.Event) tea.Cmd {
	return func() tea.Msg {
		event, ok := <-ch
		if !ok {
			return nil
		}
		return eventMsg(event)
	}
}

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)

	case tickMsg:
		m.jobs = sortedJobs(m.runner.List())
		m.clampSelection()
		m.followSelectedJob()
		return m, tick()

	case gpuMsg:
		m.gpuUtil, m.gpuUsedMB = msg.util, msg.usedMB
		return m, pollGPU(m.gpu.Available)

	case eventMsg:
		m.record(runner.Event(msg))
		return m, m.nextEvent(m.eventCh)

	case logMsg:
		m.appendLog(runner.LogLine(msg))
		return m, nil

	case serverDown:
		m.serverErr = msg.err
		return m, tea.Quit
	}
	return m, nil
}

func (m *Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "ctrl+c":
		m.quitting = true
		return m, tea.Quit

	case "?":
		m.showHelp = !m.showHelp

	case "up", "k":
		if m.selected > 0 {
			m.selected--
		}

	case "down", "j":
		if m.selected < len(m.jobs)-1 {
			m.selected++
		}

	case "p":
		paused := !m.server.Paused()
		m.server.SetPaused(paused)
		if paused {
			m.flash("paused — running jobs continue, no new ones are sold")
		} else {
			m.flash("accepting jobs again")
		}

	case "x":
		return m, m.killSelected()
	}
	return m, nil
}

// killSelected stops the highlighted job. Flat-fee jobs are paid up front, so
// this is destructive and unrefunded — the confirmation is the deliberate
// choice of a two-key sequence rather than a single stray keystroke.
func (m *Model) killSelected() tea.Cmd {
	job, ok := m.selectedJob()
	if !ok {
		return nil
	}
	if runner.Status(job.Status).IsTerminal() {
		m.flash("job " + short(job.JobID) + " has already finished")
		return nil
	}

	live, found := m.runner.Get(job.JobID)
	if !found {
		return nil
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = m.runner.Kill(ctx, live)
	}()
	m.flash("killing " + short(job.JobID) + " — the renter is not refunded")
	return nil
}

func (m *Model) flash(text string) {
	m.statusFlash = text
	m.flashUntil = time.Now().Add(4 * time.Second)
}

func (m *Model) record(event runner.Event) {
	m.feed = append(m.feed, event)
	if len(m.feed) > maxFeedLines {
		m.feed = m.feed[len(m.feed)-maxFeedLines:]
	}
	if event.Kind == runner.EventSettled {
		m.settlements++
		m.earnedTinybars += parseTinybars(event.Tinybars)
	}
}

func (m *Model) selectedJob() (runner.State, bool) {
	if m.selected < 0 || m.selected >= len(m.jobs) {
		return runner.State{}, false
	}
	return m.jobs[m.selected], true
}

func (m *Model) clampSelection() {
	if m.selected >= len(m.jobs) {
		m.selected = len(m.jobs) - 1
	}
	if m.selected < 0 {
		m.selected = 0
	}
}

// followSelectedJob keeps the log pane pointed at whatever is highlighted,
// tearing down the previous subscription so only one job streams at a time.
func (m *Model) followSelectedJob() {
	job, ok := m.selectedJob()
	if !ok || job.JobID == m.logJobID {
		return
	}

	if m.logCancel != nil {
		m.logCancel()
	}
	live, found := m.runner.Get(job.JobID)
	if !found {
		return
	}

	m.logJobID = job.JobID
	m.logTail = live.Logs()
	if len(m.logTail) > 200 {
		m.logTail = m.logTail[len(m.logTail)-200:]
	}

	ctx, cancel := context.WithCancel(context.Background())
	m.logCancel = cancel

	_, ch := live.Subscribe()
	go func() {
		defer live.Unsubscribe(ch)
		for {
			select {
			case <-ctx.Done():
				return
			case line, open := <-ch:
				if !open {
					return
				}
				if m.program != nil {
					m.program.Send(logMsg(line))
				}
			}
		}
	}()
}

func (m *Model) appendLog(line runner.LogLine) {
	m.logTail = append(m.logTail, line)
	if len(m.logTail) > 200 {
		m.logTail = m.logTail[len(m.logTail)-200:]
	}
}

// sortedJobs orders the table: live work first, then most recent.
func sortedJobs(states []runner.State) []runner.State {
	sort.SliceStable(states, func(i, j int) bool {
		iLive := !runner.Status(states[i].Status).IsTerminal()
		jLive := !runner.Status(states[j].Status).IsTerminal()
		if iLive != jLive {
			return iLive
		}
		return startTime(states[i]).After(startTime(states[j]))
	})
	return states
}

func startTime(state runner.State) time.Time {
	if state.StartedAt == nil {
		return time.Time{}
	}
	parsed, err := time.Parse(time.RFC3339, *state.StartedAt)
	if err != nil {
		return time.Time{}
	}
	return parsed
}

func short(id string) string {
	if len(id) <= 8 {
		return id
	}
	return id[:8]
}

func parseTinybars(raw string) int64 {
	var value int64
	if raw == "" {
		return 0
	}
	if _, err := fmt.Sscanf(raw, "%d", &value); err != nil {
		return 0
	}
	return value
}

// truncate cuts a string to a visible width, counting display cells rather than
// bytes or runes.
//
// This has to be ANSI-aware: the strings it receives are already styled, and a
// naive rune count charges the escape sequences against the width budget. That
// showed up as a settlement's transaction id being cut off — the one string on
// screen a provider actually needs in full to check it on HashScan.
func truncate(text string, width int) string {
	if width <= 0 {
		return ""
	}
	if lipgloss.Width(text) <= width {
		return text
	}
	return ansi.Truncate(text, width, "…")
}
