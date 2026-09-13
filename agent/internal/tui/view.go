package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/YashIIT0909/ClearGate/agent/internal/runner"
)

// chromeLines is what the header, tab strip and footer cost, and so what every
// tab has to draw within. Tabs are handed their height rather than measuring it
// themselves so that adding a line to the header cannot silently push the
// bottom of a table off the screen.
const chromeLines = 5

func (m *Model) View() string {
	if m.quitting {
		return m.farewell()
	}

	width := max(m.width, 64)
	height := max(m.height, 16)

	if m.showHelp {
		return m.helpView(width)
	}

	body := m.tabView(width, height-chromeLines)

	var b strings.Builder
	b.WriteString(m.statusBar(width))
	b.WriteString(m.tabStrip(width))
	b.WriteString("\n")
	b.WriteString(body)
	b.WriteString(m.footer(width))
	return b.String()
}

// farewell is the last thing a provider sees. It reports the session's takings
// and points at the file that can prove them, because the dashboard's own
// numbers vanish with the process and receipts.jsonl does not.
func (m *Model) farewell() string {
	if m.serverErr != nil {
		return styleBad.Render("node stopped: "+m.serverErr.Error()) + "\n"
	}
	session, count := m.earnedSince(m.startedAt)
	return fmt.Sprintf("node stopped. %d settlement(s) this session, %s earned.\n%s\n",
		count, styleMoney.Render(formatHBAR(session)),
		styleDim.Render("full history: "+m.cfg.ReceiptsPath+"  (cleargate-node earnings)"))
}

// tabView renders the selected screen, clipped to the height it was given so a
// short terminal loses the bottom of a panel rather than scrolling the header
// off the top.
func (m *Model) tabView(width, height int) string {
	var content string
	switch m.tab {
	case tabOverview:
		content = m.overviewView(width, height)
	case tabLeases:
		content = m.leasingView(width, height)
	case tabActivity:
		content = m.activityView(width, height)
	case tabNode:
		content = m.nodeView(width, height)
	}

	lines := strings.Split(strings.TrimRight(content, "\n"), "\n")
	if len(lines) > height {
		lines = lines[:height]
		lines[height-1] = styleDim.Render("  … the terminal is too short to show the rest")
	}
	for len(lines) < height {
		lines = append(lines, "")
	}
	return strings.Join(lines, "\n") + "\n"
}

// statusBar is the one line that is true on every screen: who this node is,
// whether it is selling, and what it has made.
func (m *Model) statusBar(width int) string {
	state := badge("ACCEPTING", colGood)
	if m.server.Paused() {
		state = badge("PAUSED", colWarn)
	}

	left := fmt.Sprintf("%s %s  %s  %s",
		styleAccent.Render("◆"),
		styleTitle.Render("ClearGate"),
		m.cfg.NodeID,
		state)

	right := fmt.Sprintf("%s   %s   %s",
		styleMoney.Render(formatHBAR(m.earnedTinybars)),
		styleDim.Render(m.cfg.Network),
		styleDim.Render(m.version+" · up "+duration(time.Since(m.startedAt))))

	return spread(left, right, width) + "\n"
}

// tabStrip carries a badge per tab, so a provider on the Overview can see that
// a session is live without switching to look.
func (m *Model) tabStrip(width int) string {
	parts := make([]string, 0, len(tabOrder))
	for i, t := range tabOrder {
		label := fmt.Sprintf("%d %s", i+1, t.title())
		if note := m.tabBadge(t); note != "" {
			label += " " + note
		}
		if t == m.tab {
			parts = append(parts, styleTabOn.Render(label))
		} else {
			parts = append(parts, styleTabOff.Render(label))
		}
	}
	strip := "  " + strings.Join(parts, styleBorder.Render("  │  "))
	return truncate(strip, width) + "\n"
}

func (m *Model) tabBadge(t tab) string {
	switch t {
	case tabLeases:
		if !m.runner.LeasesEnabled() {
			return styleDim.Render("(off)")
		}
		if lease, ok := m.runner.ActiveLease(); ok && !lease.Status().IsTerminal() {
			return styleGood.Render("●")
		}
	case tabActivity:
		if m.feedOffset > 0 {
			return styleWarn.Render("↑")
		}
	}
	return ""
}

// footer is the pending confirmation if there is one, then a flash message if
// one is fresh, and otherwise the keys that do something on this tab.
func (m *Model) footer(width int) string {
	if m.confirm != "" {
		return "\n  " + styleBad.Render(truncate(m.confirmText, width-4)) + "\n"
	}
	if m.statusFlash != "" && time.Now().Before(m.flashUntil) {
		return "\n  " + styleWarn.Render(truncate(m.statusFlash, width-4)) + "\n"
	}

	keys := []string{"1-4/tab screens"}
	switch m.tab {
	case tabLeases:
		keys = append(keys, "e end lease")
	case tabActivity:
		keys = append(keys, "↑↓ scroll", "g/G top/live", "f filter")
	}
	keys = append(keys, "p pause", "? help", "q quit")

	return "\n  " + styleDim.Render(truncate(strings.Join(keys, "  ·  "), width-4)) + "\n"
}

// spread puts left and right on one line with the gap between them, and gives
// the left side priority when the terminal is too narrow for both.
//
// Which side wins matters: the left is which node this is and whether it is
// selling, and a provider with several terminals open needs that even on a
// window too narrow for the uptime beside it.
func spread(left, right string, width int) string {
	available := width - 2

	if leftWidth := lipgloss.Width(left); leftWidth+lipgloss.Width(right)+2 > available {
		if leftWidth >= available {
			return " " + truncate(left, available)
		}
		right = truncate(right, available-leftWidth-2)
	}

	gap := available - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		gap = 1
	}
	return " " + left + strings.Repeat(" ", gap) + right
}

func (m *Model) helpView(width int) string {
	lines := func(rows ...string) []string { return rows }

	var b strings.Builder
	b.WriteString(" " + styleTitle.Render("ClearGate — provider dashboard") + "\n\n")

	b.WriteString(indent(cardRow(width-2, card{"screens", lines(
		kv("1 Overview", "earnings, this machine, and who can reach it"),
		kv("2 Leasing", "the shell or notebook a renter has on this box right now"),
		kv("3 Activity", "every payment and state change, filterable"),
		kv("4 Node", "the configuration this node is actually running under"),
		"",
		kv("tab / ←→", "next or previous screen"),
	)})))

	b.WriteString(indent(cardRow(width-2, card{"controls", lines(
		kv("↑ ↓ / j k", "scroll the activity feed"),
		kv("p", "pause or resume selling. A session already running continues;"),
		kv("", "new requests get a 503 instead of a 402, so nobody pays"),
		kv("", "for something this node will not start."),
		kv("e", "end the session running now. The renter is charged for"),
		kv("", "the seconds they used and refunded the rest."),
		kv("", ""),
		kv("", styleDim.Render("e asks once more before acting.")),
		kv("f", "on Activity, cycle what the feed shows"),
		kv("g / G", "on Activity, jump to the oldest kept line or back to live"),
		kv("? / q", "close this help / stop the node"),
	)})))

	b.WriteString(" " + styleDim.Render(
		"Earnings here are read from "+m.cfg.ReceiptsPath+", the same append-only file") + "\n")
	b.WriteString(" " + styleDim.Render(
		"`cleargate-node earnings` reads. It is the record that does not depend on this") + "\n")
	b.WriteString(" " + styleDim.Render(
		"display, on the registry, or on our website.") + "\n\n")
	b.WriteString(" " + styleDim.Render("press any key to go back") + "\n")
	return b.String()
}

func leaseStatusStyle(status runner.LeaseStatus) lipgloss.Style {
	switch status {
	case runner.LeaseActive:
		return styleRunning
	case runner.LeasePaused:
		return styleWarn
	case runner.LeaseStopped, runner.LeaseExpired:
		return styleDim
	case runner.LeaseFailed:
		return styleBad
	default:
		return lipgloss.NewStyle()
	}
}
