package shell

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/aviorstudio/termcade/internal/registry"
	"github.com/charmbracelet/x/ansi"
)

// Pairing is a modal interaction, not another destination in page history.
// The underlying route and paused game remain in place throughout the poll.
type productSignInDialog struct {
	button, focus, selection, action, scroll      int
	manualScroll, searchFocus, wasLoading, saving bool
	search                                        productSearchState
}

func (m *Model) closeSignInDialog(signedIn bool) tea.Cmd {
	dialog := m.app.signIn
	if dialog == nil {
		return nil
	}
	m.cancelProduct()
	m.app.signIn = nil
	m.app.round = registry.DeviceRound{}
	m.app.pairingURL = ""
	m.app.notice = ""
	m.app.focus, m.app.selection, m.app.action = dialog.focus, dialog.selection, dialog.action
	m.app.scroll, m.app.manualScroll, m.app.searchFocus = dialog.scroll, dialog.manualScroll, dialog.searchFocus
	// Keep completed public search results, but never restore a canceled context
	// or accept its generation. Restart any search that was still in flight.
	gen := m.app.search.gen
	m.app.search = dialog.search
	m.app.search.gen, m.app.search.cancel = gen, nil
	m.app.search.loading = false
	m.app.selection = clampIndex(m.app.selection, len(m.productItems()))
	if (dialog.wasLoading || dialog.saving) && !signedIn {
		// Credential persistence may already have committed before Escape. Load
		// actual state rather than promising that cancellation undid sign-in.
		return m.productRequest(ProductRequest{Kind: "load", Extra: "refresh-page"})
	}
	if dialog.search.loading && m.app.loc.Page == "marketplace" {
		return m.startProductSearch(0)
	}
	return nil
}

func (m Model) signInDialogActions() []productAction {
	return []productAction{
		{Label: "Open browser", Kind: "open-url", ID: m.app.pairingURL, Disabled: m.app.pairingURL == ""},
		{Label: "Cancel", Kind: "back"},
	}
}

func (m Model) signInDialogKey(msg tea.KeyPressMsg) (Model, tea.Cmd, bool) {
	if msg.String() == "esc" {
		cmd := m.closeSignInDialog(false)
		return m, cmd, true
	}
	if productNavToggle(msg) {
		return m, nil, true
	}
	// At sizes where the controls cannot be displayed, only dismissal/quit is
	// accepted. A hidden focused button must never launch an external browser.
	if m.termW < 40 || m.termH < 20 {
		return m, nil, true
	}
	dialog := *m.app.signIn
	m.app.signIn = &dialog
	actions := m.signInDialogActions()
	switch msg.String() {
	case "tab", "right", "down":
		dialog.button = (dialog.button + 1) % len(actions)
	case "shift+tab", "left", "up":
		dialog.button = (dialog.button + len(actions) - 1) % len(actions)
	case "enter":
		action := actions[clampIndex(dialog.button, len(actions))]
		if !action.Disabled {
			next, cmd := m.activateProduct(action.Kind, action.ID)
			return next, cmd, true
		}
	}
	return m, nil, true
}

func (m Model) signInDialogView() string {
	if m.termW < 40 || m.termH < 20 {
		return boundedBlock("Sign in\nResize to at least 40×20\nEsc cancel · Ctrl+C quit", m.termW, m.termH)
	}
	background := m
	background.app.signIn = nil
	background.app.loading, background.app.mutating = false, false
	background.app.notice = ""
	background.app.search = m.app.signIn.search
	background.app.focus, background.app.selection, background.app.action = m.app.signIn.focus, m.app.signIn.selection, m.app.signIn.action
	background.app.scroll, background.app.manualScroll = m.app.signIn.scroll, m.app.signIn.manualScroll
	base := strings.Split(background.productView(), "\n")
	width, height := min(72, m.termW-2), min(20, m.termH-2)
	innerWidth, innerHeight := width-4, height-2
	status := m.app.notice
	var buttons []string
	for i, action := range m.signInDialogActions() {
		label := "[" + action.Label + "]"
		if action.Disabled {
			label = dimStyle.Render(label)
		} else if i == m.app.signIn.button {
			label = selectedStyle.Render(label)
		}
		buttons = append(buttons, label)
	}
	joined := strings.Join(buttons, " ")
	options := lipgloss.NewStyle().Width(innerWidth).AlignHorizontal(lipgloss.Center).Render(joined)
	if ansi.StringWidth(ansi.Strip(joined)) <= innerWidth {
		options = lipgloss.PlaceHorizontal(innerWidth, lipgloss.Center, joined)
	}
	title := titleStyle.Render("Sign in")
	footer := productFooter(innerWidth)
	if status != "" {
		footer = lipgloss.NewStyle().Width(innerWidth).MaxWidth(innerWidth).Render(marketNotice.Render(sanitize(status))) + "\n" + footer
	}
	bodyHeight := max(1, innerHeight-lipgloss.Height(title)-1-lipgloss.Height(footer))
	body := lipgloss.Place(innerWidth, bodyHeight, lipgloss.Center, lipgloss.Center, signInPairingBody(m.app.round.UserCode, m.app.pairingURL, options, innerWidth))
	content := title + "\n" + body
	panel := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Padding(0, 1).Render(productWithFooter(content, footer, innerWidth, innerHeight))
	panelLines := strings.Split(boundedBlock(panel, width, height), "\n")
	x, y := (m.termW-width)/2, (m.termH-height)/2
	for i := range base {
		left, right := base[i], ""
		if i >= y && i < y+height {
			left, right = ansi.Cut(base[i], 0, x), ansi.Cut(base[i], x+width, m.termW)
			base[i] = lipgloss.NewStyle().Faint(true).Render(left) + panelLines[i-y] + lipgloss.NewStyle().Faint(true).Render(right)
		} else {
			base[i] = lipgloss.NewStyle().Faint(true).Render(left)
		}
	}
	return boundedBlock(strings.Join(base, "\n"), m.termW, m.termH)
}

func signInPairingBody(code, url, options string, width int) string {
	code, url = sanitize(code), sanitize(url)
	center := func(line string) string {
		return lipgloss.PlaceHorizontal(width, lipgloss.Center, line)
	}
	var lines []string
	if code == "" && url == "" {
		lines = append(lines, center("Requesting a pairing code…"))
	}
	if url != "" {
		visible := ansi.Truncate(url, width, "…")
		lines = append(lines, center("\x1b]8;;"+url+"\x1b\\"+visible+"\x1b]8;;\x1b\\"))
	}
	if code != "" {
		if url != "" {
			lines = append(lines, "")
		}
		lines = append(lines, center(ansi.Truncate(code, width, "…")))
	}
	if options != "" {
		lines = append(lines, "", options)
	}
	return strings.Join(lines, "\n")
}
