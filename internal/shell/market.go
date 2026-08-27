package shell

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/aviorstudio/termcade/internal/engine"
)

// MarketGame is one marketplace catalog entry, as the shell displays it.
type MarketGame struct {
	ID          string // "author/slug"
	Name        string
	Description string
	Version     string
	HasPackage  bool
}

// Marketplace is everything the shell needs from the outside world to run
// the marketplace and account screens. All calls may block; the shell only
// invokes them inside tea commands. A nil *Marketplace disables the screens.
type Marketplace struct {
	List    func() ([]MarketGame, error)
	Install func(id string) error
	Remove  func(id string) error
	// Account reports the signed-in email, or ok=false when signed out.
	Account func() (string, bool)
	SignOut func() error
	// Reload re-discovers installed games after an install/remove.
	Reload func() []engine.Registration
	// Sync flushes queued runs to the account, folds the account's record back
	// into the local one, and reports the newest published version of each
	// game so the library can say which installs have fallen behind.
	//
	// The notice is what to show, or "" for the ordinary case where nothing
	// happened and nobody needs telling.
	//
	// It returns no error on purpose. Being signed out, being offline, and
	// having nothing to send are all the normal state of an arcade that works
	// without an account — none of them is a failure worth interrupting
	// somebody over, and the queue survives all three.
	Sync func() (notice string, latest map[string]string)
}

type marketLoadedMsg struct {
	games []MarketGame
	err   error
}

type marketOpMsg struct {
	id   string
	verb string // "added" | "removed"
	err  error
}

// syncedMsg carries whatever a sync wants said, which is usually nothing, and
// what the marketplace currently publishes.
type syncedMsg struct {
	notice string
	latest map[string]string
}

type marketState struct {
	games  []MarketGame
	loaded bool
	idx    int
	notice string
	busy   bool
}

type authState struct{}

func (m Model) loadMarket() tea.Cmd {
	mp := m.mp
	return func() tea.Msg {
		games, err := mp.List()
		return marketLoadedMsg{games: games, err: err}
	}
}

func (m Model) installCmd(id string) tea.Cmd {
	mp := m.mp
	return func() tea.Msg {
		return marketOpMsg{id: id, verb: "added", err: mp.Install(id)}
	}
}

func (m Model) removeCmd(id string) tea.Cmd {
	mp := m.mp
	return func() tea.Msg {
		return marketOpMsg{id: id, verb: "removed", err: mp.Remove(id)}
	}
}

// syncCmd runs a sync off the UI loop. A nil Marketplace or a nil Sync hook
// yields no command at all, so an arcade built without one is not paying for a
// goroutine per game over.
func (m Model) syncCmd() tea.Cmd {
	if m.mp == nil || m.mp.Sync == nil {
		return nil
	}
	mp := m.mp
	return func() tea.Msg {
		notice, latest := mp.Sync()
		return syncedMsg{notice: notice, latest: latest}
	}
}

// latestVersions is what the marketplace currently publishes, by game id.
// Games with no release are left out — there is no version to fall behind.
func latestVersions(games []MarketGame) map[string]string {
	out := make(map[string]string, len(games))
	for _, g := range games {
		if g.HasPackage && g.Version != "" {
			out[g.ID] = g.Version
		}
	}
	return out
}

// installedIDs is derived from the menu's registrations, so marketplace
// markers always agree with what the menu shows.
func (m Model) installedIDs() map[string]bool {
	out := make(map[string]bool, len(m.games))
	for _, g := range m.games {
		if g.Err == nil {
			out[g.Info.ID] = true
		}
	}
	return out
}

func (m Model) updateMarketMsg(msg tea.Msg) (Model, tea.Cmd, bool) {
	switch msg := msg.(type) {
	case marketLoadedMsg:
		m.market.busy = false
		m.market.loaded = true
		if msg.err != nil {
			// Unprefixed: the client writes messages meant to be read, and
			// "marketplace unreachable: the marketplace is not answering"
			// says it twice.
			m.market.notice = msg.err.Error()
			return m, nil, true
		}
		m.market.games = msg.games
		// The catalog is the same answer a sync fetches, so reading it here
		// keeps the library's update markers current for a player who browsed
		// rather than one who waited for a sync.
		m.latest = latestVersions(msg.games)
		if m.market.idx >= len(msg.games) {
			m.market.idx = 0
		}
		return m, nil, true
	case marketOpMsg:
		m.market.busy = false
		if msg.err != nil {
			// Whichever screen kicked the operation off shows its outcome.
			m.market.notice = msg.err.Error()
			m.notice = msg.err.Error()
			return m, nil, true
		}
		m.games = m.mp.Reload()
		if m.menuIdx >= m.indexRows(m.recentGames()) {
			m.menuIdx = 0
		}
		if m.libIdx >= len(m.games) {
			m.libIdx = 0
		}
		m.market.notice = msg.verb + " " + msg.id
		m.notice = msg.verb + " " + msg.id
		return m, nil, true
	case syncedMsg:
		if len(msg.latest) > 0 {
			m.latest = msg.latest
		}
		// Silence is the common case and is left alone: overwriting a notice
		// the player is reading with nothing would be worse than saying
		// nothing.
		if msg.notice != "" {
			m.notice = msg.notice
			m.market.notice = msg.notice
		}
		return m, nil, true
	}
	return m, nil, false
}

func (m Model) openMarket() (tea.Model, tea.Cmd) {
	m.screen = screenMarket
	m.market.notice = ""
	if !m.market.loaded {
		m.market.busy = true
		return m, m.loadMarket()
	}
	return m, nil
}

func (m Model) updateMarketKey(key string) (tea.Model, tea.Cmd) {
	if m.market.busy {
		return m, nil
	}
	switch key {
	case "esc", "q":
		m.screen = screenMenu
		return m, nil
	case "up", "k":
		if n := len(m.market.games); n > 0 {
			m.market.idx = (m.market.idx + n - 1) % n
		}
	case "down", "j":
		if n := len(m.market.games); n > 0 {
			m.market.idx = (m.market.idx + 1) % n
		}
	case "r":
		m.market.busy = true
		m.market.loaded = false
		return m, m.loadMarket()
	case "l":
		if _, ok := m.mp.Account(); ok {
			if err := m.mp.SignOut(); err != nil {
				m.market.notice = err.Error()
			} else {
				m.market.notice = "signed out"
			}
			return m, nil
		}
		m.auth = authState{}
		m.screen = screenAuth
		return m, nil
	case "enter":
		if len(m.market.games) == 0 {
			return m, nil
		}
		game := m.market.games[m.market.idx]
		if m.installedIDs()[game.ID] {
			m.market.notice = game.ID + " is already in your arcade"
			return m, nil
		}
		if !game.HasPackage {
			m.market.notice = game.ID + " has no downloadable package yet"
			return m, nil
		}
		m.market.busy = true
		m.market.notice = "adding " + game.ID + "…"
		return m, m.installCmd(game.ID)
	case "x":
		if len(m.market.games) == 0 {
			return m, nil
		}
		game := m.market.games[m.market.idx]
		if !m.installedIDs()[game.ID] {
			return m, nil
		}
		m.market.busy = true
		m.market.notice = "removing " + game.ID + "…"
		return m, m.removeCmd(game.ID)
	}
	return m, nil
}

func (m Model) updateAuthKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	switch key {
	case "esc", "q", "enter":
		m.screen = screenMarket
		return m, nil
	}
	return m, nil
}

// --------------------------------------------------------------- rendering --

var (
	marketInstalled = lipgloss.NewStyle().Foreground(lipgloss.Color("#4fc964"))
	marketNotice    = lipgloss.NewStyle().Foreground(lipgloss.Color("#e6c945"))
	inputStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("#f2f2f2"))
	inputFocused    = lipgloss.NewStyle().Foreground(lipgloss.Color("#e6c945")).Bold(true)
)

func (m Model) viewMarket() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("MARKETPLACE"))
	if email, ok := m.mp.Account(); ok {
		b.WriteString(dimStyle.Render("   " + email))
	} else {
		b.WriteString(dimStyle.Render("   browsing as guest"))
	}
	b.WriteString("\n\n")

	switch {
	case m.market.busy && !m.market.loaded:
		b.WriteString(dimStyle.Render("loading…"))
	case len(m.market.games) == 0 && m.market.loaded:
		b.WriteString(dimStyle.Render("nothing published yet"))
	default:
		installed := m.installedIDs()
		for i, g := range m.market.games {
			line := "  " + g.Name + dimStyle.Render("  "+g.ID+" · v"+g.Version)
			if installed[g.ID] {
				line += marketInstalled.Render("  ● in your arcade")
			} else if !g.HasPackage {
				line += dimStyle.Render("  · no package yet")
			}
			if i == m.market.idx {
				b.WriteString(selectedStyle.Render("▸" + line[1:]))
			} else {
				b.WriteString(normalStyle.Render(line))
			}
			b.WriteByte('\n')
		}
		if m.market.idx < len(m.market.games) {
			b.WriteString("\n" + dimStyle.Render(sanitize(m.market.games[m.market.idx].Description)))
		}
	}

	b.WriteString("\n\n")
	if m.market.notice != "" {
		b.WriteString(marketNotice.Render(sanitize(m.market.notice)) + "\n")
	}
	_, signedIn := m.mp.Account()
	auth := "l login"
	if signedIn {
		auth = "l logout"
	}
	b.WriteString(dimStyle.Render("enter add · x remove · r refresh · " + auth + " · esc back"))
	return b.String()
}

func (m Model) viewAuth() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("SECURE SIGN IN"))
	b.WriteString("\n\nTermcade never asks for account credentials in the terminal.\n")
	b.WriteString("Exit the arcade and run " + inputFocused.Render("termcade login") + ".\n")
	b.WriteString("It will show a one-time code for " + inputStyle.Render("https://app.termca.de/pair") + ".")
	b.WriteString("\n\n" + dimStyle.Render("enter/esc back"))
	return b.String()
}
