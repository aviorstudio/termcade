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
	SignIn  func(email, password string) error
	SignUp  func(email, password string) error
	SignOut func() error
	// Reload re-discovers installed games after an install/remove.
	Reload func() []engine.Registration
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

type authDoneMsg struct{ err error }

type marketState struct {
	games  []MarketGame
	loaded bool
	idx    int
	notice string
	busy   bool
}

const (
	authStageChoose = iota // sign in vs create account
	authStageForm
)

type authState struct {
	stage     int
	chooseIdx int
	signup    bool
	focus     int // 0 email, 1 password
	email     string
	password  string
	err       string
	busy      bool
}

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

func (m Model) authCmd(signup bool, email, password string) tea.Cmd {
	mp := m.mp
	return func() tea.Msg {
		if signup {
			return authDoneMsg{err: mp.SignUp(email, password)}
		}
		return authDoneMsg{err: mp.SignIn(email, password)}
	}
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
			m.market.notice = "marketplace unreachable: " + msg.err.Error()
			return m, nil, true
		}
		m.market.games = msg.games
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
	case authDoneMsg:
		m.auth.busy = false
		if msg.err != nil {
			m.auth.err = msg.err.Error()
			return m, nil, true
		}
		m.screen = screenMarket
		m.market.notice = "signed in"
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
	if m.auth.busy {
		return m, nil
	}
	key := msg.String()

	if m.auth.stage == authStageChoose {
		switch key {
		case "esc":
			m.screen = screenMarket
			return m, nil
		case "up", "k", "down", "j":
			m.auth.chooseIdx = 1 - m.auth.chooseIdx
		case "enter":
			m.auth.signup = m.auth.chooseIdx == 1
			m.auth.stage = authStageForm
			m.auth.focus = 0
			m.auth.err = ""
		}
		return m, nil
	}

	switch key {
	case "esc":
		m.auth.stage = authStageChoose
		m.auth.err = ""
		return m, nil
	case "tab", "down":
		m.auth.focus = (m.auth.focus + 1) % 2
		return m, nil
	case "shift+tab", "up":
		m.auth.focus = (m.auth.focus + 1) % 2
		return m, nil
	case "enter":
		if m.auth.email == "" || m.auth.password == "" {
			m.auth.err = "email and password are required"
			return m, nil
		}
		m.auth.busy = true
		m.auth.err = ""
		return m, m.authCmd(m.auth.signup, m.auth.email, m.auth.password)
	case "backspace":
		field := m.authField()
		if *field != "" {
			*field = (*field)[:len(*field)-1]
		}
		return m, nil
	}

	// Printable input lands in the focused field. Key.Text is empty for
	// non-text keys, which filters modifiers and function keys for free.
	if text := msg.Text; text != "" && !strings.ContainsAny(text, "\n\r\t") {
		*m.authField() += text
	}
	return m, nil
}

func (m *Model) authField() *string {
	if m.auth.focus == 0 {
		return &m.auth.email
	}
	return &m.auth.password
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

	if m.auth.stage == authStageChoose {
		b.WriteString(titleStyle.Render("ACCOUNT"))
		b.WriteString("\n\n")
		for i, label := range []string{"sign in", "create account"} {
			if i == m.auth.chooseIdx {
				b.WriteString(overlaySel.Render("▸ " + label))
			} else {
				b.WriteString(overlayDim.Render("  " + label))
			}
			b.WriteByte('\n')
		}
		b.WriteString("\n" + dimStyle.Render("↑/↓ select · enter continue · esc back"))
		return b.String()
	}

	title := "SIGN IN"
	if m.auth.signup {
		title = "CREATE ACCOUNT"
	}
	b.WriteString(titleStyle.Render(title))
	b.WriteString("\n\n")

	fields := []struct {
		label, value string
		mask         bool
	}{
		{"email   ", m.auth.email, false},
		{"password", m.auth.password, true},
	}
	for i, f := range fields {
		value := f.value
		if f.mask {
			value = strings.Repeat("•", len(value))
		}
		style := inputStyle
		cursor := " "
		if i == m.auth.focus {
			style = inputFocused
			cursor = "▏"
		}
		b.WriteString(dimStyle.Render(f.label+" ") + style.Render(value+cursor) + "\n")
	}

	if m.auth.busy {
		b.WriteString("\n" + dimStyle.Render("talking to the marketplace…"))
	} else if m.auth.err != "" {
		b.WriteString("\n" + marketNotice.Render(sanitize(m.auth.err)))
	}
	b.WriteString("\n\n" + dimStyle.Render("tab next field · enter submit · esc back"))
	return b.String()
}
