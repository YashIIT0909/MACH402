package tui

import (
	"fmt"
	"strings"

	"github.com/YashIIT0909/ClearGate/agent/internal/runner"
)

// activityView is the node's running commentary: every payment, every lease
// opening and closing, in order.
//
// It gets a whole screen because the old five-line feed could not hold one
// settlement plus the burn checkpoints a metered session publishes every
// fifteen seconds. Those checkpoints are the thing that makes a session
// trustworthy, so the answer is a filter and a scrollback rather than
// publishing fewer of them.
func (m *Model) activityView(width, height int) string {
	body := width - 2
	rows := max(height-3, 3)

	feed := m.visibleFeed()
	if len(feed) == 0 {
		return indent(panel("activity", body,
			styleDim.Render("nothing yet — waiting for the first request"),
			"",
			styleDim.Render("a renter's first call gets a 402 with this node's price in the"),
			styleDim.Render("PAYMENT-REQUIRED header. That challenge shows up here, and so"),
			styleDim.Render("does the settlement that follows it."),
		))
	}

	end := len(feed) - m.feedOffset
	end = max(min(end, len(feed)), 1)
	start := max(end-rows, 0)

	lines := make([]string, 0, rows)
	for _, event := range feed[start:end] {
		lines = append(lines, truncate(renderEvent(event), body-4))
	}
	// Newest at the bottom of a fixed frame, so a quiet node's feed does not
	// jump up the screen every time something happens.
	for len(lines) < rows {
		lines = append([]string{""}, lines...)
	}

	title := "activity · " + m.feedFilter.title()
	if m.feedOffset > 0 {
		title += fmt.Sprintf(" · scrolled back %d, G returns to live", m.feedOffset)
	}
	return indent(panel(title, body, lines...))
}

// visibleFeed applies the current filter. Ordering is untouched: the feed is
// the order things happened, and a filter that reordered it would be lying
// about a sequence a provider may be trying to reconstruct.
func (m *Model) visibleFeed() []runner.Event {
	if m.feedFilter == filterAll {
		return m.feed
	}

	out := make([]runner.Event, 0, len(m.feed))
	for _, event := range m.feed {
		if matchesFilter(event.Kind, m.feedFilter) {
			out = append(out, event)
		}
	}
	return out
}

func matchesFilter(kind runner.EventKind, filter feedFilter) bool {
	switch filter {
	case filterMoney:
		switch kind {
		case runner.EventChallenged, runner.EventVerified, runner.EventRejected,
			runner.EventSettled, runner.EventSettleFailed, runner.EventSessionBurn:
			return true
		}
	case filterLeases:
		switch kind {
		case runner.EventLeaseStarted,
			runner.EventLeasePaused, runner.EventLeaseEnded, runner.EventSessionBurn:
			return true
		}
	}
	return false
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
	// A provider should be able to watch a stranger's shell open on their
	// machine and close again, in the same feed as the money.
	case runner.EventLeaseStarted:
		return fmt.Sprintf("%s %s %s  %s", stamp,
			styleRunning.Render("lease open"), short(event.LeaseID), event.Detail)
	case runner.EventLeasePaused:
		return fmt.Sprintf("%s %s %s  %s", stamp,
			styleWarn.Render("lease frozen"), short(event.LeaseID), event.Detail)
	case runner.EventLeaseEnded:
		return fmt.Sprintf("%s %s %s  %s", stamp,
			styleDim.Render("lease ended"), short(event.LeaseID), event.Detail)
	// The same figure this tick just published to the provider's own HCS
	// topic — visible here too, so watching a session's credit count down
	// does not require reading a mirror node.
	case runner.EventSessionBurn:
		return fmt.Sprintf("%s %s %s  %s", stamp,
			styleDim.Render("session burn"), short(event.LeaseID), event.Detail)
	default:
		return fmt.Sprintf("%s %s %s", stamp, event.Kind, event.Detail)
	}
}

// nodeView is the configuration this node is actually running under, and the
// trust posture that follows from it.
//
// It exists because the honest answers to "where does my money go" and "what
// can a renter's container reach" live in a YAML file the provider wrote once
// and a set of invariants they have to take on faith. Printing them where the
// node is running turns both into something they can check.
func (m *Model) nodeView(width, height int) string {
	body := width - 2

	var b strings.Builder
	b.WriteString(cardRow(body, m.moneyCard(), m.keysCard()))
	b.WriteString(cardRow(body, m.sandboxCard(), m.originsCard()))
	return indent(b.String())
}

func (m *Model) moneyCard() card {
	return card{"payment", []string{
		kv("network", m.cfg.Network),
		kv("asset", assetName(m.cfg.Asset)),
		kv("facilitator", truncate(m.cfg.FacilitatorURL, 40)),
		kv("payload ttl", fmt.Sprintf("%ds", m.cfg.MaxTimeoutSeconds)+
			styleDim.Render("  a signed payment expires after this")),
		kv("pay_to", orNone(m.cfg.PayTo)),
		kv("", styleDim.Render("earnings land here; it never signs.")),
		kv("receipts", m.cfg.ReceiptsPath),
		kv("", styleDim.Render("append-only, read by `earnings`")),
	}}
}

// keysCard is the one panel that should make a provider comfortable rather than
// impressed: it says exactly which key this machine holds and what it can spend.
func (m *Model) keysCard() card {
	lines := []string{
		kv("hedera key", yesNo(m.cfg.Hedera.Enabled,
			"one, node-local, read only by the sidecar", "none on this machine")),
	}

	if !m.cfg.Hedera.Enabled {
		return card{"keys and trust", append(lines,
			kv("", styleDim.Render("this node never signs. it receives, and the")),
			kv("", styleDim.Render("facilitator submits what the renter signed.")),
			kv("audit topic", styleDim.Render("not publishing")),
			kv("identity", styleDim.Render("unregistered")))}
	}

	lines = append(lines,
		kv("operator", orNone(m.cfg.Hedera.OperatorAccountID)),
		kv("it signs", styleDim.Render("topic writes, ERC-8004 registration")))
	if m.cfg.Leases.Enabled {
		lines = append(lines, kv("", styleDim.Render("and session refunds — so it holds a float")))
	}
	lines = append(lines,
		kv("", styleDim.Render("a compromise costs that float and cannot")),
		kv("", styleDim.Render("touch pay_to, where earnings are.")),
		kv("mirror", truncate(m.cfg.Hedera.MirrorURL, 40)),
		kv("audit topic", func() string {
			if m.cfg.HCS.Enabled {
				return styleGood.Render(m.cfg.HCS.TopicID)
			}
			return styleDim.Render("not publishing")
		}()))

	if m.cfg.Identity.AgentID != 0 {
		lines = append(lines,
			kv("identity", fmt.Sprintf("agent #%d", m.cfg.Identity.AgentID)),
			kv("", styleDim.Render(truncate(m.cfg.Identity.AgentAddress, 40))))
	} else {
		lines = append(lines, kv("identity", styleDim.Render("unregistered — `cleargate-node register`")))
	}
	return card{"keys and trust", lines}
}

func (m *Model) sandboxCard() card {
	leases := m.cfg.Leases
	return card{"session sandbox", []string{
		kv("network", styleGood.Render("internal")+
			styleDim.Render("  out only through the egress proxy")),
		kv("egress", fmt.Sprintf("%d allowlisted host(s)", len(leases.Egress.Allowlist))+
			styleDim.Render("  listed on Leasing")),
		kv("memory", fmt.Sprintf("%d MB", leases.Limits.MemoryMB)),
		kv("cpu", fmt.Sprintf("%d core(s)", leases.Limits.CPUCores)),
		kv("workspace", fmt.Sprintf("%d GB", leases.Limits.WorkspaceGB)+
			styleDim.Render("  wiped when the session is reaped")),
		kv("image", truncate(leases.Image, 40)),
	}}
}

func (m *Model) originsCard() card {
	return card{"browser origins", []string{
		kv("allowed", truncate(strings.Join(m.cfg.CORS.AllowedOrigins, ", "), 34)),
		kv("", styleDim.Render("no cookies are ever accepted, so a wide")),
		kv("", styleDim.Render("origin list grants a page nothing extra.")),
	}}
}

func assetName(asset string) string {
	if asset == "0.0.0" || asset == "" {
		return "HBAR" + styleDim.Render("  (0.0.0)")
	}
	return "HTS token " + asset
}
