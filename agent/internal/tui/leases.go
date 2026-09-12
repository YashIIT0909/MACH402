package tui

import (
	"fmt"
	"math/big"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/YashIIT0909/ClearGate/agent/internal/config"
	"github.com/YashIIT0909/ClearGate/agent/internal/runner"
)

// leasingView is the screen for the trade a provider should watch hardest: a
// stranger with a shell on their machine.
//
// Everything a lease does that a job does not — freeze instead of kill, a
// credit that burns down, a refund the node owes back — is a number that only
// makes sense next to the others, which is why they get a screen rather than a
// line in a stack.
func (m *Model) leasingView(width, height int) string {
	body := width - 2

	if !m.runner.LeasesEnabled() {
		return indent(panel("interactive leasing", body,
			styleDim.Render("this node does not sell interactive time."),
			"",
			styleDim.Render("leasing hands a paying stranger a shell and a Jupyter server on"),
			styleDim.Render("this machine, so it is never switched on as a side effect of"),
			styleDim.Render("anything else. `cleargate-node setup --enable-leases` opts in,"),
			styleDim.Render("and `make lease-image` builds the runtime it needs."),
			"",
			styleDim.Render("until then POST /v1/leases answers 404 and nothing changes."),
		))
	}

	var b strings.Builder
	lease, live := m.runner.ActiveLease()
	if live && !lease.Status().IsTerminal() {
		b.WriteString(panel("the lease running now", body, m.leaseSummary(body-4)...))
		b.WriteString(panel("how the renter is connected", body, m.leaseAccess(body-4)...))
	} else {
		b.WriteString(panel("the lease running now", body,
			styleDim.Render("no active lease — this node's one slot is free"),
			"",
			styleDim.Render("one lease at a time in V1: a renter who pays gets the whole"),
			styleDim.Render("machine's lease slot until their time or their credit runs out."),
		))
	}

	b.WriteString(panel("terms", body, m.leaseTerms(body-4)...))

	rows := max(height-len(strings.Split(b.String(), "\n"))-3, 3)
	b.WriteString(panel("recent leases", body, m.leaseHistory(rows)...))
	return indent(b.String())
}

// leaseSummary is the live lease, and it is also what the Overview's "right
// now" panel shows — one renderer, so the two screens cannot disagree about
// what the node is doing.
func (m *Model) leaseSummary(width int) []string {
	lease, ok := m.runner.ActiveLease()
	if !ok || lease.Status().IsTerminal() {
		return []string{styleDim.Render("no active lease — listening for renters")}
	}

	state := lease.State()
	status := leaseStatusStyle(lease.Status()).Render(strings.ToUpper(string(lease.Status())))
	bars := max(min(width-30, 40), 10)

	head := fmt.Sprintf("%s  %s  %s",
		styleTitle.Render(short(lease.ID)), status,
		styleDim.Render("payer "+orNone(lease.Payer())))

	if lease.SessionID() == "" {
		// Direct-paid: what was bought is time, and time is the whole story.
		remaining := time.Until(lease.ExpiresAt())
		bought := time.Duration(state.PaidMinutes) * time.Minute
		used := 0.0
		if bought > 0 {
			used = 1 - float64(remaining)/float64(bought)
		}
		lines := []string{
			head,
			kv("bought", fmt.Sprintf("%d minute(s), forward-paid", state.PaidMinutes)),
			kv("time left", meter(1-used, bars, timeStyle(remaining))+"  "+duration(remaining)),
			kv("gpu", yesNo(state.GPU, "yes", "no")),
		}
		if overdue := lease.OverdueBy(); overdue > 0 {
			lines = append(lines, kv("overdue", styleWarn.Render(
				duration(overdue)+" past expiry — freezing, then reaped after "+
					fmt.Sprintf("%d min", m.cfg.Leases.GraceMinutes))))
		}
		if lease.Status() == runner.LeasePaused {
			lines = append(lines, kv("frozen", styleWarn.Render(
				"for "+duration(lease.PausedFor())+" — the container is paused, not destroyed")))
		}
		return lines
	}

	// Metered: the number that matters is the live credit, because that is
	// simultaneously "how much runway is left" and "what would be refunded
	// right now" — the same figure this node is publishing to its own HCS
	// topic on every sweep.
	credit := lease.Credit()
	burned := lease.Burned()
	total := new(big.Int).Add(credit, burned)

	left := 0.0
	if total.Sign() > 0 {
		left, _ = new(big.Float).Quo(new(big.Float).SetInt(credit), new(big.Float).SetInt(total)).Float64()
	}

	seconds := lease.SecondsRemaining()
	creditStyle := styleGood
	if seconds < int64(m.cfg.Leases.LowCreditThresholdSeconds) {
		creditStyle = styleWarn
	}

	lines := []string{
		head + styleDim.Render("  session "+short(lease.SessionID())),
		kv("credit", meter(left, bars, creditStyle)+"  "+
			styleMoney.Render(formatHBARBig(credit))+
			styleDim.Render(fmt.Sprintf("  %ds left", seconds))),
		kv("burned", formatHBARBig(burned)+styleDim.Render("  at "+
			formatHBARBig(lease.PricePerSecond())+" per second")),
		kv("owed back", styleTitle.Render(formatHBARBig(credit))+
			styleDim.Render("  if it stopped right now")),
		kv("settle", settleLine(lease)),
	}
	if lease.Status() == runner.LeasePaused {
		lines = append(lines, kv("frozen", styleWarn.Render(
			"for "+duration(lease.PausedFor())+" — a frozen session does not burn credit")))
	}
	return lines
}

// settleLine says where the refund stands, and says the uncomfortable part out
// loud when it applies. Between a chunk settling and the refund going out this
// node is holding money that is partly the renter's, and a dashboard that hid
// that would be the wrong dashboard.
func settleLine(lease *runner.Lease) string {
	switch lease.SettleState() {
	case runner.SettleDone:
		return styleGood.Render("refunded ") + formatHBARBig(lease.Refunded())
	default:
		return styleWarn.Render("pending") +
			styleDim.Render("  paid when the session ends; retried every sweep if it fails")
	}
}

func (m *Model) leaseAccess(width int) []string {
	lease, ok := m.runner.ActiveLease()
	if !ok {
		return []string{styleDim.Render("—")}
	}

	lines := []string{
		kv("in container", styleDim.Render("ssh ")+orNone(lease.SSHAddr())+
			styleDim.Render("   jupyter ")+orNone(lease.JupyterAddr())),
	}

	endpoints, tunnelled := m.server.Reach()
	if !tunnelled {
		return append(lines, styleDim.Render("no tunnel — leasing is configured without one"))
	}

	if endpoints.Mode == config.TunnelQuick {
		lines = append(lines,
			kv("tunnel", "quick"+styleDim.Render("  no Cloudflare account, random hostname")),
			kv("jupyter", orNone(endpoints.JupyterURL)),
			kv("ssh", styleWarn.Render("not available — a quick tunnel carries no TCP")))
		return lines
	}
	lines = append(lines,
		kv("tunnel", "named"+styleDim.Render("  stable hostnames, provisioned by the registry")),
		kv("jupyter", orNone(endpoints.JupyterURL)),
		kv("ssh", orNone(endpoints.SSHHost)))
	return lines
}

// leaseTerms is what a renter sees on /v1/specs before they pay, shown to the
// provider so that the offer they are making is never something they have to go
// and read their own config file to recall.
func (m *Model) leaseTerms(width int) []string {
	leases := m.cfg.Leases

	mode := "direct" + styleDim.Render("  forward-paid per slice, not refundable")
	if leases.PaymentMode == config.PaymentSession {
		switch {
		case !m.cfg.HCS.Enabled:
			// config.validate refuses this combination at load, so reaching it
			// here means a config hand-edited after the fact — worth a loud
			// line rather than a silent gap in what this node can prove.
			mode = styleBad.Render("session, but NOT publishing a refund trail — this should be impossible")
		case leases.SelfSettle:
			mode = styleAccent.Render("session") +
				styleDim.Render("  metered, refunds paid automatically, topic ") + m.cfg.HCS.TopicID
		default:
			mode = styleAccent.Render("session") +
				styleWarn.Render("  metered, refunds must be paid by hand") +
				styleDim.Render("  topic "+m.cfg.HCS.TopicID)
		}
	}

	return []string{
		kv("payment", mode),
		kv("price", formatHBAR(parseTinybars(leases.PriceTinybarsPerMinute))+
			styleDim.Render(" per minute   ")+
			fmt.Sprintf("%d–%d min per slice, %d max total",
				leases.MinMinutes, leases.MaxMinutes, leases.MaxTotalMinutes)),
		kv("image", truncate(leases.Image, width-14)),
		kv("gpu", yesNo(m.runner.LeaseGPU(),
			"a lease container can use this card",
			"no — the host has a card or the lease image has no CUDA runtime")),
		kv("overrun", fmt.Sprintf("frozen %ds past expiry, reaped %d min later",
			leases.OverrunSeconds, leases.GraceMinutes)),
		kv("egress", truncate(strings.Join(leases.Egress.Allowlist, ", "), width-14)),
	}
}

func (m *Model) leaseHistory(rows int) []string {
	all := m.runner.ListLeases()
	if len(all) == 0 {
		return []string{styleDim.Render("none yet")}
	}

	sortLeasesNewestFirst(all)
	if len(all) > rows-1 {
		all = all[:max(rows-1, 1)]
	}

	out := []string{styleHead.Render(fmt.Sprintf("%-14s %-12s %-9s %-6s %s",
		"LEASE", "STATUS", "PAID", "GPU", "EXPIRES"))}
	for _, lease := range all {
		out = append(out, leaseStatusStyle(lease.Status).Render(fmt.Sprintf("%-14s %-12s %-9s %-6s %s",
			short(lease.LeaseID),
			lease.Status,
			fmt.Sprintf("%d min", lease.PaidMinutes),
			gpuMark(lease.GPU),
			lease.ExpiresAt)))
	}
	return out
}

// sortLeasesNewestFirst orders the history by creation. The timestamps are
// RFC3339 in UTC, which sorts correctly as text, so this needs no parsing.
func sortLeasesNewestFirst(all []runner.LeaseState) {
	sort.SliceStable(all, func(i, j int) bool {
		return all[i].CreatedAt > all[j].CreatedAt
	})
}

func timeStyle(remaining time.Duration) lipgloss.Style {
	switch {
	case remaining <= 0:
		return styleBad
	case remaining < 2*time.Minute:
		return styleWarn
	default:
		return styleGood
	}
}
