package shell

import (
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

var (
	logoStyle     = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#3fc4c9"))
	selectedStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#e6c945"))
	normalStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("#f2f2f2"))
)

const logo = `
▄▄▄▄▄▄ ▄▄▄▄▄ ▄▄▄▄▄  ▄▄   ▄▄  ▄▄▄▄  ▄▄▄▄▄  ▄▄▄▄▄  ▄▄▄▄▄
  ██   ██▄▄  ██▄▄▄▀ ██▀▄▀██ ██  ▀▀ ██▄▄██ ██  ██ ██▄▄
  ██   ██▄▄▄ ██  ██ ██   ██  ▀███▀ ██  ██ ██▄▄█▀ ██▄▄▄
`

func (m Model) updateMenuKey(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "q":
		m.scores.Save()
		return m, tea.Quit
	case "up", "k":
		m.menuIdx = (m.menuIdx + len(m.games) - 1) % len(m.games)
	case "down", "j":
		m.menuIdx = (m.menuIdx + 1) % len(m.games)
	case "m":
		if m.mp != nil {
			return m.openMarket()
		}
	case "r":
		return m.removeFromMenu()
	case "enter":
		return m.startGame(m.menuIdx)
	}
	return m, nil
}

// removeFromMenu removes the selected game when it is an installed one.
// Builtins are compiled in — nothing to remove — and an installed game that
// shadows a builtin reverts to the builtin on the reload.
func (m Model) removeFromMenu() (tea.Model, tea.Cmd) {
	if m.mp == nil || m.market.busy || len(m.games) == 0 {
		return m, nil
	}
	reg := m.games[m.menuIdx]
	if !reg.Installed {
		m.notice = reg.Info.Title + " is built in — nothing to remove"
		return m, nil
	}
	m.market.busy = true
	m.notice = "removing " + reg.Info.ID + "…"
	return m, m.removeCmd(reg.Info.ID)
}

func (m Model) viewMenu() string {
	var b strings.Builder
	b.WriteString(logoStyle.Render(strings.TrimLeft(logo, "\n")))
	b.WriteString("\n\n")
	for i, g := range m.games {
		// Titles and errors can derive from on-disk manifests; validation
		// rejects control characters already, but the menu strips them again
		// rather than trusting that every path here was validated.
		line := "  " + sanitize(g.Info.Title)
		switch {
		case g.Err != nil:
			line += "  ··· " + sanitize(g.Err.Error())
			if i == m.menuIdx {
				line = "▸" + line[1:]
			}
			b.WriteString(dimStyle.Render(line))
		case i == m.menuIdx:
			if high := m.scores.High(g.Info.ID); high > 0 {
				line += dimStyle.Render("  ··· high " + strconv.Itoa(high))
			}
			b.WriteString(selectedStyle.Render("▸" + line[1:]))
		default:
			if high := m.scores.High(g.Info.ID); high > 0 {
				line += dimStyle.Render("  ··· high " + strconv.Itoa(high))
			}
			b.WriteString(normalStyle.Render(line))
		}
		b.WriteByte('\n')
	}
	b.WriteString("\n")
	hint := "↑/↓ select · enter play · q quit"
	if m.mp != nil {
		hint = "↑/↓ select · enter play · m marketplace · r remove · q quit"
	}
	b.WriteString(dimStyle.Render(hint))
	if m.notice != "" {
		b.WriteString("\n\n" + dimStyle.Render(m.notice))
	}
	return b.String()
}
