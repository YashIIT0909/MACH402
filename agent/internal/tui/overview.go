package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/YashIIT0909/ClearGate/agent/internal/config"
	"github.com/YashIIT0909/ClearGate/agent/internal/escrow"
)

// overviewView answers the four questions a provider opens this dashboard to
// ask, in the order they matter: am I being paid, is my machine working, what
// am I offering, and can anyone find me.
//
// The last of those is the one no other surface answers. A node with a typo in
// its registry URL sells perfectly well to anyone who knows its address and
// appears nowhere on the website, and until this panel existed the only
// evidence was a warning in a log file the dashboard had taken over the
// terminal from.
func (m *Model) overviewView(width, height int) string {
	body := width - 2

	var b strings.Builder
	b.WriteString(cardRow(body, m.earningsCard(), m.machineCard()))
	b.WriteString(cardRow(body, m.sellingCard(), m.reachCard()))
	b.WriteString(m.nowStrip(body, height))
	return indent(b.String())
}

func (m *Model) earningsCard() card {
	today, todayCount := m.earnedSince(startOfDay(m.now))
	day, dayCount := m.earnedSince(m.now.Add(-24 * time.Hour))
	session, sessionCount := m.earnedSince(m.startedAt)

	lines := []string{
		kv("lifetime", styleMoney.Render(formatHBAR(m.earnedTinybars))+
			styleDim.Render(fmt.Sprintf("  over %d settlement(s)", len(m.ledger)))),
		kv("today", formatHBAR(today)+styleDim.Render(fmt.Sprintf("  (%d)", todayCount))),
		kv("last 24h", formatHBAR(day)+styleDim.Render(fmt.Sprintf("  (%d)", dayCount))),
		kv("this run", formatHBAR(session)+styleDim.Render(fmt.Sprintf("  (%d)", sessionCount))),
		"",
	}

	if len(m.ledger) == 0 {
		lines = append(lines, styleDim.Render("no payments yet — waiting for the first renter"))
	} else {
		last := m.ledger[len(m.ledger)-1]
		when := styleDim.Render("just now")
		if !last.at.IsZero() {
			when = styleDim.Render(duration(m.now.Sub(last.at)) + " ago")
		}
		lines = append(lines,
			kv("last paid", formatHBAR(last.tinybars)+"  "+when),
			kv("", styleDim.Render("from "+orNone(last.payer))))
	}
	return card{"earnings", lines}
}

func (m *Model) machineCard() card {
	lines := make([]string, 0, 7)

	switch {
	case !m.gpu.Available && m.gpu.Model != "":
		// The card exists but cannot be passed through. Say so on every redraw:
		// a provider must never believe they are selling GPU time when they are
		// selling CPU time.
		lines = append(lines,
			kv("gpu", styleWarn.Render(m.gpu.Model)),
			kv("", styleBad.Render("present but NOT usable — selling CPU only")))
		if m.gpu.Reason != "" {
			lines = append(lines, kv("", styleDim.Render(truncate(m.gpu.Reason, 40))))
		}
	case !m.gpu.Available:
		lines = append(lines,
			kv("gpu", styleWarn.Render("none — this node sells CPU compute")))
	default:
		bars := max(min(m.width/2-26, 24), 8)
		vram := 0.0
		if m.gpu.VRAMMb > 0 {
			vram = float64(m.gpuUsedMB) / float64(m.gpu.VRAMMb)
		}
		lines = append(lines,
			kv("gpu", m.gpu.Model),
			kv("load", meter(float64(m.gpuUtil)/100, bars, loadStyle(m.gpuUtil))+
				fmt.Sprintf(" %3d%%", m.gpuUtil)),
			kv("vram", meter(vram, bars, styleRunning)+
				fmt.Sprintf(" %d/%d MB", m.gpuUsedMB, m.gpu.VRAMMb)),
			kv("trend", styleDim.Render(sparkline(m.gpuHistory, 100))))
	}

	lines = append(lines, "",
		kv("jobs", fmt.Sprintf("%d running · %d known", m.runningJobs(), len(m.jobs))),
		kv("docker", truncate(m.cfg.DockerHost, 40)))
	return card{"this machine", lines}
}

func (m *Model) sellingCard() card {
	selling := styleGood.Render("yes")
	if m.server.Paused() {
		selling = styleWarn.Render("paused by you")
	}

	lines := []string{
		kv("accepting", selling),
		kv("one job", styleMoney.Render(formatHBAR(parseTinybars(m.cfg.PriceTinybars)))),
		kv("paid into", orNone(m.cfg.PayTo)),
		"",
	}

	leases := m.cfg.Leases
	if !m.runner.LeasesEnabled() {
		lines = append(lines,
			kv("interactive", styleDim.Render("not offered")),
			kv("", styleDim.Render("setup --enable-leases turns it on")))
		return card{"what this node sells", lines}
	}

	if leases.PaymentMode == config.PaymentSession {
		rate := leases.PriceTinybarsPerSecond
		if rate == "" {
			if derived, err := escrow.PricePerSecond(leases.PriceTinybarsPerMinute); err == nil {
				rate = derived.String()
			}
		}
		lines = append(lines,
			kv("interactive", styleAccent.Render("metered session")+styleDim.Render(" · refundable")),
			kv("rate", styleMoney.Render(formatHBAR(parseTinybars(rate)))+styleDim.Render(" per second")),
			kv("chunk cap", fmt.Sprintf("%ds", leases.SessionChunkSeconds)+
				styleDim.Render("  most one payment ever buys")),
			kv("refunds", yesNo(leases.SelfSettle,
				"paid automatically by this node", "must be paid by hand")))
	} else {
		lines = append(lines,
			kv("interactive", "direct lease"+styleDim.Render(" · forward-paid, no refunds")),
			kv("rate", styleMoney.Render(formatHBAR(parseTinybars(leases.PriceTinybarsPerMinute)))+
				styleDim.Render(" per minute")),
			kv("slice", fmt.Sprintf("%d–%d min, %d max total",
				leases.MinMinutes, leases.MaxMinutes, leases.MaxTotalMinutes)))
	}
	lines = append(lines, kv("gpu in lease", yesNo(m.runner.LeaseGPU(),
		"yes", "no — the lease image has no CUDA runtime")))
	return card{"what this node sells", lines}
}

// reachCard is the discovery half: whether the website is offering this node,
// and by what route a renter actually arrives.
func (m *Model) reachCard() card {
	lines := []string{
		kv("listening", m.cfg.ListenAddr),
		kv("public url", orNone(truncate(m.cfg.PublicURL, 42))),
		kv("registry", m.registryLine()),
	}

	if endpoints, ok := m.server.Reach(); ok {
		mode := endpoints.Mode
		if mode == config.TunnelQuick {
			lines = append(lines, kv("tunnel", "quick"+
				styleWarn.Render("  Jupyter only — a quick tunnel carries no SSH")))
		} else {
			lines = append(lines, kv("tunnel", "named"+styleDim.Render("  SSH and Jupyter")))
		}
	}

	if m.cfg.Identity.AgentID != 0 {
		lines = append(lines, kv("identity", fmt.Sprintf("ERC-8004 agent #%d", m.cfg.Identity.AgentID)))
	} else {
		lines = append(lines, kv("identity", styleDim.Render("unregistered — `cleargate-node register`")))
	}

	if m.cfg.HCS.Enabled {
		lines = append(lines, kv("audit topic", styleGood.Render(m.cfg.HCS.TopicID)))
	} else {
		lines = append(lines, kv("audit topic", styleDim.Render("not publishing")))
	}
	return card{"how renters find you", lines}
}

// registryLine turns the announcer's last outcome into one readable verdict.
//
// Unlisted is not a fault and is not coloured like one: a node with no
// registry_url is fully functional, and renters who know its URL still pay it.
// A configured registry that is not answering is a fault, because the provider
// believes they are on the website and they are not.
func (m *Model) registryLine() string {
	status := m.registryStatus()

	switch {
	case !status.Configured:
		return styleDim.Render("unlisted — renters need this node's URL")
	case status.Listed():
		return styleGood.Render("listed") + styleDim.Render("  "+truncate(status.URL, 28))
	case status.LastSuccess.IsZero() && status.Err != "":
		return styleBad.Render("never reached") + styleDim.Render("  "+truncate(status.Err, 30))
	case status.Err != "":
		return styleBad.Render("stale "+duration(time.Since(status.LastSuccess))) +
			styleDim.Render("  "+truncate(status.Err, 26))
	default:
		return styleDim.Render("announcing…")
	}
}

// nowStrip is the live work, given whatever height the cards left over. On an
// idle node it says so in one line rather than leaving a hole.
func (m *Model) nowStrip(width, height int) string {
	if height < 14 {
		return ""
	}

	if m.runner.LeasesEnabled() {
		if lease, ok := m.runner.ActiveLease(); ok && !lease.Status().IsTerminal() {
			return panel("right now", width, m.leaseSummary(width-4)...)
		}
	}

	live := make([]string, 0, 4)
	for _, job := range m.jobs {
		if len(live) == 4 {
			break
		}
		if !job.Status.IsTerminal() {
			live = append(live, jobRow(job, width-4))
		}
	}
	if len(live) == 0 {
		return panel("right now", width,
			styleDim.Render("idle — nothing is running, and this node is listening on "+m.cfg.ListenAddr))
	}
	return panel("right now", width, live...)
}

func loadStyle(util int) lipgloss.Style {
	switch {
	case util >= 85:
		return styleGood
	case util >= 30:
		return styleRunning
	default:
		return styleDim
	}
}

func startOfDay(now time.Time) time.Time {
	year, month, day := now.Date()
	return time.Date(year, month, day, 0, 0, 0, 0, now.Location())
}

// indent shifts a block one column in from the edge, which is what gives the
// panels the same left margin as the header and the footer.
func indent(block string) string {
	lines := strings.Split(strings.TrimRight(block, "\n"), "\n")
	for i, line := range lines {
		lines[i] = " " + line
	}
	return strings.Join(lines, "\n") + "\n"
}
