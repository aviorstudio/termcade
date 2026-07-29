package shell

import (
	"sort"
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

// The index is a launcher, not a list: the last few games you played, then
// the places to find everything else.

const maxRecent = 3

// recentGames returns indexes into m.games of the most recently played
// launchable games, newest first, capped at maxRecent.
func (m Model) recentGames() []int {
	var idxs []int
	for i, g := range m.games {
		if g.Err == nil && !m.scores.LastPlayed(g.Info.ID).IsZero() {
			idxs = append(idxs, i)
		}
	}
	sort.Slice(idxs, func(a, b int) bool {
		return m.scores.LastPlayed(m.games[idxs[a]].Info.ID).
			After(m.scores.LastPlayed(m.games[idxs[b]].Info.ID))
	})
	if len(idxs) > maxRecent {
		idxs = idxs[:maxRecent]
	}
	return idxs
}

// indexRows is how many selectable rows the index has: recent games plus the
// LIBRARY action, plus MARKETPLACE when one is wired.
func (m Model) indexRows(recent []int) int {
	rows := len(recent) + 1
	if m.mp != nil {
		rows++
	}
	return rows
}

func (m Model) updateMenuKey(key string) (tea.Model, tea.Cmd) {
	recent := m.recentGames()
	rows := m.indexRows(recent)
	switch key {
	case "q":
		m.scores.Save()
		return m, tea.Quit
	case "up", "k":
		m.menuIdx = (m.menuIdx + rows - 1) % rows
	case "down", "j":
		m.menuIdx = (m.menuIdx + 1) % rows
	case "l":
		return m.openLibrary()
	case "m":
		if m.mp != nil {
			return m.openMarket()
		}
	case "r":
		if m.menuIdx < len(recent) {
			return m.removeGame(recent[m.menuIdx])
		}
	case "enter":
		switch {
		case m.menuIdx < len(recent):
			return m.startGame(recent[m.menuIdx])
		case m.menuIdx == len(recent):
			return m.openLibrary()
		default:
			return m.openMarket()
		}
	}
	return m, nil
}

func (m Model) openLibrary() (tea.Model, tea.Cmd) {
	m.screen = screenLibrary
	if m.libIdx >= len(m.games) {
		m.libIdx = 0
	}
	return m, nil
}

// removeGame removes m.games[idx]. Every game is an installed package now —
// bundled starter games included — so anything healthy can be removed and
// re-added from the marketplace later.
func (m Model) removeGame(idx int) (tea.Model, tea.Cmd) {
	if m.mp == nil || m.market.busy || idx >= len(m.games) {
		return m, nil
	}
	reg := m.games[idx]
	if reg.Err != nil {
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

	recent := m.recentGames()
	if len(recent) == 0 {
		b.WriteString(dimStyle.Render("nothing played yet — pick a game from the library") + "\n")
	} else {
		b.WriteString(dimStyle.Render("recently played") + "\n")
		for row, idx := range recent {
			g := m.games[idx]
			line := "  " + sanitize(g.Info.Title)
			if high := m.scores.High(g.Info.ID); high > 0 {
				line += dimStyle.Render("  ··· high " + strconv.Itoa(high))
			}
			if row == m.menuIdx {
				b.WriteString(selectedStyle.Render("▸" + line[1:]))
			} else {
				b.WriteString(normalStyle.Render(line))
			}
			b.WriteByte('\n')
		}
	}
	b.WriteByte('\n')

	actions := []string{"LIBRARY", "MARKETPLACE"}
	if m.mp == nil {
		actions = actions[:1]
	}
	for i, label := range actions {
		row := len(recent) + i
		line := "  → " + label
		if row == m.menuIdx {
			b.WriteString(selectedStyle.Render("▸" + line[1:]))
		} else {
			b.WriteString(normalStyle.Render(line))
		}
		b.WriteByte('\n')
	}

	if m.notice != "" {
		b.WriteString("\n" + dimStyle.Render(sanitize(m.notice)))
	}
	b.WriteString("\n")
	hint := "↑/↓ select · enter open · l library · q quit"
	if m.mp != nil {
		hint = "↑/↓ select · enter open · l library · m marketplace · q quit"
	}
	b.WriteString(dimStyle.Render(hint))
	return b.String()
}
