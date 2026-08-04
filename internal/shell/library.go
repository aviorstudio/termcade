package shell

import (
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/aviorstudio/termcade/internal/engine"
)

// updateStyle marks a game the marketplace has moved past. Green rather than
// the notice yellow: an update is available, not a problem.
var updateStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#4fc964"))

// The library is every game in the arcade — builtins and installed — with
// broken installs listed dimmed so the player sees why they are missing.

// updateAvailable reports the newer version the marketplace publishes for an
// installed game, if there is one.
//
// It compares the version in the package's own manifest with the catalog's,
// and says nothing at all when either is unknown: a game whose manifest omits
// a version, or a catalog nothing has loaded yet, is not evidence that an
// update exists. Inequality rather than ordering, because a player running
// something newer than the marketplace has is a developer, and telling them to
// "update" to their own older release would be wrong in the other direction —
// so the marker names the version rather than commanding anything.
func (m Model) updateAvailable(g engine.Registration) (string, bool) {
	if g.Err != nil || g.Version == "" {
		return "", false
	}
	newer, ok := m.latest[g.Info.ID]
	if !ok || newer == "" || newer == g.Version {
		return "", false
	}
	return newer, true
}

func (m Model) updateLibraryKey(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "esc", "q":
		m.screen = screenMenu
		return m, nil
	case "up", "k":
		if n := len(m.games); n > 0 {
			m.libIdx = (m.libIdx + n - 1) % n
		}
	case "down", "j":
		if n := len(m.games); n > 0 {
			m.libIdx = (m.libIdx + 1) % n
		}
	case "m":
		if m.mp != nil {
			return m.openMarket()
		}
	case "r":
		return m.removeGame(m.libIdx)
	case "p":
		return m.cyclePixels()
	case "enter":
		if len(m.games) > 0 {
			return m.startGame(m.libIdx)
		}
	}
	return m, nil
}

func (m Model) viewLibrary() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("LIBRARY"))
	b.WriteString("\n\n")

	if len(m.games) == 0 {
		b.WriteString(dimStyle.Render("no games — visit the marketplace") + "\n")
	}
	for i, g := range m.games {
		line := "  " + sanitize(g.Info.Title)
		switch {
		case g.Err != nil:
			line += "  ··· " + sanitize(g.Err.Error())
			if i == m.libIdx {
				line = "▸" + line[1:]
			}
			b.WriteString(dimStyle.Render(line))
		default:
			if high := m.scores.High(g.Info.ID); high > 0 {
				line += dimStyle.Render("  ··· high " + strconv.Itoa(high))
			}
			if newer, ok := m.updateAvailable(g); ok {
				line += updateStyle.Render("  ··· v" + newer + " available")
			}
			if i == m.libIdx {
				b.WriteString(selectedStyle.Render("▸" + line[1:]))
			} else {
				b.WriteString(normalStyle.Render(line))
			}
		}
		b.WriteByte('\n')
	}

	if m.notice != "" {
		b.WriteString("\n" + dimStyle.Render(sanitize(m.notice)))
	}
	b.WriteString("\n")
	hint := "↑/↓ select · enter play · p pixels · esc back"
	if m.mp != nil {
		hint = "↑/↓ select · enter play · r remove · m marketplace · p pixels · esc back"
	}
	b.WriteString(dimStyle.Render(hint))
	return b.String()
}
