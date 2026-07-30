package shell

import (
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
)

// The library is every game in the arcade — builtins and installed — with
// broken installs listed dimmed so the player sees why they are missing.

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
