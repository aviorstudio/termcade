package shell

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/aviorstudio/termcade/internal/registry"
	"github.com/aviorstudio/termcade/sdk"
	"github.com/charmbracelet/x/ansi"
)

const sidebarWidth = 22
const sidebarBreakpoint = 120
const productContentMax = 100

type productLocation struct{ Page, ID string }
type productItem struct{ Label, Detail, Meta, Kind, ID string }
type productAction struct {
	Label, Kind, ID string
	Disabled        bool
}
type productForm struct {
	Carets          []int
	Title, Kind, ID string
	Labels, Values  []string
	Index           int
	Confirmation    string
}
type productState struct {
	signIn                        *productSignInDialog
	marketQuery                   string
	searchFocus                   bool
	searchCaret                   int
	search                        productSearchState
	accountGen                    uint64
	loc                           productLocation
	history                       []productLocation
	snapshot                      ProductSnapshot
	game                          registry.Game
	owner                         registry.HandleOwner
	days                          []registry.PlayDay
	members                       []registry.Member
	credentials                   []registry.CLICredential
	focus, selection, action, nav int // navigation / content / actions
	scroll                        int
	manualScroll                  bool
	loading                       bool
	mutating                      bool
	gen                           uint64
	ctx                           context.Context
	cancel                        context.CancelFunc
	form                          *productForm
	round                         registry.DeviceRound
	pairingURL                    string
	notice                        string
	gameNav                       bool
}
type productMsg struct {
	Gen     uint64
	Request ProductRequest
	Reply   ProductReply
	Err     error
}
type productSyncMsg struct {
	AccountGen uint64
	Notice     string
}

func (m Model) productEnabled() bool { return m.mp != nil && m.mp.Product != nil }

func (m *Model) cancelProduct() {
	m.cancelProductSearch()
	if m.app.cancel != nil {
		m.app.cancel()
	}
	m.app.gen++
	m.app.loading = false
	m.app.mutating = false
}

func (m *Model) productRequest(req ProductRequest) tea.Cmd {
	m.cancelProduct()
	m.app.notice = ""
	timeout := 45 * time.Second
	if req.Kind == "pair-wait" {
		timeout = m.app.round.ExpiresIn + time.Second
	}
	m.app.ctx, m.app.cancel = context.WithTimeout(context.Background(), timeout)
	m.app.loading = true
	m.app.mutating = req.Kind != "load" && req.Kind != "game" && req.Kind != "owner" && req.Kind != "members" && req.Kind != "sessions"
	if req.Extra == "refresh-page" {
		m.app.mutating = true
	}
	gen, ctx, services := m.app.gen, m.app.ctx, m.mp.Product
	return func() tea.Msg { reply, err := services.Request(ctx, req); return productMsg{gen, req, reply, err} }
}

func (m Model) productInit() tea.Cmd {
	services, ctx, gen := m.mp.Product, m.app.ctx, m.app.gen
	return func() tea.Msg {
		req := ProductRequest{Kind: "load"}
		reply, err := services.Request(ctx, req)
		return productMsg{gen, req, reply, err}
	}
}

func (m Model) updateProduct(msg tea.Msg) (Model, tea.Cmd, bool) {
	switch msg := msg.(type) {
	case productSearchTick:
		if msg.Gen != m.app.search.gen || m.app.loc.Page != "marketplace" {
			return m, nil, true
		}
		cmd := m.fetchProductSearch()
		return m, cmd, true
	case productSearchMsg:
		if msg.Gen != m.app.search.gen || m.app.loc.Page != "marketplace" {
			return m, nil, true
		}
		m.app.search.loading = false
		if msg.Err != nil {
			m.app.search.err = sanitize(msg.Err.Error())
		} else {
			m.app.search.games = msg.Games
		}
		m.app.selection = 0
		m.app.action = 0
		return m, nil, true
	case productOpenMsg:
		if msg.Gen == m.app.gen && msg.Err != nil {
			m.app.notice = sanitize(msg.Err.Error())
		}
		return m, nil, true
	case productSyncMsg:
		if msg.AccountGen == m.app.accountGen && m.app.notice == "" {
			m.app.notice = sanitize(msg.Notice)
		}
		return m, nil, true
	case productMsg:
		if msg.Gen != m.app.gen {
			return m, nil, true
		}
		selected := ""
		selectedKind := ""
		navigation := m.productNavItems()
		navSelection := navigation[clampIndex(m.app.nav, len(navigation))]
		items := m.productItems()
		if m.app.selection < len(items) {
			selected = items[m.app.selection].ID
			selectedKind = items[m.app.selection].Kind
		}
		m.app.loading = false
		m.app.mutating = false
		if msg.Err != nil {
			if !errors.Is(msg.Err, context.Canceled) {
				m.app.notice = sanitize(msg.Err.Error())
			}
			if msg.Request.Kind == "game" && m.localIndex(msg.Request.ID) >= 0 {
				m.app.notice = "Using installed package metadata · " + sanitize(msg.Err.Error())
			}
			if errors.Is(msg.Err, registry.ErrLoginRequired) {
				if m.app.snapshot.SignedIn {
					m.app.accountGen++
				}
				m.app.snapshot.SignedIn = false
				m.app.snapshot.LibraryKnown = false
				m.app.snapshot.Library = nil
				m.clearPrivateProductViews()
			}
			return m, nil, true
		}
		if msg.Reply.Snapshot != nil {
			before := m.app.snapshot
			m.app.snapshot = *msg.Reply.Snapshot
			if before.SignedIn != m.app.snapshot.SignedIn || before.Account.Email != m.app.snapshot.Account.Email || before.CredentialID != m.app.snapshot.CredentialID {
				m.app.accountGen++
			}
			if !m.app.snapshot.SignedIn || before.Account.Email != m.app.snapshot.Account.Email || before.CredentialID != m.app.snapshot.CredentialID {
				m.clearPrivateProductViews()
			}
			m.games = m.app.snapshot.Installed
			m.latest = map[string]string{}
			for _, g := range m.app.snapshot.Catalog {
				m.latest[g.ID] = g.Version
			}
		}
		if msg.Reply.Game != nil {
			m.app.game = *msg.Reply.Game
			m.app.days = msg.Reply.Days
		}
		if msg.Reply.Owner != nil {
			m.app.owner = *msg.Reply.Owner
			m.app.days = msg.Reply.Days
		}
		if msg.Request.Kind == "members" {
			m.app.members = msg.Reply.Members
		}
		if msg.Request.Kind == "sessions" {
			m.app.credentials = msg.Reply.Credentials
		}
		m.app.notice = sanitize(msg.Reply.Notice)
		switch msg.Request.Kind {
		case "pair-start":
			m.app.round = msg.Reply.Round
			m.app.pairingURL = registry.PairingURLWithCode(msg.Reply.PairingURL, msg.Reply.Round.UserCode)
			cmd := m.productRequest(ProductRequest{Kind: "pair-wait", Round: msg.Reply.Round})
			return m, cmd, true
		case "pair-wait":
			if m.app.signIn != nil {
				dialog := *m.app.signIn
				dialog.saving = true
				m.app.signIn = &dialog
			}
			cmd := m.productRequest(ProductRequest{Kind: "save-login", Session: msg.Reply.Session})
			return m, cmd, true
		case "save-login":
			if m.app.signIn != nil {
				cmd := m.closeSignInDialog(true)
				return m, cmd, true
			}
			m.app.round = registry.DeviceRound{}
			m.app.pairingURL = ""
			m.app.form = nil
			m.app.loc = productLocation{Page: "settings"}
			m.app.focus = 1
			m.app.selection = 0
			if n := len(m.app.history); n > 0 && m.app.history[n-1] == m.app.loc {
				m.app.history = m.app.history[:n-1]
			}
		case "install":
			for i, g := range m.games {
				if g.Info.ID == msg.Request.ID {
					if m.app.loc.Page != "game" || m.app.loc.ID != msg.Request.ID {
						m.app.history = append(m.app.history, m.app.loc)
						m.app.loc = productLocation{Page: "game", ID: msg.Request.ID}
						m.app.game = m.metadata(msg.Request.ID)
					}
					next, cmd := m.startGame(i)
					return next.(Model), cmd, true
				}
			}
			m.app.notice = "The installed package could not be discovered; refresh Library"
		case "logout", "account-delete":
			m.app.form = nil
			m.app.history = nil
			m.app.loc = productLocation{Page: "marketplace"}
			m.app.selection = 0
			m.app.focus = 1
		default:
			switch msg.Request.Kind {
			case "load", "game", "owner", "members", "sessions", "username-check":
			default:
				m.app.form = nil
			}
			if !m.app.snapshot.SignedIn && m.app.loc.Page == "sessions" {
				m.app.loc = productLocation{Page: "settings"}
			}
			if msg.Request.Kind == "member-set" || msg.Request.Kind == "member-remove" {
				cmd := m.productRequest(ProductRequest{Kind: "members", ID: m.app.loc.ID})
				return m, cmd, true
			}
			if msg.Request.Kind == "session-revoke" && m.app.snapshot.SignedIn {
				cmd := m.productRequest(ProductRequest{Kind: "sessions"})
				return m, cmd, true
			}
			if msg.Request.Kind == "org-delete" {
				m.app.loc = productLocation{Page: "settings"}
				m.app.selection = 0
				m.app.focus = 1
			}
		}
		items = m.productItems()
		for i, item := range items {
			if item.Kind == selectedKind && item.ID == selected {
				m.app.selection = i
				break
			}
		}
		m.app.selection = clampIndex(m.app.selection, len(items))
		if m.productSearchPage() && strings.TrimSpace(m.productQuery()) == "" && !m.app.searchFocus {
			if len(items) > 1 && items[m.app.selection].Kind == "search" {
				m.app.selection = 1
			}
		}
		m.syncSearchFocus()
		for i, item := range m.productNavItems() {
			if item.Kind == navSelection.Kind && item.ID == navSelection.ID {
				m.app.nav = i
				break
			}
		}
		m.app.nav = clampIndex(m.app.nav, len(m.productNavItems()))
		m.app.action = 0
		if msg.Request.Kind == "load" && msg.Request.Extra == "refresh-page" {
			switch m.app.loc.Page {
			case "game", "owner", "members", "sessions", "chart":
				next, cmd := m.openProduct(m.app.loc, false)
				return next, cmd, true
			}
		}
		if msg.Reply.Snapshot != nil && m.app.loc.Page == "marketplace" && strings.TrimSpace(m.app.marketQuery) != "" && !m.app.search.active {
			cmd := m.startProductSearch(0)
			return m, cmd, true
		}
		if msg.Request.Kind == "game" && m.app.loc.Page == "game" && m.screen == screenProduct {
			next, cmd := m.activateProduct("play", m.app.loc.ID)
			return next, cmd, true
		}
		return m, nil, true
	case tea.PasteMsg:
		if m.app.signIn != nil {
			return m, nil, true
		}
		if m.app.searchFocus && m.screen == screenProduct && m.app.form == nil {
			cmd := m.insertProductSearch(msg.Content)
			return m, cmd, true
		}
		if m.app.form != nil && m.app.form.Index < len(m.app.form.Values) && !m.app.loading {
			m.appendFormText(msg.Content)
		}
		return m, nil, true
	case tea.KeyPressMsg:
		key := msg.String()
		if m.screen != screenPlaying && msg.IsRepeat && (key == "enter" || key == "tab" || key == "shift+tab" || productNavToggle(msg) || key == "esc") {
			return m, nil, true
		}
		if key == "ctrl+c" {
			m.cancelProduct()
			m = m.closeProductGame()
			m.scores.Save()
			return m, tea.Quit, true
		}
		if m.app.signIn != nil {
			return m.signInDialogKey(msg)
		}
		if m.screen == screenPlaying {
			return m, nil, false
		}
		if m.screen == screenPaused || m.screen == screenGameOver || m.screen == screenCrashed {
			if productNavToggle(msg) {
				m.app.gameNav = !m.app.gameNav
				return m, nil, true
			}
			if !productNavToggle(msg) && (key == "tab" || key == "shift+tab") {
				if m.app.gameNav {
					navKey := "down"
					if key == "shift+tab" {
						navKey = "up"
					}
					return m.productNavKey(navKey)
				}
				if m.screen == screenPaused {
					return m.productPauseKey(key)
				}
				return m, nil, true
			}
			if m.app.gameNav {
				if key == "esc" {
					m.app.gameNav = false
					return m, nil, true
				}
				return m.productNavKey(key)
			}
			if m.screen == screenPaused {
				return m.productPauseKey(key)
			}
			return m, nil, false
		}
		if productNavToggle(msg) {
			m.app.searchFocus = false
			if m.app.focus == 0 {
				m.app.focus = 1
			} else {
				m.app.focus = 0
			}
			return m, nil, true
		}
		if m.app.form != nil {
			return m.productFormKey(msg)
		}
		if m.app.searchFocus {
			next, cmd, handled := m.productSearchKey(msg)
			if handled {
				return next, cmd, true
			}
			m = next
		}
		if key == "/" && m.productSearchPage() && !m.app.mutating {
			m.app.focus = 1
			m.app.selection = 0
			m.syncSearchFocus()
			m.app.searchCaret = len([]rune(m.productQuery()))
			return m, nil, true
		}
		if key == "esc" {
			if m.app.focus == 0 && m.termW < sidebarBreakpoint {
				m.app.focus = 1
				return m, nil, true
			}
			m.cancelProduct()
			if n := len(m.app.history); n > 0 {
				loc := m.app.history[n-1]
				m.app.history = m.app.history[:n-1]
				next, cmd := m.openProduct(loc, false)
				return next, cmd, true
			}
			m.app.loc = productLocation{Page: "marketplace"}
			m.app.focus = 1
			if strings.TrimSpace(m.app.marketQuery) != "" {
				cmd := m.startProductSearch(0)
				return m, cmd, true
			}
			return m, nil, true
		}
		if key == "tab" || key == "shift+tab" {
			if m.app.focus == 0 {
				navKey := "down"
				if key == "shift+tab" {
					navKey = "up"
				}
				return m.productNavKey(navKey)
			}
			if m.app.focus == 2 {
				actions := m.productActions()
				if len(actions) > 0 {
					delta := 1
					if key == "shift+tab" {
						delta = len(actions) - 1
					}
					m.app.action = (m.app.action + delta) % len(actions)
				}
				return m, nil, true
			}
			items := m.productItems()
			if len(items) > 0 {
				delta := 1
				if key == "shift+tab" {
					delta = len(items) - 1
				}
				m.app.selection = (m.app.selection + delta) % len(items)
				m.app.manualScroll = false
				m.app.action = 0
				m.syncSearchFocus()
			}
			return m, nil, true
		}
		if m.app.focus == 0 {
			return m.productNavKey(key)
		}
		if next, cmd, ok := m.productActionHotkey(key); ok {
			return next, cmd, true
		}
		if m.app.focus == 2 {
			actions := m.productActions()
			if len(actions) == 0 {
				return m, nil, true
			}
			switch key {
			case "left", "up":
				m.app.action = (m.app.action + len(actions) - 1) % len(actions)
			case "right", "down":
				m.app.action = (m.app.action + 1) % len(actions)
			case "enter":
				a := actions[clampIndex(m.app.action, len(actions))]
				if !a.Disabled {
					next, cmd := m.activateProduct(a.Kind, a.ID)
					return next, cmd, true
				}
			}
			return m, nil, true
		}
		items := m.productItems()
		switch key {
		case "up", "down":
			if len(items) > 0 {
				delta := 1
				if key == "up" {
					delta = len(items) - 1
				}
				m.app.selection = (m.app.selection + delta) % len(items)
				m.app.manualScroll = false
				m.app.action = 0
				m.syncSearchFocus()
			}
		case "pgdown", "pgup":
			if m.app.loc.Page != "docs" && m.app.loc.Page != "game" {
				delta := max(1, (m.termH-6)/3)
				if key == "pgup" {
					delta = -delta
				}
				m.app.selection = clampIndex(m.app.selection+delta, len(items))
				m.app.manualScroll = false
				m.app.action = 0
				m.syncSearchFocus()
				return m, nil, true
			}
			d := max(1, m.termH/2)
			if key == "pgup" {
				d = -d
			}
			m.app.scroll = max(0, m.app.scroll+d)
			m.app.manualScroll = true
		case "enter":
			if len(items) > 0 {
				item := items[clampIndex(m.app.selection, len(items))]
				next, cmd := m.activateProduct(item.Kind, item.ID)
				return next, cmd, true
			}
		}
		return m, nil, true
	}
	return m, nil, false
}

func productNavToggle(msg tea.KeyPressMsg) bool {
	if msg.Mod&tea.ModCtrl == 0 {
		return false
	}
	return msg.Code == 'n' || msg.Code == 'N' || msg.String() == "ctrl+n"
}

func clampIndex(i, n int) int {
	if n == 0 {
		return 0
	}
	return min(max(i, 0), n-1)
}

func (m Model) productNavItems() []productItem {
	items := []productItem{{Label: "Marketplace", Kind: "page", ID: "marketplace"}, {Label: "Docs", Kind: "page", ID: "docs"}, {Label: "Library"}}
	games := m.libraryGames()
	if len(games) == 0 {
		label := "  No games added"
		if !m.app.snapshot.SignedIn {
			label = "  login to access your library"
		}
		items = append(items, productItem{Label: label})
	}
	for _, g := range games {
		items = append(items, productItem{Label: "  " + g.Name, Kind: "game", ID: g.ID})
	}
	label := "Sign in"
	if m.app.snapshot.SignedIn {
		label = "Account"
		if m.app.snapshot.Account.Username != "" {
			label = "@" + m.app.snapshot.Account.Username
		}
	}
	if !m.app.snapshot.SignedIn {
		return append(items, productItem{Label: label, Kind: "sign-in"})
	}
	return append(items, productItem{Label: label, Kind: "page", ID: "settings"})
}

func (m Model) productNavKey(key string) (Model, tea.Cmd, bool) {
	items := m.productNavItems()
	switch key {
	case "up":
		m.app.nav = (m.app.nav + len(items) - 1) % len(items)
	case "down":
		m.app.nav = (m.app.nav + 1) % len(items)
	case "enter":
		i := items[clampIndex(m.app.nav, len(items))]
		if i.Kind == "" {
			return m, nil, true
		}
		if i.Kind == "sign-in" {
			next, cmd := m.activateProduct(i.Kind, i.ID)
			return next, cmd, true
		}
		m = m.closeProductGame()
		m.app.gameNav = false
		next, cmd := m.activateProduct(i.Kind, i.ID)
		return next, cmd, true
	}
	return m, nil, true
}

func (m Model) openProduct(loc productLocation, remember bool) (Model, tea.Cmd) {
	if loc.Page == "library" {
		loc.Page = "marketplace"
		loc.ID = ""
	}
	// Explicit route changes dismiss the dialog and invalidate any device poll.
	m.app.signIn = nil
	if remember && loc != m.app.loc && m.app.loc.Page != "pairing" {
		m.app.history = append(m.app.history, m.app.loc)
	}
	m.cancelProduct()
	if m.app.loc.Page == "pairing" {
		m.app.round = registry.DeviceRound{}
		m.app.pairingURL = ""
	}
	m.screen = screenProduct
	m.app.loc = loc
	m.app.focus = 1
	m.app.selection = 0
	m.app.action = 0
	m.app.scroll = 0
	m.app.manualScroll = false
	m.app.form = nil
	m.app.searchFocus = false
	if loc.Page == "marketplace" {
		m.app.selection = 1
	}
	m.app.notice = ""
	m.app.days = nil
	var req ProductRequest
	switch loc.Page {
	case "game":
		m.app.game = m.metadata(loc.ID)
		req = ProductRequest{Kind: "game", ID: loc.ID}
		cmd := m.productRequest(req)
		if i := m.localIndex(loc.ID); i >= 0 && m.games[i].Err == nil {
			next, play := m.startGame(i)
			return next.(Model), tea.Batch(cmd, play)
		}
		return m, cmd
	case "owner":
		m.app.owner = registry.HandleOwner{Name: loc.ID}
		req = ProductRequest{Kind: "owner", ID: loc.ID}
	case "members":
		m.app.members = nil
		req = ProductRequest{Kind: "members", ID: loc.ID}
	case "sessions":
		m.app.credentials = nil
		req = ProductRequest{Kind: "sessions"}
	case "chart":
		kind := "owner"
		if strings.Contains(loc.ID, "/") {
			kind = "game"
		}
		req = ProductRequest{Kind: kind, ID: loc.ID}
	case "docs":
		return m, nil
	default:
		req = ProductRequest{Kind: "load"}
	}
	cmd := m.productRequest(req)
	return m, cmd
}

func (m Model) metadata(id string) registry.Game {
	for _, collection := range [][]registry.Game{m.app.search.games, m.app.snapshot.Catalog, m.app.snapshot.Library, m.app.owner.Games} {
		for _, g := range collection {
			if g.ID == id {
				return g
			}
		}
	}
	for _, g := range m.games {
		if g.Info.ID == id {
			return registry.Game{ID: id, Name: g.Info.Title, Version: g.Version, Width: g.Info.PixelW, Height: g.Info.PixelH, ABI: sdk.ABIVersion, HasPackage: g.Err == nil}
		}
	}
	return registry.Game{ID: id, Name: id}
}

func (m Model) localIndex(id string) int {
	for i, g := range m.games {
		if g.Info.ID == id {
			return i
		}
	}
	return -1
}
func (m Model) inAccount(id string) bool {
	if !m.app.snapshot.SignedIn || !m.app.snapshot.LibraryKnown {
		return false
	}
	for _, g := range m.app.snapshot.Library {
		if g.ID == id {
			return true
		}
	}
	return false
}
func (m Model) gameBadge(id string) string {
	local := m.localIndex(id)
	owned := m.inAccount(id)
	if local >= 0 {
		if m.games[local].Err != nil {
			return "Broken install"
		}
		if owned {
			return "Installed"
		}
		if m.app.snapshot.SignedIn && !m.app.snapshot.LibraryKnown {
			return "Installed · membership unknown"
		}
		if !m.app.snapshot.SignedIn {
			return "Installed · on this machine"
		}
		return "Installed · Local only"
	}
	if owned {
		return "Not installed"
	}
	return "Not in Library"
}

func (m Model) installationStatus(id string) string {
	status := m.gameBadge(id)
	if i := m.localIndex(id); i >= 0 && m.games[i].Version != "" {
		status += " · local v" + m.games[i].Version
	}
	return status
}

func (m Model) libraryGames() []registry.Game {
	byID := map[string]registry.Game{}
	if m.app.snapshot.SignedIn {
		for _, g := range m.app.snapshot.Library {
			byID[g.ID] = g
		}
	}
	for _, g := range m.games {
		if _, ok := byID[g.Info.ID]; !ok {
			byID[g.Info.ID] = m.metadata(g.Info.ID)
		}
	}
	ids := make([]string, 0, len(byID))
	for id := range byID {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := make([]registry.Game, 0, len(ids))
	for _, id := range ids {
		out = append(out, byID[id])
	}
	return out
}

func productPlayedAgo(when, now time.Time) string {
	if when.IsZero() {
		return "never"
	}
	seconds := max(0, math.Round(now.Sub(when).Seconds()))
	if seconds < 90 {
		return "just now"
	}
	minutes := math.Round(seconds / 60)
	if minutes < 60 {
		return fmt.Sprintf("%.0f minutes ago", minutes)
	}
	hours := math.Round(minutes / 60)
	if hours < 24 {
		if hours == 1 {
			return "an hour ago"
		}
		return fmt.Sprintf("%.0f hours ago", hours)
	}
	days := math.Round(hours / 24)
	if days < 30 {
		if days == 1 {
			return "yesterday"
		}
		return fmt.Sprintf("%.0f days ago", days)
	}
	months := math.Round(days / 30)
	if months == 1 {
		return "a month ago"
	}
	return fmt.Sprintf("%.0f months ago", months)
}

func likeMeta(g registry.Game) string {
	mark := "♡"
	if g.Liked {
		mark = "♥"
	}
	return fmt.Sprintf("%s %d", mark, g.Likes)
}

func productLabelLine(prefix, label, meta string, width int) string {
	label = sanitize(label)
	if meta == "" {
		return prefix + label
	}
	meta = sanitize(meta)
	space := width - lipgloss.Width(prefix+label) - lipgloss.Width(meta)
	if space < 1 {
		space = 1
	}
	return prefix + label + strings.Repeat(" ", space) + meta
}

func (m Model) productItems() []productItem {
	var items []productItem
	gameRows := func(games []registry.Game) {
		for _, g := range games {
			items = append(items, productItem{Label: g.ID, Detail: g.Description, Meta: likeMeta(g), Kind: "game", ID: g.ID})
		}
	}
	switch m.app.loc.Page {
	case "marketplace":
		items = append(items, productItem{Label: "Search", Kind: "search"})
		if m.app.search.active {
			gameRows(m.app.search.games)
		} else if strings.TrimSpace(m.app.marketQuery) == "" {
			gameRows(m.app.snapshot.Catalog)
		}
	case "game":
		// Rendered as one centered block in productPageView. Owner, repository,
		// and activity stay available as footer actions.
	case "owner":
		o := m.app.owner
		kind := "developer"
		if o.IsOrg {
			kind = "org"
		}
		items = append(items, productItem{Label: "@" + o.Name + " · " + kind, Detail: o.Bio}, productItem{Label: "Website", Detail: o.Link, Kind: "open-url", ID: o.Link}, productItem{Label: "Public play activity", Detail: playSummary(m.app.days), Kind: "chart", ID: o.Name})
		gameRows(o.Games)
	case "chart":
		for _, d := range m.app.days {
			items = append(items, productItem{Label: fmt.Sprintf("%s  %s  %d plays", d.Day, playIntensity(d.Count, m.app.days), d.Count)})
		}
	case "settings":
		if !m.app.snapshot.SignedIn {
			items = append(items, productItem{Label: "Sign in", Detail: "Approve this terminal in your browser", Kind: "sign-in"})
		} else {
			a := m.app.snapshot.Account
			items = append(items, productItem{Label: a.Email}, productItem{Label: "Handle: @" + a.Username, Detail: "Check availability, claim, or rename", Kind: "form-username"}, productItem{Label: "Create an org", Kind: "form-org"})
			for _, o := range a.Orgs {
				role := "member"
				if o.Admin {
					role = "admin"
				}
				items = append(items, productItem{Label: "@" + o.Username + " · " + role, Detail: o.Bio, Kind: "members", ID: o.Username})
			}
			items = append(items, productItem{Label: "CLI sessions", Detail: "View and revoke login sessions (not publish keys)", Kind: "page", ID: "sessions"}, productItem{Label: "Sign out", Detail: "Revoke this device, then clear locally", Kind: "confirm-logout"}, productItem{Label: "Delete account", Detail: "Subject to published-game and org-admin safeguards", Kind: "confirm-account"})
		}
		items = append(items, productItem{Label: "Pixel style: " + m.shape.Name, Detail: "Change for the next game start", Kind: "pixels"})
	case "members":
		for _, member := range m.app.members {
			role := "member"
			if member.Admin {
				role = "admin"
			}
			items = append(items, productItem{Label: member.Email + " · " + role, Kind: "form-member", ID: member.Email})
		}
	case "sessions":
		for _, c := range m.app.credentials {
			state := "active"
			if c.Revoked {
				state = "revoked"
			}
			if c.ID == m.app.snapshot.CredentialID {
				state += " · this device"
			}
			items = append(items, productItem{Label: c.DeviceName + " · " + state, Detail: "expires " + c.ExpiresAt + " · last used " + c.LastUsedAt + " · created " + c.CreatedAt, Kind: "confirm-session", ID: c.ID})
		}
	case "pairing":
		items = append(items, productItem{Label: "Secure browser approval", Detail: "Never enter an account password or email verification code here."}, productItem{Label: m.app.pairingURL, Kind: "open-url", ID: m.app.pairingURL}, productItem{Label: "Pairing code: " + m.app.round.UserCode, Detail: "Waiting for approval. No browser is opened automatically."})
	case "docs":
		items = append(items, productItem{Label: "TERMCADE", Detail: "One arcade in your terminal and browser."}, productItem{Label: "Library", Detail: "Games you add appear in the navigation. Play installs a missing package. Local installations remain playable offline."}, productItem{Label: "Two removal actions", Detail: "Remove from Library changes your account. Uninstall here removes only this machine's copy."}, productItem{Label: "Build a game", Detail: "termcade dev new author/slug\ntermcade dev build\ntermcade dev install game.tcade"}, productItem{Label: "Publish", Detail: "termcade publish <github-repo> <tag> [asset]"}, productItem{Label: "Keyboard", Detail: "Ctrl+N toggles navigation. Tab is next and Shift+Tab is previous in lists, actions, forms, and dialogs. Enter activates; Esc goes back/pauses; Ctrl+C quits. PgUp/PgDown scrolls long text."}, productItem{Label: "Gameplay", Detail: "Arrows/WASD to move · Space/Z action A · X action B · Enter start. Pause retains keyboard navigation and pixel selection."})
	}
	return items
}

func playSummary(days []registry.PlayDay) string {
	n := 0
	for _, d := range days {
		n += d.Count
	}
	return fmt.Sprintf("%d plays · %d UTC days this year", n, len(days)) + "\n" + playHeatmap(days)
}

func playHeatmap(days []registry.PlayDay) string {
	return formatPlayHeatmap(days, 54)
}

func playChart(days []registry.PlayDay, maxWeeks int) string {
	total := 0
	for _, day := range days {
		total += day.Count
	}
	var legend strings.Builder
	legend.WriteString(dimStyle.Render("Less "))
	for i, style := range playSquare {
		if i > 0 {
			legend.WriteByte(' ')
		}
		legend.WriteString(style.Render("■"))
	}
	legend.WriteString(dimStyle.Render(" More"))
	plays := dimStyle.Render("PLAYS ") + normalStyle.Render(fmt.Sprintf("%d", total))
	grid := formatPlayHeatmap(days, maxWeeks)
	width := max(lipgloss.Width(grid), lipgloss.Width(plays)+1+lipgloss.Width(legend.String()))
	pad := max(1, width-lipgloss.Width(plays)-lipgloss.Width(legend.String()))
	return plays + strings.Repeat(" ", pad) + legend.String() + "\n" + grid
}

func formatPlayHeatmap(days []registry.PlayDay, maxWeeks int) string {
	if len(days) == 0 {
		return "Activity unavailable or no daily data"
	}
	start, err := time.Parse("2006-01-02", days[0].Day)
	if err != nil {
		return "Use daily view to inspect activity"
	}
	byDate := map[string]int{}
	last := start
	peak := 0
	for _, day := range days {
		d, e := time.Parse("2006-01-02", day.Day)
		if e != nil {
			continue
		}
		byDate[day.Day] = day.Count
		peak = max(peak, day.Count)
		if d.Before(start) {
			start = d
		}
		if d.After(last) {
			last = d
		}
	}
	yearStart := time.Date(last.Year(), 1, 1, 0, 0, 0, 0, time.UTC)
	start = yearStart.AddDate(0, 0, -int(yearStart.Weekday()))
	weeks := min(54, int(last.Sub(start).Hours()/24)/7+1)
	if maxWeeks > 0 {
		weeks = min(weeks, maxWeeks)
	}
	latest := last.AddDate(0, 0, -int(last.Weekday())).AddDate(0, 0, -(weeks-1)*7)
	if latest.After(start) {
		start = latest
	}
	var lines []string
	for row := 0; row < 7; row++ {
		var cells []string
		for week := 0; week < weeks; week++ {
			date := start.AddDate(0, 0, week*7+row)
			key := date.Format("2006-01-02")
			if date.Before(yearStart) || date.After(last) {
				cells = append(cells, " ")
				continue
			}
			cells = append(cells, playSquare[playLevel(byDate[key], peak)].Render("■"))
		}
		lines = append(lines, strings.Join(cells, " "))
	}
	return strings.Join(lines, "\n")
}

var playSquare = []lipgloss.Style{
	lipgloss.NewStyle().Foreground(lipgloss.Color("#3a3a3a")),
	lipgloss.NewStyle().Foreground(lipgloss.Color("#5c4e1c")),
	lipgloss.NewStyle().Foreground(lipgloss.Color("#8a7429")),
	lipgloss.NewStyle().Foreground(lipgloss.Color("#c4a73a")),
	lipgloss.NewStyle().Foreground(lipgloss.Color("#e6c945")),
}

func playLevel(count, peak int) int {
	if count <= 0 || peak <= 0 {
		return 0
	}
	if count*4 <= peak {
		return 1
	}
	if count*2 <= peak {
		return 2
	}
	if count*4 <= peak*3 {
		return 3
	}
	return 4
}

func playIntensity(count int, days []registry.PlayDay) string {
	peak := 0
	for _, d := range days {
		peak = max(peak, d.Count)
	}
	return playSquare[playLevel(count, peak)].Render("■")
}

func (m Model) productActions() []productAction {
	a := []productAction{{Label: "Back", Kind: "back"}, {Label: "Refresh", Kind: "refresh"}}
	if m.productSearchPage() && m.productQuery() != "" {
		a = append(a, productAction{Label: "Clear search", Kind: "clear-search", Disabled: m.app.mutating})
	}
	if m.app.loc.Page == "pairing" {
		return []productAction{{Label: "Open browser", Kind: "open-url", ID: m.app.pairingURL, Disabled: m.app.pairingURL == ""}, {Label: "New code", Kind: "pair-retry", Disabled: m.app.loading}, {Label: "Cancel", Kind: "back"}}
	}
	if m.app.loc.Page == "members" {
		a = append(a, productAction{Label: "View org", Kind: "owner", ID: m.app.loc.ID})
		if m.orgAdmin(m.app.loc.ID) {
			a = append(a, productAction{Label: "Add member", Kind: "form-member"}, productAction{Label: "Remove member", Kind: "confirm-member"}, productAction{Label: "Dissolve org", Kind: "confirm-org"})
		}
	}
	id := ""
	if m.app.loc.Page == "game" {
		id = m.app.game.ID
	} else if m.app.loc.Page == "marketplace" || m.app.loc.Page == "owner" {
		items := m.productItems()
		if len(items) > 0 {
			selected := items[clampIndex(m.app.selection, len(items))]
			if selected.Kind == "game" || selected.Kind == "play" {
				id = selected.ID
			}
		}
	}
	if id != "" {
		g := m.metadata(id)
		if m.app.loc.Page == "game" && m.app.game.ID == id {
			g = m.app.game
		}
		local := m.localIndex(id)
		canPlay := local >= 0 && m.games[local].Err == nil
		canDownload := local < 0 && m.inAccount(id) && g.HasPackage && g.ABI == sdk.ABIVersion
		a = append(a, productAction{Label: "Play", Kind: "play", ID: id, Disabled: !canPlay && !canDownload})
		if m.inAccount(id) {
			a = append(a, productAction{Label: "Remove from Library", Kind: "remove", ID: id})
		} else {
			a = append(a, productAction{Label: "Add", Kind: "add", ID: id, Disabled: !m.app.snapshot.SignedIn || !m.app.snapshot.LibraryKnown})
		}
		if g.Liked {
			a = append(a, productAction{Label: "Unlike", Kind: "unlike", ID: id, Disabled: !m.app.snapshot.SignedIn})
		} else {
			a = append(a, productAction{Label: "Like", Kind: "like", ID: id, Disabled: !m.app.snapshot.SignedIn})
		}
		if local >= 0 {
			a = append(a, productAction{Label: "Uninstall here", Kind: "confirm-uninstall", ID: id})
		}
		if m.app.loc.Page == "game" {
			if owner, _, ok := strings.Cut(id, "/"); ok && owner != "" {
				a = append(a, productAction{Label: "Owner", Kind: "owner", ID: owner})
			}
			if g.Repo != "" {
				a = append(a, productAction{Label: "Repository", Kind: "open-url", ID: g.Repo})
			}
			a = append(a, productAction{Label: "Activity", Kind: "chart", ID: id})
		}
	}
	for i := range a {
		if m.app.loading && a[i].Kind != "back" && a[i].Kind != "open-url" && (a[i].Kind != "play" || m.localIndex(a[i].ID) < 0 || m.app.mutating) {
			a[i].Disabled = true
		}
	}
	return a
}

func (a productAction) keyHint() string {
	switch a.Kind {
	case "back":
		return "✕ Esc Back"
	case "refresh":
		return "↻ R Refresh"
	case "play":
		return "▶ P Play"
	case "add":
		return "+ A Add"
	case "like":
		return "♡ L Like"
	case "unlike":
		return "♥ L Unlike"
	case "confirm-uninstall":
		return "⌫ U Uninstall"
	case "owner":
		return "@ O Owner"
	case "chart":
		return "▦ C Activity"
	case "open-url":
		return "↗ B Open"
	default:
		return ""
	}
}

func (m Model) productActionHotkey(key string) (Model, tea.Cmd, bool) {
	want := ""
	switch strings.ToLower(key) {
	case "r":
		want = "refresh"
	case "p":
		want = "play"
	case "a":
		want = "add"
	case "l":
		want = "like"
	case "u":
		want = "confirm-uninstall"
	case "o":
		want = "owner"
	case "c":
		want = "chart"
	case "b":
		want = "open-url"
	default:
		return m, nil, false
	}
	for _, a := range m.productActions() {
		match := a.Kind == want || (want == "like" && a.Kind == "unlike")
		if match {
			if a.Disabled {
				return m, nil, true
			}
			next, cmd := m.activateProduct(a.Kind, a.ID)
			return next, cmd, true
		}
	}
	return m, nil, false
}

func (m Model) productActionLegend() string {
	var parts []string
	for _, a := range m.productActions() {
		if a.Disabled {
			continue
		}
		if hint := a.keyHint(); hint != "" {
			parts = append(parts, hint)
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return dimStyle.Render(strings.Join(parts, " · "))
}

func (m Model) orgAdmin(name string) bool {
	if !m.app.snapshot.SignedIn {
		return false
	}
	for _, o := range m.app.snapshot.Account.Orgs {
		if o.Username == name {
			return o.Admin
		}
	}
	return false
}

func (m *Model) clearPrivateProductViews() {
	m.app.members = nil
	m.app.credentials = nil
	m.app.form = nil
	if !m.app.snapshot.SignedIn {
		m.app.snapshot.Activity = nil
		m.app.snapshot.Account = registry.Me{}
		m.app.snapshot.CredentialID = ""
	}
	if !m.app.snapshot.SignedIn && (m.app.loc.Page == "members" || m.app.loc.Page == "sessions") {
		m.app.loc = productLocation{Page: "settings"}
		m.app.selection = 0
		m.app.focus = 1
	}
}

func (m Model) activateProduct(kind, id string) (Model, tea.Cmd) {
	if m.app.loading && (strings.HasPrefix(kind, "form-") || strings.HasPrefix(kind, "confirm-")) {
		m.app.notice = "Wait for the current request, or Esc to cancel it"
		return m, nil
	}
	switch kind {
	case "search":
		return m, nil
	case "clear-search":
		m.app.searchCaret = 0
		cmd := m.setProductQuery("")
		return m, cmd
	case "page":
		return m.openProduct(productLocation{Page: id}, true)
	case "game", "owner", "members":
		return m.openProduct(productLocation{Page: kind, ID: id}, true)
	case "chart":
		m.app.history = append(m.app.history, m.app.loc)
		m.app.loc = productLocation{Page: "chart", ID: id}
		m.app.selection = 0
		m.app.manualScroll = false
		return m, nil
	case "back":
		if m.app.signIn != nil {
			cmd := m.closeSignInDialog(false)
			return m, cmd
		}
		m.cancelProduct()
		if n := len(m.app.history); n > 0 {
			loc := m.app.history[n-1]
			m.app.history = m.app.history[:n-1]
			return m.openProduct(loc, false)
		}
		return m.openProduct(productLocation{Page: "marketplace"}, false)
	case "refresh":
		return m.openProduct(m.app.loc, false)
	case "pixels":
		next, cmd := m.cyclePixels()
		m = next.(Model)
		m.app.notice = m.notice
		return m, cmd
	case "play":
		local := m.localIndex(id)
		canPlay := local >= 0 && m.games[local].Err == nil
		g := m.metadata(id)
		canInstall := local < 0 && m.inAccount(id) && g.HasPackage && g.ABI == sdk.ABIVersion
		if !canPlay && !canInstall {
			if local >= 0 {
				m.app.notice = "Broken package: uninstall or replace it explicitly"
			} else {
				m.app.notice = "Add a compatible published game to your Library first"
			}
			return m, nil
		}
		if m.app.loc.Page != "game" || m.app.loc.ID != id {
			m.app.history = append(m.app.history, m.app.loc)
			m.app.loc = productLocation{Page: "game", ID: id}
			m.app.game = g
		}
		if canPlay {
			m.cancelProduct()
			next, cmd := m.startGame(local)
			return next.(Model), cmd
		}
		cmd := m.productRequest(ProductRequest{Kind: "install", ID: id})
		return m, cmd
	case "add", "remove", "like", "unlike":
		cmd := m.productRequest(ProductRequest{Kind: kind, ID: id})
		return m, cmd
	case "open-url":
		if id == "" {
			return m, nil
		}
		// Opening a browser must not cancel the active device poll.
		service, gen := m.mp.Product, m.app.gen
		return m, func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			reply, err := service.Request(ctx, ProductRequest{Kind: "open-url", ID: id})
			return productOpenMsg{gen, reply.Notice, err}
		}
	case "sign-in":
		if m.app.signIn != nil {
			return m, nil
		}
		m.app.signIn = &productSignInDialog{
			focus: m.app.focus, selection: m.app.selection, action: m.app.action,
			scroll: m.app.scroll, manualScroll: m.app.manualScroll,
			searchFocus: m.app.searchFocus, search: m.app.search, wasLoading: m.app.loading,
		}
		m.app.round = registry.DeviceRound{}
		m.app.pairingURL = ""
		cmd := m.productRequest(ProductRequest{Kind: "pair-start"})
		return m, cmd
	case "pair-retry":
		m.app.round = registry.DeviceRound{}
		m.app.pairingURL = ""
		m.app.notice = ""
		cmd := m.productRequest(ProductRequest{Kind: "pair-start"})
		return m, cmd
	case "form-username":
		m.app.form = &productForm{Title: "Handle availability / claim / rename", Kind: "username", Labels: []string{"Handle"}, Values: []string{m.app.snapshot.Account.Username}}
	case "form-org":
		m.app.form = &productForm{Title: "Create org", Kind: "org-create", Labels: []string{"Handle", "Bio (optional)", "Website (optional)"}, Values: []string{"", "", ""}}
	case "form-member":
		if !m.orgAdmin(m.app.loc.ID) {
			m.app.notice = "Only org admins can change membership"
			return m, nil
		}
		role := "member"
		for _, member := range m.app.members {
			if member.Email == id && member.Admin {
				role = "admin"
			}
		}
		m.app.form = &productForm{Title: "Add member / change role", Kind: "member-set", ID: m.app.loc.ID, Labels: []string{"Existing account email", "Role: member or admin"}, Values: []string{id, role}}
	case "confirm-uninstall":
		m.confirmProduct("Uninstall local copy", "uninstall", id, id)
	case "confirm-logout":
		m.confirmProduct("Revoke this device and sign out", "logout", "", "sign out")
	case "confirm-account":
		m.confirmProduct("Delete account", "account-delete", "", m.app.snapshot.Account.Email)
	case "confirm-org":
		if m.orgAdmin(m.app.loc.ID) {
			m.confirmProduct("Dissolve org", "org-delete", m.app.loc.ID, m.app.loc.ID)
		}
	case "confirm-member":
		items := m.productItems()
		if len(items) > 0 && m.orgAdmin(m.app.loc.ID) {
			email := items[clampIndex(m.app.selection, len(items))].ID
			m.confirmProduct("Remove member", "member-remove", m.app.loc.ID, email)
		}
	case "confirm-session":
		name := id
		for _, credential := range m.app.credentials {
			if credential.ID == id {
				name = credential.DeviceName + " (" + credential.ID + ")"
				if credential.Revoked {
					m.app.notice = "This session is already revoked"
					return m, nil
				}
			}
		}
		m.confirmProduct("Revoke "+sanitize(name), "session-revoke", id, "revoke")
	}
	return m, nil
}

type productOpenMsg struct {
	Gen    uint64
	Notice string
	Err    error
}

func (m *Model) confirmProduct(title, kind, id, confirmation string) {
	m.app.form = &productForm{Title: title, Kind: kind, ID: id, Labels: []string{"Type " + confirmation + " to confirm"}, Values: []string{""}, Confirmation: confirmation}
}
func (m *Model) appendFormText(text string) {
	f := *m.app.form
	f.Values = append([]string(nil), f.Values...)
	f.Carets = append([]int(nil), f.Carets...)
	initializeCarets(&f)
	r := []rune(f.Values[f.Index])
	at := f.Carets[f.Index]
	insert := []rune(sanitize(text))
	value := string(r[:at]) + string(insert) + string(r[at:])
	if utf8.RuneCountInString(value) <= 2048 {
		f.Values[f.Index] = value
		f.Carets[f.Index] = at + len(insert)
		m.app.form = &f
	}
}

func initializeCarets(f *productForm) {
	if len(f.Carets) != len(f.Values) {
		f.Carets = make([]int, len(f.Values))
		for i, value := range f.Values {
			f.Carets[i] = utf8.RuneCountInString(value)
		}
	}
}

func (m Model) productFormKey(msg tea.KeyPressMsg) (Model, tea.Cmd, bool) {
	key := msg.String()
	f := *m.app.form
	f.Values = append([]string(nil), f.Values...)
	f.Carets = append([]int(nil), f.Carets...)
	initializeCarets(&f)
	m.app.form = &f
	if key == "esc" {
		refresh := m.app.loading && m.app.mutating
		m.cancelProduct()
		m.app.form = nil
		if refresh {
			cmd := m.productRequest(ProductRequest{Kind: "load", Extra: "refresh-page"})
			return m, cmd, true
		}
		return m, nil, true
	}
	if m.app.loading {
		return m, nil, true
	}
	buttons := 2
	if f.Kind == "username" {
		buttons = 3
	}
	if key == "tab" || key == "shift+tab" {
		delta := 1
		if key == "shift+tab" {
			delta = len(f.Values) + buttons - 1
		}
		m.app.form.Index = (f.Index + delta) % (len(f.Values) + buttons)
		return m, nil, true
	}
	if f.Index < len(f.Values) {
		r := []rune(f.Values[f.Index])
		at := f.Carets[f.Index]
		switch key {
		case "left":
			f.Carets[f.Index] = max(0, at-1)
		case "right":
			f.Carets[f.Index] = min(len(r), at+1)
		case "home":
			f.Carets[f.Index] = 0
		case "end":
			f.Carets[f.Index] = len(r)
		case "ctrl+u":
			f.Values[f.Index] = ""
			f.Carets[f.Index] = 0
		case "backspace":
			if at > 0 {
				f.Values[f.Index] = string(r[:at-1]) + string(r[at:])
				f.Carets[f.Index]--
			}
		case "delete":
			if at < len(r) {
				f.Values[f.Index] = string(r[:at]) + string(r[at+1:])
			}
		default:
			if msg.Text != "" {
				m.appendFormText(msg.Text)
			}
		}
		return m, nil, true
	}
	if key != "enter" {
		return m, nil, true
	}
	button := f.Index - len(f.Values)
	if button == buttons-1 {
		m.app.form = nil
		return m, nil, true
	}
	if f.Confirmation != "" && f.Values[0] != f.Confirmation {
		m.app.notice = "Confirmation does not match"
		return m, nil, true
	}
	req := ProductRequest{Kind: f.Kind, ID: f.ID, Value: strings.TrimSpace(f.Values[0])}
	switch f.Kind {
	case "username":
		if req.Value == "" {
			m.app.notice = "Handle is required"
			return m, nil, true
		}
		if button == 1 {
			req.Kind = "username-check"
		}
	case "org-create":
		req.ID = req.Value
		req.Value = strings.TrimSpace(f.Values[1])
		req.Extra = strings.TrimSpace(f.Values[2])
		if req.ID == "" {
			m.app.notice = "Org handle is required"
			return m, nil, true
		}
	case "member-set":
		role := strings.TrimSpace(f.Values[1])
		if role != "admin" && role != "member" {
			m.app.notice = "Role must be admin or member"
			return m, nil, true
		}
		req.Admin = role == "admin"
	case "member-remove":
		req.Value = f.Confirmation
	}
	cmd := m.productRequest(req)
	return m, cmd, true
}

func (m Model) closeProductGame() Model {
	if m.game != nil {
		if m.screen == screenPlaying || m.screen == screenPaused {
			m.scores.Record(m.game.Info().ID, m.game.Score(), m.gameVersion, false)
		}
		m.game.Close()
		m.game = nil
		m.tickGen++
	}
	m.screen = screenProduct
	return m
}

func (m Model) productPauseKey(key string) (Model, tea.Cmd, bool) {
	switch strings.ToLower(key) {
	case "tab", "right", "down":
		m.pauseIdx = 1
	case "shift+tab", "left", "up":
		m.pauseIdx = 0
	case "esc":
		next, cmd := m.resumeGame()
		return next.(Model), cmd, true
	case "enter":
		if m.pauseIdx == 0 {
			next, cmd := m.resumeGame()
			return next.(Model), cmd, true
		}
		m = m.closeProductGame()
		next, cmd := m.openProduct(productLocation{Page: "marketplace"}, false)
		return next, tea.Batch(cmd, saveScores(m.scores), m.syncCmd()), true
	}
	return m, nil, true
}

func (m Model) pauseDialogView() string {
	if m.termW < 1 || m.termH < 1 {
		return ""
	}
	background := m
	background.screen = screenPlaying
	base := strings.Split(background.productView(), "\n")
	width, height := min(40, max(24, m.termW-4)), min(10, max(8, m.termH-4))
	innerWidth, innerHeight := max(1, width-4), max(1, height-2)
	resume, exit := "[Resume]", "[Exit]"
	if m.pauseIdx == 0 {
		resume = selectedStyle.Render(resume)
	} else {
		exit = selectedStyle.Render(exit)
	}
	options := lipgloss.PlaceHorizontal(innerWidth, lipgloss.Center, resume+"  "+exit)
	title := titleStyle.Render("Paused")
	footer := productFooter(innerWidth)
	body := lipgloss.Place(innerWidth, max(1, innerHeight-lipgloss.Height(title)-1-lipgloss.Height(footer)), lipgloss.Center, lipgloss.Center, options)
	panel := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Padding(0, 1).Render(productWithFooter(title+"\n"+body, footer, innerWidth, innerHeight))
	panelLines := strings.Split(boundedBlock(panel, width, height), "\n")
	x, y := (m.termW-width)/2, (m.termH-height)/2
	for i := range base {
		left, right := base[i], ""
		if i >= y && i < y+height && i-y < len(panelLines) {
			left, right = ansi.Cut(base[i], 0, x), ansi.Cut(base[i], x+width, m.termW)
			base[i] = lipgloss.NewStyle().Faint(true).Render(left) + panelLines[i-y] + lipgloss.NewStyle().Faint(true).Render(right)
		} else {
			base[i] = lipgloss.NewStyle().Faint(true).Render(left)
		}
	}
	return boundedBlock(strings.Join(base, "\n"), m.termW, m.termH)
}

// boundedBlock constrains both axes in display cells, including ANSI styling.
func boundedBlock(s string, w, h int) string {
	if w <= 0 || h <= 0 {
		return ""
	}
	return lipgloss.NewStyle().Width(w).MaxWidth(w).Height(h).MaxHeight(h).Render(s)
}

// Every product surface reserves its footer before laying out content. This
// keeps the divider and controls anchored at the bottom instead of clipping
// them when a form, navigation list, or game uses the available height.
func productFooter(width int, blocks ...string) string {
	if width <= 0 {
		return ""
	}
	var controls []string
	for _, block := range blocks {
		if block != "" {
			controls = append(controls, block)
		}
	}
	divider := lipgloss.NewStyle().Foreground(lipgloss.Color("#3a3a3a")).Render(strings.Repeat("─", width))
	if len(controls) == 0 {
		return divider
	}
	return divider + "\n" + lipgloss.NewStyle().Width(width).MaxWidth(width).Render(strings.Join(controls, "\n"))
}

func productWithFooter(content, footer string, width, height int) string {
	if width <= 0 || height <= 0 {
		return ""
	}
	bodyHeight := height - lipgloss.Height(footer)
	if bodyHeight <= 0 {
		return boundedBlock(footer, width, height)
	}
	return boundedBlock(content, width, bodyHeight) + "\n" + footer
}

func (m Model) productView() string {
	if m.termW < 1 || m.termH < 1 {
		return ""
	}
	if m.app.signIn != nil {
		return m.signInDialogView()
	}
	if m.screen == screenPaused {
		return m.pauseDialogView()
	}
	gameRoute := m.screen != screenProduct
	navOpen := (!gameRoute && m.app.focus == 0) || m.app.gameNav
	// Closed navigation takes no space. An open rail pushes on wide terminals
	// and overlays otherwise, keeping narrow content at its original width.
	pushRail := navOpen && m.termW >= sidebarBreakpoint
	if gameRoute && m.game != nil && m.termW-sidebarWidth < m.game.Info().PixelW+2 {
		pushRail = false
	}
	width := m.termW
	if pushRail {
		width -= sidebarWidth
	}
	contentWidth := min(width, productContentMax)
	if gameRoute {
		contentWidth = width
	}
	var body string
	if gameRoute && m.app.loc.Page == "game" {
		body = m.productGamePagePlayView(width, m.termH)
	} else if gameRoute {
		body = m.productGameView(width, m.termH)
	} else if m.app.form != nil {
		body = m.productFormView(contentWidth, m.termH)
	} else {
		body = m.productPageView(contentWidth, m.termH)
	}
	body = lipgloss.PlaceHorizontal(width, lipgloss.Center, boundedBlock(body, contentWidth, m.termH))
	if navOpen && !pushRail {
		columns := min(sidebarWidth, m.termW)
		background := strings.Split(boundedBlock(body, m.termW, m.termH), "\n")
		cut := productFooterRow(background)
		navH := max(1, cut)
		nav := strings.Split(m.productNavView(columns, navH), "\n")
		for i := 0; i < cut && i < len(background); i++ {
			left := ""
			if i < len(nav) {
				left = nav[i]
			}
			background[i] = boundedBlock(left, columns-1, 1) + dimStyle.Render("│") + lipgloss.NewStyle().Faint(true).Render(ansi.Cut(background[i], columns, m.termW))
		}
		body = strings.Join(background[:cut], "\n")
	} else if pushRail {
		bodyLines := strings.Split(boundedBlock(body, width, m.termH), "\n")
		cut := productFooterRow(bodyLines)
		navH := max(1, cut)
		nav := strings.Split(m.productNavView(sidebarWidth, navH), "\n")
		var stacked []string
		for i := 0; i < cut; i++ {
			left := ""
			if i < len(nav) {
				left = nav[i]
			}
			stacked = append(stacked, boundedBlock(left, sidebarWidth-1, 1)+dimStyle.Render("│")+bodyLines[i])
		}
		body = strings.Join(stacked, "\n")
	}
	return m.withTerminalFooter(body)
}

func (m Model) withTerminalFooter(body string) string {
	footer := m.productChromeFooter(m.termW)
	topH := max(0, m.termH-lipgloss.Height(footer))
	return productWithFooter(boundedBlock(body, m.termW, topH), footer, m.termW, m.termH)
}

func productFooterRow(lines []string) int {
	row := len(lines)
	for i, line := range lines {
		stripped := strings.TrimSpace(ansi.Strip(line))
		if stripped == "" {
			continue
		}
		if strings.Trim(stripped, "─") == "" && strings.Count(stripped, "─") >= 8 {
			row = i
		}
	}
	return row
}

func (m Model) productNavView(width, height int) string {
	items := m.productNavItems()
	contentWidth := max(0, width-1)
	renderItem := func(i int) string {
		line := "  " + sanitize(items[i].Label)
		active := items[i].Kind == "page" && items[i].ID == m.app.loc.Page
		if items[i].ID == "settings" && (m.app.loc.Page == "members" || m.app.loc.Page == "sessions" || m.app.loc.Page == "pairing") {
			active = true
		}
		if active {
			line = titleStyle.Render(line)
		}
		if i == m.app.nav && ((m.screen == screenProduct && m.app.focus == 0) || m.app.gameNav) {
			line = selectedStyle.Render("▸ " + sanitize(items[i].Label))
		}
		return ansi.Truncate(line, contentWidth, "…")
	}
	// The final item is account/sign-in. Keep its keyboard index and action,
	// but reserve a bottom row for it rather than scrolling it with the library.
	accountIndex := len(items) - 1
	identity := renderItem(accountIndex)
	bodyHeight := max(0, height-lipgloss.Height(identity))
	lines := []string{lipgloss.NewStyle().Foreground(lipgloss.Color("#e6c945")).Bold(true).Render("TERMCADE"), ""}
	visibleRows := max(0, bodyHeight-len(lines))
	start := 0
	if m.app.nav < accountIndex {
		start = min(max(0, m.app.nav-visibleRows+1), max(0, accountIndex-visibleRows))
	}
	for i := start; i < accountIndex && len(lines) < bodyHeight; i++ {
		if items[i].Label == "Library" && items[i].Kind == "" {
			divider := dimStyle.Render(strings.Repeat("─", max(1, contentWidth)))
			if len(lines) < bodyHeight {
				lines = append(lines, divider)
			}
		}
		if len(lines) < bodyHeight {
			lines = append(lines, renderItem(i))
		}
	}
	return productWithFooter(strings.Join(lines, "\n"), identity, contentWidth, height)
}

func (m Model) productChromeFooter(width int) string {
	var notice []string
	if m.app.form != nil && m.app.loading {
		notice = append(notice, "Working… Esc stops waiting and refreshes state")
	}
	msg := m.app.notice
	if msg == "" && m.app.form == nil && m.screen == screenProduct {
		switch m.app.loc.Page {
		case "marketplace":
			msg = m.app.snapshot.CatalogError
			if m.app.search.active {
				msg = m.app.search.err
			}
		case "settings":
			msg = m.app.snapshot.AccountError
		}
		if msg == "" {
			msg = m.app.snapshot.AccountError
		}
	}
	if msg != "" {
		notice = append(notice, marketNotice.Render(sanitize(msg)))
	}
	page := m.productPageControls(width)
	if page == "" {
		page = dimStyle.Render(" ")
	}
	return productFooter(width, append(notice, page)...)
}

func (m Model) productPageControls(width int) string {
	if m.app.form != nil {
		return dimStyle.Render("Enter confirm · Esc cancel")
	}
	switch m.screen {
	case screenPlaying:
		hint := "Arrows/WASD move · Space/Z A · X B · Enter start"
		if m.game != nil {
			if gameHint := strings.TrimSpace(sanitize(m.game.HUD().Hint)); gameHint != "" {
				hint = gameHint
			}
		}
		return dimStyle.Render(hint + " · Esc pause · Ctrl+C quit")
	case screenPaused:
		return dimStyle.Render("Enter select · Esc resume")
	case screenGameOver:
		return dimStyle.Render("Enter play again · Esc Marketplace · Ctrl+C quit")
	case screenCrashed:
		return dimStyle.Render("Any key Marketplace · Ctrl+C quit")
	}
	var parts []string
	if legend := m.productActionLegend(); legend != "" {
		parts = append(parts, legend)
	}
	if m.app.searchFocus {
		parts = append(parts, dimStyle.Render("Ctrl+U clear query"))
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, " · ")
}

func (m Model) productPageView(width, height int) string {
	if m.app.loc.Page == "game" {
		return m.productGameDetailsView(width, height)
	}
	title := strings.ToUpper(m.app.loc.Page)
	if m.app.loc.ID != "" {
		title += " / " + sanitize(m.app.loc.ID)
	}
	header := titleStyle.Render(title)
	if m.app.loading {
		header += dimStyle.Render(" · loading…")
	}
	footer := m.productChromeFooter(width)
	available := max(0, height-lipgloss.Height(footer)-lipgloss.Height(header)-1)
	items := m.productItems()
	var lines []string
	selectedLine := 0
	for i, item := range items {
		if i == m.app.selection {
			selectedLine = len(lines)
		}
		var text string
		if item.Kind == "search" {
			text = m.productSearchView(width)
		} else {
			prefix := "  "
			if i == m.app.selection && m.app.focus == 1 && !m.app.searchFocus {
				prefix = "▸ "
			}
			label := productLabelLine(prefix, item.Label, item.Meta, max(1, width-2))
			if i == m.app.selection && m.app.focus == 1 && !m.app.searchFocus {
				label = selectedStyle.Render(label)
			}
			text = lipgloss.NewStyle().Width(max(1, width-2)).Render(label)
			if item.Detail != "" {
				text += "\n" + lipgloss.NewStyle().PaddingLeft(2).Width(max(1, width-2)).Render(dimStyle.Render(sanitizeBlock(item.Detail)))
			}
		}
		lines = append(lines, strings.Split(text, "\n")...)
		lines = append(lines, "")
	}
	if m.productSearchPage() && strings.TrimSpace(m.productQuery()) != "" {
		onlySearch := true
		for _, item := range items {
			if item.Kind != "search" {
				onlySearch = false
				break
			}
		}
		if onlySearch {
			lines = append(lines, "No matching games.")
		}
	}
	if len(lines) == 0 {
		lines = []string{"Nothing here yet. Use the focused actions or visit Marketplace."}
		if m.productSearchPage() && strings.TrimSpace(m.productQuery()) != "" {
			lines = []string{"No matching games."}
		}
		if m.app.loc.Page == "marketplace" && (m.app.search.loading || m.app.loading) {
			lines = []string{"Searching…"}
		}
		if m.app.loc.Page == "marketplace" && m.app.search.err != "" {
			lines = []string{"Search unavailable. Use Refresh to retry."}
		}
	}
	start := max(0, selectedLine-available+2)
	if m.app.manualScroll {
		start = m.app.scroll
	}
	start = min(start, max(0, len(lines)-available))
	content := boundedBlock(strings.Join(lines[start:min(len(lines), start+available)], "\n"), width, available)
	return productWithFooter(header+"\n\n"+content, footer, width, height)
}

func (m Model) productActionView(width int) string {
	var labels []string
	for i, a := range m.productActions() {
		if a.keyHint() != "" {
			continue
		}
		label := "[" + a.Label + "]"
		if a.Disabled {
			label = dimStyle.Render(label)
		} else if m.app.focus == 2 && i == m.app.action {
			label = selectedStyle.Render(label)
		}
		labels = append(labels, label)
	}
	joined := strings.Join(labels, " ")
	if width <= 0 {
		return joined
	}
	return lipgloss.NewStyle().Width(max(1, width)).Render(joined)
}

func (m Model) productFormView(width, height int) string {
	f := m.app.form
	lines := []string{titleStyle.Render(f.Title), ""}
	for i, label := range f.Labels {
		value := sanitize(f.Values[i])
		prefix := "  "
		if f.Index == i {
			prefix = "▸ "
			r := []rune(value)
			at := len(r)
			if len(f.Carets) > i {
				at = min(len(r), f.Carets[i])
			}
			start := 0
			for start < at && lipgloss.Width(string(r[start:at])) > max(1, (width-4)/2) {
				start++
			}
			value = string(r[start:at]) + "│" + string(r[at:])
		}
		lines = append(lines, dimStyle.Render(sanitize(label)), lipgloss.NewStyle().MaxWidth(max(1, width-2)).Render(prefix+value), "")
	}
	return productWithFooter(lipgloss.NewStyle().Width(width).Render(strings.Join(lines, "\n")), m.productChromeFooter(width), width, height)
}

func sanitizeBlock(value string) string {
	lines := strings.Split(value, "\n")
	for i, line := range lines {
		lines[i] = sanitize(line)
	}
	return strings.Join(lines, "\n")
}

func (m Model) productGameDetailsView(width, height int) string {
	title := titleStyle.Render("GAME")
	if m.app.loading {
		title += dimStyle.Render(" · loading…")
	}
	footer := m.productChromeFooter(width)
	return productWithFooter(title+"\n\n"+m.productGameHero(max(1, width), false), footer, width, height)
}

func (m Model) productGameHero(width int, compact bool) string {
	g := m.app.game
	author, slug, _ := strings.Cut(g.ID, "/")
	name := strings.TrimSpace("@" + author + " / " + slug)
	if author == "" {
		name = g.Name
		if name == "" {
			name = g.ID
		}
	}
	var left []string
	left = append(left, name)
	if g.Repo != "" {
		left = append(left, sanitize(g.Repo))
	}
	if g.Description != "" {
		left = append(left, dimStyle.Render(sanitizeBlock(g.Description)))
	}
	if idx := m.localIndex(g.ID); idx >= 0 && m.games[idx].Err != nil {
		left = append(left, "Cannot play\n"+dimStyle.Render(sanitize(m.games[idx].Err.Error())))
	}
	sep := "\n\n"
	if compact {
		sep = "\n"
	}
	leftBlock := strings.Join(left, sep)
	gap := 2
	col := max(1, (width-gap)/2)
	chart := playChart(m.app.days, max(1, col/2))
	leftCol := lipgloss.NewStyle().Width(col).MaxWidth(col).Align(lipgloss.Center).AlignVertical(lipgloss.Center).Render(leftBlock)
	rightCol := lipgloss.NewStyle().Width(col).MaxWidth(col).Align(lipgloss.Center).AlignVertical(lipgloss.Center).Render(chart)
	return lipgloss.JoinHorizontal(lipgloss.Center, leftCol, strings.Repeat(" ", gap), rightCol)
}

func (m Model) productGamePagePlayView(width, height int) string {
	footer := m.productChromeFooter(width)
	title := titleStyle.Render("GAME")
	if m.app.loading {
		title += dimStyle.Render(" · loading…")
	}
	hero := m.productGameHero(max(1, width), height < 36)
	top := title + "\n" + hero
	rest := max(1, height-lipgloss.Height(footer)-lipgloss.Height(top)-1)
	return productWithFooter(top+"\n"+m.productPlayfield(width, rest), footer, width, height)
}

func (m Model) productPlayfield(width, height int) string {
	if m.game == nil {
		return ""
	}
	info := m.game.Info()
	if m.screen == screenCrashed {
		return titleStyle.Render("GAME STOPPED") + "\n" + sanitizeBlock(m.crash)
	}
	hud := m.game.HUD()
	var fields []string
	for _, f := range hud.Fields {
		value := inputStyle.Render(sanitize(f.Value))
		if f.Accent {
			value = titleStyle.Render(sanitize(f.Value))
		}
		fields = append(fields, dimStyle.Render(sanitize(f.Label)+" ")+value)
	}
	fields = append(fields, dimStyle.Render(fmt.Sprintf("SCORE %d", m.game.Score())))
	status := strings.Join(fields, "   ")
	if m.app.notice != "" {
		status += " · " + marketNotice.Render(sanitize(m.app.notice))
	}
	oneLine := lipgloss.NewStyle().MaxWidth(max(1, width))
	requiredHeight := info.PixelH/2 + 1
	if width < info.PixelW+2 {
		return tooSmall(info.PixelW+2, requiredHeight, width, height)
	}
	gameW := max(info.PixelW+2, lipgloss.Width(m.frame), 1)
	playfield := lipgloss.NewStyle().Width(gameW).MaxWidth(gameW).Render(oneLine.Render(status) + "\n\n" + m.frame)
	return lipgloss.NewStyle().Width(width).Height(height).MaxHeight(height).Render(
		lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, playfield),
	)
}

func (m Model) productGameView(width, height int) string {
	if m.game == nil {
		return productWithFooter("No active game", m.productChromeFooter(width), width, height)
	}
	info := m.game.Info()
	frame := m.frame
	header := inputStyle.Bold(true).Render(sanitize(info.Title)) + dimStyle.Render(fmt.Sprintf("   best %d", m.scores.High(info.ID)))
	if played := m.scores.LastPlayed(info.ID); !played.IsZero() {
		header += dimStyle.Render(" · played " + productPlayedAgo(played, time.Now()))
	}
	footer := m.productChromeFooter(width)
	if m.screen == screenCrashed {
		stopped := header + "\n\n" + titleStyle.Render("GAME STOPPED") + "\n" + sanitizeBlock(m.crash)
		return productWithFooter(stopped, footer, width, height)
	}
	hud := m.game.HUD()
	var fields []string
	for _, f := range hud.Fields {
		value := inputStyle.Render(sanitize(f.Value))
		if f.Accent {
			value = titleStyle.Render(sanitize(f.Value))
		}
		fields = append(fields, dimStyle.Render(sanitize(f.Label)+" ")+value)
	}
	fields = append(fields, dimStyle.Render(fmt.Sprintf("SCORE %d", m.game.Score())))
	if m.screen == screenGameOver {
		header += titleStyle.Render(" · GAME OVER")
	} else if m.screen == screenPaused {
		header += titleStyle.Render(" · PAUSED")
	}
	status := strings.Join(fields, "   ")
	if m.app.notice != "" {
		status += " · " + marketNotice.Render(sanitize(m.app.notice))
	}
	// Title stays at the top. The playfield is centered in the remaining area.
	oneLine := lipgloss.NewStyle().MaxWidth(max(1, width))
	top := oneLine.Render(header)
	requiredHeight := info.PixelH/2 + 2 + lipgloss.Height(footer)
	bodyHeight := max(1, height-lipgloss.Height(footer))
	if width < info.PixelW+2 || height < requiredHeight {
		return productWithFooter(tooSmall(info.PixelW+2, requiredHeight, width, height), footer, width, height)
	}
	gameW := max(info.PixelW+2, lipgloss.Width(frame), 1)
	playfield := lipgloss.NewStyle().Width(gameW).MaxWidth(gameW).Render(oneLine.Render(status) + "\n\n" + frame)
	rest := max(1, bodyHeight-lipgloss.Height(top))
	playfield = lipgloss.Place(width, rest, lipgloss.Center, lipgloss.Center, playfield)
	return productWithFooter(top+"\n"+playfield, footer, width, height)
}
