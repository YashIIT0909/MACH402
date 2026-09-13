// Package tui is the provider's dashboard: a live view of what this node is
// earning, what it is running, and whether anyone can find it.
//
// It is a *view*. Every fact on screen comes from the same runner and server
// that `serve` uses headlessly, and the TUI never owns state of its own beyond
// what is needed to draw. That is deliberate — a provider debugging a node
// should never have to wonder whether the dashboard and the daemon disagree.
//
// The one thing it does own is a ledger of past payments, seeded from
// receipts.jsonl at startup. That is not second-guessing the daemon: it is
// reading the same authoritative file `cleargate-node earnings` reads, so that
// a restart does not appear to reset a provider's earnings to zero.
package tui

import (
	"context"
	"sort"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/YashIIT0909/ClearGate/agent/internal/config"
	"github.com/YashIIT0909/ClearGate/agent/internal/httpapi"
	"github.com/YashIIT0909/ClearGate/agent/internal/receipts"
	"github.com/YashIIT0909/ClearGate/agent/internal/registry"
	"github.com/YashIIT0909/ClearGate/agent/internal/runner"
)

// refreshInterval drives the clock and the lease countdown.
// Fast enough to feel live, slow enough that an idle node is not spinning a CPU
// it is trying to rent out.
const refreshInterval = time.Second

// gpuPollInterval is how often nvidia-smi is asked for utilisation. Each call
// forks a process, so this stays well clear of the redraw rate.
const gpuPollInterval = 2 * time.Second

// maxFeedLines bounds the activity feed on screen. Larger than any terminal is
// tall on purpose: the Activity tab scrolls back through it.
const maxFeedLines = 500

// gpuHistoryLen is how many utilisation samples the header sparkline keeps —
// at gpuPollInterval, about two minutes of history.
const gpuHistoryLen = 60

// tab is one screen of the dashboard.
//
// Tabs rather than one long stack because the previous layout drew every
// section at once and gave each of them three or four lines, which meant the
// lease panel and the payment feed were both permanently too short to be
// useful. A provider is doing one of a small number of things at a time —
// watching money land, watching a session run, checking why nobody can find
// their node — and each of those deserves the whole screen while it is the thing
// being done.
type tab int

const (
	tabOverview tab = iota
	tabLeases
	tabActivity
	tabNode
)

// tabOrder is the left-to-right order of the tab strip, and what the number
// keys select.
var tabOrder = []tab{tabOverview, tabLeases, tabActivity, tabNode}

func (t tab) title() string {
	switch t {
	case tabOverview:
		return "Overview"
	case tabLeases:
		return "Leasing"
	case tabActivity:
		return "Activity"
	case tabNode:
		return "Node"
	}
	return ""
}

// feedFilter narrows the Activity tab to one kind of thing.
//
// A busy metered session publishes a burn checkpoint every fifteen seconds,
// which is exactly the behaviour that makes the session trustworthy and also
// exactly the behaviour that buries a settlement a provider was watching for.
type feedFilter int

const (
	filterAll feedFilter = iota
	filterMoney
	filterLeases
)

func (f feedFilter) title() string {
	switch f {
	case filterMoney:
		return "money"
	case filterLeases:
		return "leases"
	}
	return "everything"
}

// payment is one settled receipt, kept so the dashboard can answer "what have I
// earned today" and not only "what have I earned ever".
type payment struct {
	at       time.Time
	tinybars int64
	payer    string
}

// Options is everything the dashboard needs to draw a node.
//
// A struct rather than a parameter list because this is the seam where the
// dashboard meets the daemon, and a caller should be able to see at the call
// site which of the node's parts it was handed.
type Options struct {
	Config  config.Config
	Runner  *runner.Runner
	Server  *httpapi.Server
	Version string

	// Receipts is the node's earnings history, already read from disk. The
	// dashboard takes it rather than opening the file itself so that the one
	// place that knows where receipts live stays the one place.
	Receipts []receipts.Receipt

	// RegistryStatus reports whether this node's listing is healthy. Must be
	// non-nil; on an unlisted node it reports exactly that.
	RegistryStatus func() registry.Status
}

// Model is the dashboard state.
type Model struct {
	cfg     config.Config
	runner  *runner.Runner
	server  *httpapi.Server
	version string
	gpu     runner.GPU

	registryStatus func() registry.Status

	startedAt time.Time
	now       time.Time
	width     int
	height    int

	tab      tab
	showHelp bool

	feed []runner.Event

	// feedFilter and feedOffset belong to the Activity tab. feedOffset counts
	// lines back from the newest, so zero means pinned to live.
	feedFilter feedFilter
	feedOffset int

	// ledger is every settlement this node has ever taken, oldest first, seeded
	// from receipts.jsonl and appended to as payments land.
	ledger         []payment
	earnedTinybars int64

	gpuUtil    int
	gpuUsedMB  int
	gpuHistory []int

	serverErr error
	quitting  bool

	// confirm is the pending destructive action, if the provider has pressed
	// its key once. Evicting a lease takes a stranger's shell away mid-command,
	// which should not be one stray keystroke away.
	confirm     string
	confirmText string

	statusFlash string
	flashUntil  time.Time

	// eventCh is the live subscription to the runner's broker. program is the
	// bubbletea handle the dashboard runs under.
	eventCh chan runner.Event
	program *tea.Program
}

// Attach hands the model the program it runs under, so background work can
// push messages into the update loop.
func (m *Model) Attach(program *tea.Program) { m.program = program }

// New builds the dashboard model.
func New(opts Options) *Model {
	ledger := ledgerFrom(opts.Receipts)

	var earned int64
	for _, entry := range ledger {
		earned += entry.tinybars
	}

	status := opts.RegistryStatus
	if status == nil {
		status = func() registry.Status { return registry.Status{} }
	}

	return &Model{
		cfg:            opts.Config,
		runner:         opts.Runner,
		server:         opts.Server,
		version:        opts.Version,
		gpu:            opts.Runner.GPU(),
		registryStatus: status,
		startedAt:      time.Now(),
		now:            time.Now(),
		ledger:         ledger,
		earnedTinybars: earned,
		width:          100,
		height:         32,
	}
}

// ledgerFrom turns receipts into the dashboard's payment history, oldest first.
// A receipt whose timestamp will not parse still counts towards the lifetime
// total — the money moved — it just cannot be placed in a time window.
func ledgerFrom(all []receipts.Receipt) []payment {
	ledger := make([]payment, 0, len(all))
	for _, receipt := range all {
		at, err := time.Parse(time.RFC3339Nano, receipt.SettledAt)
		if err != nil {
			at = time.Time{}
		}
		ledger = append(ledger, payment{
			at:       at,
			tinybars: parseTinybars(receipt.AmountTinybars),
			payer:    receipt.Payer,
		})
	}
	sort.SliceStable(ledger, func(i, j int) bool { return ledger[i].at.Before(ledger[j].at) })
	return ledger
}

// Messages the dashboard reacts to.
type (
	tickMsg    time.Time
	gpuMsg     struct{ util, usedMB int }
	eventMsg   runner.Event
	serverDown struct{ err error }
)

func (m *Model) Init() tea.Cmd {
	backlog, ch := m.runner.Subscribe()
	m.eventCh = ch
	for _, event := range backlog {
		m.feed = append(m.feed, event)
	}
	m.trimFeed()

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
		m.now = time.Time(msg)
		return m, tick()

	case gpuMsg:
		m.gpuUtil, m.gpuUsedMB = msg.util, msg.usedMB
		m.gpuHistory = append(m.gpuHistory, msg.util)
		if len(m.gpuHistory) > gpuHistoryLen {
			m.gpuHistory = m.gpuHistory[len(m.gpuHistory)-gpuHistoryLen:]
		}
		return m, pollGPU(m.gpu.Available)

	case eventMsg:
		m.record(runner.Event(msg))
		return m, m.nextEvent(m.eventCh)

	case serverDown:
		m.serverErr = msg.err
		return m, tea.Quit
	}
	return m, nil
}

func (m *Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()

	// A pending confirmation swallows the next keystroke, whatever it is:
	// either it is the same key again and the action runs, or it is anything
	// else and the action is abandoned. Nothing destructive can happen by
	// accident on the way to something else.
	if m.confirm != "" {
		pending := m.confirm
		m.confirm, m.confirmText = "", ""
		if key == pending || key == "y" || key == "enter" {
			return m, m.runConfirmed(pending)
		}
		m.flash("cancelled")
		return m, nil
	}

	switch key {
	case "q", "ctrl+c":
		m.quitting = true
		return m, tea.Quit

	case "?":
		m.showHelp = !m.showHelp
		return m, nil
	}

	if m.showHelp {
		// Any other key closes help rather than acting behind it.
		m.showHelp = false
		return m, nil
	}

	switch key {
	case "1", "2", "3", "4":
		index := int(key[0] - '1')
		if index < len(tabOrder) {
			m.setTab(tabOrder[index])
		}

	case "tab", "right", "l":
		m.setTab(tabOrder[(int(m.tab)+1)%len(tabOrder)])

	case "shift+tab", "left", "h":
		m.setTab(tabOrder[(int(m.tab)+len(tabOrder)-1)%len(tabOrder)])

	case "up", "k":
		m.moveSelection(-1)

	case "down", "j":
		m.moveSelection(1)

	case "pgup":
		m.moveSelection(-10)

	case "pgdown":
		m.moveSelection(10)

	case "g":
		if m.tab == tabActivity {
			m.feedOffset = len(m.visibleFeed())
		}

	case "G":
		if m.tab == tabActivity {
			m.feedOffset = 0
		}

	case "f":
		if m.tab == tabActivity {
			m.feedFilter = (m.feedFilter + 1) % 3
			m.feedOffset = 0
			m.flash("showing " + m.feedFilter.title())
		}

	case "p":
		paused := !m.server.Paused()
		m.server.SetPaused(paused)
		if paused {
			m.flash("paused — running work continues, nothing new is sold")
		} else {
			m.flash("accepting work again")
		}

	case "e":
		m.askEndLease()
	}
	return m, nil
}

// setTab switches screens.
func (m *Model) setTab(next tab) {
	if next == m.tab {
		return
	}
	m.tab = next
	m.feedOffset = 0
}

// moveSelection is the arrow keys, which scroll the Activity feed.
func (m *Model) moveSelection(delta int) {
	switch m.tab {
	case tabActivity:
		m.feedOffset -= delta
		limit := len(m.visibleFeed())
		if m.feedOffset > limit {
			m.feedOffset = limit
		}
		if m.feedOffset < 0 {
			m.feedOffset = 0
		}
	}
}

// askEndLease arms the confirmation for evicting the lease running right now.
func (m *Model) askEndLease() {
	if !m.runner.LeasesEnabled() {
		return
	}
	lease, ok := m.runner.ActiveLease()
	if !ok || lease.Status().IsTerminal() {
		m.flash("no lease is running")
		return
	}

	outcome := "they are refunded " + formatHBARBig(lease.Credit()) + " of unburned credit"
	m.confirm = "e"
	m.confirmText = "end lease " + short(lease.ID) + "? " + outcome +
		" — press e again to confirm"
}

// runConfirmed performs a destructive action the provider has now pressed twice.
//
// It hands the work to a goroutine with its own context: stopping a
// container can take tens of seconds, and the dashboard must keep redrawing
// while it happens rather than appearing to hang on the keystroke.
func (m *Model) runConfirmed(action string) tea.Cmd {
	switch action {
	case "e":
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			m.server.EndLease(ctx, runner.LeaseStopped)
		}()
		m.flash("ending the lease — the machine comes back once the container stops")
	}
	return nil
}

func (m *Model) flash(text string) {
	m.statusFlash = text
	m.flashUntil = time.Now().Add(5 * time.Second)
}

func (m *Model) record(event runner.Event) {
	m.feed = append(m.feed, event)
	m.trimFeed()

	// Scrolled-back readers stay where they are put: a provider reading
	// something that happened a minute ago should not have it yanked out from
	// under them by a burn checkpoint.
	if m.feedOffset > 0 {
		m.feedOffset++
	}

	if event.Kind == runner.EventSettled {
		amount := parseTinybars(event.Tinybars)
		m.earnedTinybars += amount
		m.ledger = append(m.ledger, payment{at: event.At, tinybars: amount, payer: event.Payer})
	}
}

func (m *Model) trimFeed() {
	if len(m.feed) > maxFeedLines {
		m.feed = m.feed[len(m.feed)-maxFeedLines:]
	}
}

// earnedSince totals the payments that landed after a moment. Entries with an
// unparseable timestamp are left out rather than guessed at — a receipt that
// cannot be placed in time should not inflate "today".
func (m *Model) earnedSince(cutoff time.Time) (int64, int) {
	var total int64
	var count int
	for _, entry := range m.ledger {
		if entry.at.IsZero() || entry.at.Before(cutoff) {
			continue
		}
		total += entry.tinybars
		count++
	}
	return total, count
}
