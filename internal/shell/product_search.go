package shell

import (
	"context"
	"strings"
	"time"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"github.com/aviorstudio/termcade/internal/registry"
	"github.com/charmbracelet/x/ansi"
)

const productSearchDebounce = 300 * time.Millisecond

type productSearchState struct {
	gen             uint64
	cancel          context.CancelFunc
	active, loading bool
	games           []registry.Game
	err             string
}
type productSearchTick struct{ Gen uint64 }
type productSearchMsg struct {
	Gen   uint64
	Games []registry.Game
	Err   error
}

func (m Model) productSearchPage() bool {
	return m.app.loc.Page == "marketplace"
}

func (m *Model) syncSearchFocus() {
	m.app.searchFocus = false
	if m.app.focus != 1 || !m.productSearchPage() {
		return
	}
	items := m.productItems()
	if len(items) > 0 && m.app.selection < len(items) && items[m.app.selection].Kind == "search" {
		m.app.searchFocus = true
		m.app.searchCaret = min(m.app.searchCaret, len([]rune(m.productQuery())))
	}
}
func (m Model) productQuery() string {
	return m.app.marketQuery
}
func matchesProductSearch(game registry.Game, query string) bool {
	query = strings.ToLower(strings.TrimSpace(query))
	_, slug, ok := strings.Cut(game.ID, "/")
	if !ok {
		slug = game.ID
	}
	for _, field := range []string{game.Name, slug, game.Description} {
		if strings.Contains(strings.ToLower(field), query) {
			return true
		}
	}
	return false
}
func (m *Model) cancelProductSearch() {
	if m.app.search.cancel != nil {
		m.app.search.cancel()
	}
	m.app.search = productSearchState{gen: m.app.search.gen + 1}
}
func (m *Model) startProductSearch(delay time.Duration) tea.Cmd {
	m.cancelProductSearch()
	m.app.search.active = true
	m.app.search.loading = true
	if len(strings.TrimSpace(m.app.marketQuery)) > 64 {
		m.app.search.loading = false
		m.app.search.err = "Marketplace search must be 64 UTF-8 bytes or fewer."
		return nil
	}
	if delay == 0 {
		return m.fetchProductSearch()
	}
	gen := m.app.search.gen
	return tea.Tick(delay, func(time.Time) tea.Msg { return productSearchTick{gen} })
}
func (m *Model) fetchProductSearch() tea.Cmd {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	m.app.search.cancel = cancel
	gen, query, service := m.app.search.gen, strings.TrimSpace(m.app.marketQuery), m.mp.Product
	return func() tea.Msg {
		defer cancel()
		reply, err := service.Request(ctx, ProductRequest{Kind: "catalog-search", Value: query})
		return productSearchMsg{gen, reply.Catalog, err}
	}
}
func (m *Model) setProductQuery(value string) tea.Cmd {
	old := m.productQuery()
	m.app.marketQuery = value
	m.app.selection, m.app.action, m.app.scroll = 0, 0, 0
	m.app.manualScroll = false
	if m.app.loc.Page == "marketplace" && strings.TrimSpace(old) != strings.TrimSpace(value) {
		return m.startProductSearch(productSearchDebounce)
	}
	return nil
}
func (m *Model) insertProductSearch(text string) tea.Cmd {
	text = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, text)
	runes := []rune(m.productQuery())
	i := min(m.app.searchCaret, len(runes))
	m.app.searchCaret = i + len([]rune(text))
	return m.setProductQuery(string(runes[:i]) + text + string(runes[i:]))
}
func (m Model) productSearchKey(msg tea.KeyPressMsg) (Model, tea.Cmd, bool) {
	runes := []rune(m.productQuery())
	i := min(m.app.searchCaret, len(runes))
	var cmd tea.Cmd
	switch msg.String() {
	case "tab", "shift+tab", "up", "down", "esc", "pgup", "pgdown":
		return m, nil, false
	case "enter":
		items := m.productItems()
		if len(items) > 1 {
			m.app.selection = 1
			m.syncSearchFocus()
		}
		return m, nil, true
	case "ctrl+u":
		m.app.searchCaret = 0
		cmd = m.setProductQuery("")
	case "left":
		m.app.searchCaret = max(0, i-1)
	case "right":
		m.app.searchCaret = min(len(runes), i+1)
	case "home":
		m.app.searchCaret = 0
	case "end":
		m.app.searchCaret = len(runes)
	case "backspace":
		if i > 0 {
			m.app.searchCaret = i - 1
			cmd = m.setProductQuery(string(runes[:i-1]) + string(runes[i:]))
		}
	case "delete":
		if i < len(runes) {
			cmd = m.setProductQuery(string(runes[:i]) + string(runes[i+1:]))
		}
	default:
		if msg.Text != "" && msg.Mod&(tea.ModCtrl|tea.ModAlt|tea.ModSuper) == 0 {
			cmd = m.insertProductSearch(msg.Text)
		}
	}
	return m, cmd, true
}
func (m Model) productSearchView(width int) string {
	query := m.productQuery()
	prefix := "  "
	if m.app.searchFocus {
		prefix = "▸ "
		runes := []rune(query)
		i := min(m.app.searchCaret, len(runes))
		label := prefix + "Search: "
		// Keep the caret visible without letting a long query wrap the footer away.
		keep := max(1, width-ansi.StringWidth(label)-1)
		left := ansi.TruncateLeft(sanitize(string(runes[:i])), max(0, ansi.StringWidth(sanitize(string(runes[:i])))-keep), "")
		return selectedStyle.Render(ansi.Truncate(label+left+"│"+sanitize(string(runes[i:])), width, ""))
	}
	if query == "" {
		query = "Search…"
	}
	return dimStyle.Render(ansi.Truncate(prefix+"Search: "+sanitize(query), width, "…"))
}
