package tui

import (
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// Colours are adaptive: a provider's terminal may be light or dark, and the
// dashboard has to stay readable in both. Nothing here assumes 24-bit colour —
// these are 256-colour indices, which is the most a provider's box over SSH can
// be relied on to have.
var (
	colAccent = lipgloss.AdaptiveColor{Light: "57", Dark: "141"}
	colMuted  = lipgloss.AdaptiveColor{Light: "245", Dark: "243"}
	colBorder = lipgloss.AdaptiveColor{Light: "252", Dark: "238"}
	colGood   = lipgloss.AdaptiveColor{Light: "28", Dark: "42"}
	colWarn   = lipgloss.AdaptiveColor{Light: "166", Dark: "214"}
	colBad    = lipgloss.AdaptiveColor{Light: "160", Dark: "203"}
	colInfo   = lipgloss.AdaptiveColor{Light: "26", Dark: "39"}
	colOnDark = lipgloss.AdaptiveColor{Light: "231", Dark: "16"}
)

var (
	styleTitle   = lipgloss.NewStyle().Bold(true)
	styleDim     = lipgloss.NewStyle().Foreground(colMuted)
	styleBorder  = lipgloss.NewStyle().Foreground(colBorder)
	styleAccent  = lipgloss.NewStyle().Foreground(colAccent)
	styleMoney   = lipgloss.NewStyle().Bold(true).Foreground(colGood)
	styleWarn    = lipgloss.NewStyle().Foreground(colWarn)
	styleBad     = lipgloss.NewStyle().Foreground(colBad)
	styleGood    = lipgloss.NewStyle().Foreground(colGood)
	styleRunning = lipgloss.NewStyle().Foreground(colInfo)
	styleSelect  = lipgloss.NewStyle().Bold(true).Foreground(colAccent)
	styleHead    = lipgloss.NewStyle().Bold(true).Foreground(colMuted)

	styleTabOn  = lipgloss.NewStyle().Bold(true).Foreground(colAccent)
	styleTabOff = lipgloss.NewStyle().Foreground(colMuted)
)

// badge is the small filled label the header uses for a node's selling state.
// Foreground and background are both set, so it never inherits a terminal
// colour that makes it unreadable.
func badge(text string, background lipgloss.TerminalColor) string {
	return lipgloss.NewStyle().
		Bold(true).
		Foreground(colOnDark).
		Background(background).
		Render(" " + text + " ")
}

// card is one titled box of the dashboard, before it knows how wide it will be.
//
// Separating the content from the geometry is what lets a row of cards be laid
// out as two columns on a wide terminal and one on a narrow one without every
// caller knowing which happened.
type card struct {
	title string
	lines []string
}

// cardRow lays cards out across the available width, wrapping to as many rows
// as it takes. Cards in the same row are padded to a common height so their
// bottom borders line up.
func cardRow(width int, cards ...card) string {
	if len(cards) == 0 {
		return ""
	}

	const gap = 2
	perRow := len(cards)
	for perRow > 1 && (width-gap*(perRow-1))/perRow < 34 {
		perRow--
	}

	var out strings.Builder
	for start := 0; start < len(cards); start += perRow {
		end := min(start+perRow, len(cards))
		out.WriteString(renderCards(width, gap, cards[start:end]))
	}
	return out.String()
}

func renderCards(width, gap int, cards []card) string {
	count := len(cards)
	each := (width - gap*(count-1)) / count

	tallest := 0
	for _, c := range cards {
		tallest = max(tallest, len(c.lines))
	}

	boxes := make([]string, 0, count)
	for i, c := range cards {
		boxWidth := each
		if i == count-1 {
			// The last card absorbs the rounding, so the row reaches the edge.
			boxWidth = width - (each+gap)*(count-1)
		}
		lines := c.lines
		for len(lines) < tallest {
			lines = append(lines, "")
		}
		boxes = append(boxes, panel(c.title, boxWidth, lines...))
		if i < count-1 {
			boxes = append(boxes, strings.Repeat(" ", gap))
		}
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, boxes...) + "\n"
}

// panel draws a titled box. The title sits in the top border, which buys back
// the line a separate heading would have cost — worth having on a dashboard
// that has to fit a provider's whole node into one screen.
func panel(title string, width int, lines ...string) string {
	inner := width - 4
	if inner < 8 {
		inner = 8
		width = inner + 4
	}

	// "╭─ " + title + " " + fill + "╮" has to come to exactly width cells.
	styled := styleTitle.Render(truncate(strings.ToUpper(title), inner))
	fill := width - 5 - lipgloss.Width(styled)
	if fill < 0 {
		fill = 0
	}

	var b strings.Builder
	b.WriteString(styleBorder.Render("╭─") + " " + styled + " " +
		styleBorder.Render(strings.Repeat("─", fill)+"╮") + "\n")
	for _, line := range lines {
		b.WriteString(styleBorder.Render("│") + " " + pad(line, inner) + " " +
			styleBorder.Render("│") + "\n")
	}
	b.WriteString(styleBorder.Render("╰"+strings.Repeat("─", width-2)+"╯") + "\n")
	return b.String()
}

// kv is one label/value line inside a card. Labels are a fixed column so the
// values down a card line up, which is most of what makes a dense panel
// scannable rather than a wall of text.
func kv(label, value string) string {
	return styleDim.Render(pad(label, 13)) + value
}

// meter is a proportional bar. Used for GPU load, VRAM, and the credit left on
// a metered session — the three numbers a provider reads as a level rather than
// as a figure.
func meter(fraction float64, width int, style lipgloss.Style) string {
	if width < 1 {
		return ""
	}
	if fraction < 0 {
		fraction = 0
	}
	if fraction > 1 {
		fraction = 1
	}
	filled := int(fraction*float64(width) + 0.5)
	return style.Render(strings.Repeat("█", filled)) +
		styleBorder.Render(strings.Repeat("░", width-filled))
}

// sparkline draws recent history in one line. It is how a provider tells a card
// that is genuinely busy from one that spiked when they looked at it.
func sparkline(samples []int, ceiling int) string {
	if len(samples) == 0 || ceiling <= 0 {
		return ""
	}
	// No blank in the ramp: a sample is a reading, and an idle GPU should draw
	// a low mark rather than a gap that reads as missing data.
	glyphs := []rune("▁▂▃▄▅▆▇█")

	var b strings.Builder
	for _, sample := range samples {
		index := sample * (len(glyphs) - 1) / ceiling
		if index < 0 {
			index = 0
		}
		if index >= len(glyphs) {
			index = len(glyphs) - 1
		}
		b.WriteRune(glyphs[index])
	}
	return b.String()
}

// pad truncates to a visible width and then fills the remainder with spaces.
func pad(text string, width int) string {
	text = truncate(text, width)
	if gap := width - lipgloss.Width(text); gap > 0 {
		text += strings.Repeat(" ", gap)
	}
	return text
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

// yesNo renders a boolean as something a provider can read at a glance, with
// the colour carrying whether it is the answer they wanted.
func yesNo(value bool, whenTrue, whenFalse string) string {
	if value {
		return styleGood.Render(whenTrue)
	}
	return styleDim.Render(whenFalse)
}

// orNone substitutes a visible placeholder for an empty value, so a blank
// setting reads as "not set" rather than as a rendering bug.
func orNone(value string) string {
	if strings.TrimSpace(value) == "" {
		return styleDim.Render("—")
	}
	return value
}

// short clips an identifier to its first eight characters, which is enough to
// tell this node's jobs apart without spending a column on the rest.
func short(id string) string {
	if len(id) <= 8 {
		return id
	}
	return id[:8]
}

// duration renders a span the way a provider reads one: coarse when it is long,
// exact when it is nearly up.
func duration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	switch {
	case d >= 24*time.Hour:
		return fmt.Sprintf("%dd %dh", int(d.Hours())/24, int(d.Hours())%24)
	case d >= time.Hour:
		return fmt.Sprintf("%dh %02dm", int(d.Hours()), int(d.Minutes())%60)
	case d >= time.Minute:
		return fmt.Sprintf("%dm %02ds", int(d.Minutes()), int(d.Seconds())%60)
	default:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
}

// formatHBAR renders tinybars as HBAR using integer arithmetic only. Payment
// amounts are never floats anywhere in this project.
func formatHBAR(tinybars int64) string {
	return formatHBARBig(big.NewInt(tinybars))
}

// formatHBARBig is formatHBAR for the *big.Int amounts a metered session's
// ledger produces. A session's own numbers are never small enough to risk
// truncating into an int64 by construction, but they are still tinybars and
// deserve the same never-a-float treatment as everything else here.
func formatHBARBig(tinybars *big.Int) string {
	perHBAR := big.NewInt(100_000_000)
	whole, frac := new(big.Int).QuoRem(tinybars, perHBAR, new(big.Int))
	if frac.Sign() == 0 {
		return whole.String() + " HBAR"
	}
	fraction := strings.TrimRight(fmt.Sprintf("%08d", frac.Abs(frac)), "0")
	return whole.String() + "." + fraction + " HBAR"
}

// parseTinybars reads an amount string. Amounts are strings end to end in this
// project; this is the one place the dashboard turns one into a number, and it
// stays integral.
func parseTinybars(raw string) int64 {
	if raw == "" {
		return 0
	}
	value, ok := new(big.Int).SetString(raw, 10)
	if !ok || !value.IsInt64() {
		return 0
	}
	return value.Int64()
}
