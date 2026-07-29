package shell

import (
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"
)

var (
	overlayStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("#e6c945")).
			Padding(0, 2).
			Align(lipgloss.Center)
	overlayTitle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#e6c945"))
	overlayDim   = lipgloss.NewStyle().Foreground(lipgloss.Color("#8a8a8a"))
	overlaySel   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#e6c945"))
	highStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#4fc964"))
)

// compose overlays a lipgloss-rendered box onto the cached canvas frame by
// replacing whole vertically-centered lines. Canvas lines are self-contained
// ANSI (each ends with a reset), so whole-line replacement is safe; mid-line
// splicing would not be.
func compose(frame, box string) string {
	frameLines := strings.Split(frame, "\n")
	boxLines := strings.Split(box, "\n")
	if len(boxLines) >= len(frameLines) {
		return box
	}
	frameW := lipgloss.Width(frameLines[0])
	top := (len(frameLines) - len(boxLines)) / 2
	for i, bl := range boxLines {
		pad := frameW - lipgloss.Width(bl)
		left := pad / 2
		frameLines[top+i] = strings.Repeat(" ", left) + bl + strings.Repeat(" ", pad-left)
	}
	return strings.Join(frameLines, "\n")
}

func pauseBox(selected int) string {
	var b strings.Builder
	b.WriteString(overlayTitle.Render("PAUSED"))
	b.WriteString("\n\n")
	for i, c := range pauseChoices {
		if i == selected {
			b.WriteString(overlaySel.Render("▸ " + c))
		} else {
			b.WriteString(overlayDim.Render("  " + c))
		}
		if i < len(pauseChoices)-1 {
			b.WriteByte('\n')
		}
	}
	return overlayStyle.Render(b.String())
}

func gameOverBox(score int, newHigh bool) string {
	var b strings.Builder
	b.WriteString(overlayTitle.Render("GAME OVER"))
	b.WriteString("\n\nSCORE " + strconv.Itoa(score))
	if newHigh {
		b.WriteString("\n" + highStyle.Render("★ NEW HIGH SCORE ★"))
	}
	b.WriteString("\n\n" + overlayDim.Render("enter play again · m menu"))
	return overlayStyle.Render(b.String())
}

// crashBox reports a game panic. The shell survives these; the game doesn't.
func crashBox(msg string) string {
	const maxW = 40
	if len(msg) > maxW {
		msg = msg[:maxW-1] + "…"
	}
	var b strings.Builder
	b.WriteString(overlayTitle.Render("GAME CRASHED"))
	b.WriteString("\n\n" + overlayDim.Render(msg))
	b.WriteString("\n\n" + overlayDim.Render("any key returns to menu"))
	return overlayStyle.Render(b.String())
}

func spaces(n int) string {
	if n <= 0 {
		return ""
	}
	return strings.Repeat(" ", n)
}
