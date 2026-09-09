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
type productItem struct{ Label, Detail, Kind, ID string }
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
			m.app.pairingURL = msg.Reply.PairingURL
			cmd := m.productRequest(ProductRequest{Kind: "pair-wait", Round: msg.Reply.Round})
			return m, cmd, true
		case "pair-wait":
			cmd := m.productRequest(ProductRequest{Kind: "save-login", Session: msg.Reply.Session})
			return m, cmd, true
		case "save-login":
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
			if selected != "" && item.ID == selected && item.Kind == selectedKind {
				m.app.selection = i
				break
			}
		}
		m.app.selection = clampIndex(m.app.selection, len(items))
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
		return m, nil, true
	case tea.PasteMsg:
		if m.app.form != nil && m.app.form.Index < len(m.app.form.Values) && !m.app.loading {
			m.appendFormText(msg.Content)
		}
		return m, nil, true
	case tea.KeyPressMsg:
		key := msg.String()
		if m.screen != screenPlaying && msg.IsRepeat && (key == "enter" || key == "tab" || key == "shift+tab" || key == "esc") {
			return m, nil, true
		}
		if key == "ctrl+c" {
			m.cancelProduct()
			m = m.closeProductGame()
			m.scores.Save()
			return m, tea.Quit, true
		}
		if m.screen == screenPlaying {
			return m, nil, false
		}
		if m.screen == screenPaused || m.screen == screenGameOver || m.screen == screenCrashed {
			if key == "tab" || key == "shift+tab" {
				m.app.gameNav = !m.app.gameNav
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
		if m.app.form != nil {
			return m.productFormKey(msg)
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
			return m, nil, true
		}
		if key == "tab" || key == "shift+tab" {
			delta := 1
			if key == "shift+tab" {
				delta = 2
			}
			m.app.focus = (m.app.focus + delta) % 3
			return m, nil, true
		}
		if m.app.focus == 0 {
			return m.productNavKey(key)
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

func clampIndex(i, n int) int {
	if n == 0 {
		return 0
	}
	return min(max(i, 0), n-1)
}

func (m Model) productNavItems() []productItem {
	items := []productItem{{Label: "Marketplace", Kind: "page", ID: "marketplace"}, {Label: "Docs", Kind: "page", ID: "docs"}, {Label: "Library", Kind: "page", ID: "library"}}
	for _, g := range m.app.snapshot.Library {
		items = append(items, productItem{Label: "  " + g.Name, Kind: "game", ID: g.ID})
	}
	label := "Sign in"
	if m.app.snapshot.SignedIn {
		label = "Account"
		if m.app.snapshot.Account.Username != "" {
			label = "@" + m.app.snapshot.Account.Username
		}
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
		m = m.closeProductGame()
		m.app.gameNav = false
		next, cmd := m.activateProduct(i.Kind, i.ID)
		return next, cmd, true
	}
	return m, nil, true
}

func (m Model) openProduct(loc productLocation, remember bool) (Model, tea.Cmd) {
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
	m.app.notice = ""
	m.app.days = nil
	var req ProductRequest
	switch loc.Page {
	case "game":
		m.app.game = m.metadata(loc.ID)
		req = ProductRequest{Kind: "game", ID: loc.ID}
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
	for _, collection := range [][]registry.Game{m.app.snapshot.Catalog, m.app.snapshot.Library, m.app.owner.Games} {
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

func (m Model) productRecentItems() []productItem {
	type recent struct {
		game   registry.Game
		played time.Time
		best   int
	}
	var entries []recent
	for _, g := range m.libraryGames() {
		played, best := m.scores.LastPlayed(g.ID), m.scores.High(g.ID)
		if m.app.snapshot.SignedIn {
			for _, activity := range m.app.snapshot.Activity {
				if activity.ID == g.ID {
					if activity.PlayedAt().After(played) {
						played = activity.PlayedAt()
					}
					best = max(best, activity.PersonalBest)
				}
			}
		}
		local := m.localIndex(g.ID)
		playable := local >= 0 && m.games[local].Err == nil
		if !played.IsZero() && (playable || (local < 0 && m.inAccount(g.ID) && g.HasPackage && g.ABI == sdk.ABIVersion)) {
			entries = append(entries, recent{g, played, best})
		}
	}
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].played.After(entries[j].played) })
	var out []productItem
	for _, e := range entries[:min(3, len(entries))] {
		out = append(out, productItem{Label: "Continue playing: " + e.game.Name + "  [Play]", Detail: fmt.Sprintf("best %d · played %s", e.best, productPlayedAgo(e.played, time.Now())), Kind: "play", ID: e.game.ID})
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

func (m Model) productItems() []productItem {
	var items []productItem
	gameRows := func(games []registry.Game) {
		for _, g := range games {
			items = append(items, productItem{Label: g.ID + "  · " + m.installationStatus(g.ID), Detail: g.Name + " · " + g.Description, Kind: "game", ID: g.ID})
		}
	}
	switch m.app.loc.Page {
	case "marketplace":
		gameRows(m.app.snapshot.Catalog)
	case "library":
		items = append(items, m.productRecentItems()...)
		gameRows(m.libraryGames())
	case "game":
		g := m.app.game
		owner, _, _ := strings.Cut(g.ID, "/")
		release := "No published release"
		if g.HasPackage {
			release = fmt.Sprintf("Release %s · ABI %d · %d×%d", g.Version, g.ABI, g.Width, g.Height)
		}
		items = append(items, productItem{Label: g.Name, Detail: g.Description}, productItem{Label: "@" + owner, Kind: "owner", ID: owner}, productItem{Label: "Repository", Detail: g.Repo, Kind: "open-url", ID: g.Repo}, productItem{Label: release, Detail: m.installationStatus(g.ID)}, productItem{Label: "Public play activity", Detail: playSummary(m.app.days), Kind: "chart", ID: g.ID})
		if idx := m.localIndex(g.ID); idx >= 0 && m.games[idx].Err != nil {
			items = append(items, productItem{Label: "Cannot play", Detail: m.games[idx].Err.Error()})
		}
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
		items = append(items, productItem{Label: "Secure browser approval", Detail: "Never enter an account password or email verification code here."}, productItem{Label: m.app.pairingURL, Kind: "open-url", ID: m.app.pairingURL}, productItem{Label: "Pairing code: " + m.app.round.UserCode, Detail: "Waiting for approval; Esc cancels. Browser opens only on explicit selection."})
	case "docs":
		items = append(items, productItem{Label: "TERMCADE", Detail: "One arcade in your terminal and browser."}, productItem{Label: "Library", Detail: "Add saves account membership. Play installs a missing package. Local installations remain playable offline."}, productItem{Label: "Two removal actions", Detail: "Remove from Library changes your account. Uninstall here removes only this machine's copy."}, productItem{Label: "Build a game", Detail: "termcade dev new author/slug\ntermcade dev build\ntermcade dev install game.tcade"}, productItem{Label: "Publish", Detail: "termcade publish <github-repo> <tag> [asset]"}, productItem{Label: "Keyboard", Detail: "Tab/Shift+Tab focus · arrows select · Enter activate · Esc back/pause · Ctrl+C quit. PgUp/PgDown scroll long text."}, productItem{Label: "Gameplay", Detail: "Arrows/WASD to move · Space/Z action A · X action B · Enter start. Pause retains keyboard navigation and pixel selection."})
	}
	return items
}

func playSummary(days []registry.PlayDay) string {
	n := 0
	for _, d := range days {
		n += d.Count
	}
	return fmt.Sprintf("%d plays · %d UTC days this year · Enter for daily counts", n, len(days)) + "\n" + playHeatmap(days)
}

func playHeatmap(days []registry.PlayDay) string {
	if len(days) == 0 {
		return "Activity unavailable or no daily data"
	}
	start, err := time.Parse("2006-01-02", days[0].Day)
	if err != nil {
		return "Use daily view to inspect activity"
	}
	start = start.AddDate(0, 0, -int(start.Weekday()))
	byDate := map[string]int{}
	last := start
	for _, day := range days {
		d, e := time.Parse("2006-01-02", day.Day)
		if e != nil {
			continue
		}
		byDate[day.Day] = day.Count
		if d.After(last) {
			last = d
		}
	}
	weeks := min(54, int(last.Sub(start).Hours()/24)/7+1)
	var lines []string
	for row, label := range []string{"S", "M", "T", "W", "T", "F", "S"} {
		line := label + " "
		for week := 0; week < weeks; week++ {
			date := start.AddDate(0, 0, week*7+row).Format("2006-01-02")
			if count, ok := byDate[date]; ok {
				line += playIntensity(count, days)
			} else {
				line += " "
			}
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}
func playIntensity(count int, days []registry.PlayDay) string {
	peak := 0
	for _, d := range days {
		peak = max(peak, d.Count)
	}
	if count <= 0 {
		return "░"
	}
	if count*4 <= peak {
		return "▁"
	}
	if count*2 <= peak {
		return "▃"
	}
	if count*4 <= peak*3 {
		return "▆"
	}
	return "█"
}

func (m Model) productActions() []productAction {
	a := []productAction{{Label: "Back", Kind: "back"}, {Label: "Refresh", Kind: "refresh"}}
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
	} else if m.app.loc.Page == "library" || m.app.loc.Page == "marketplace" || m.app.loc.Page == "owner" {
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
		local := m.localIndex(id)
		canPlay := local >= 0 && m.games[local].Err == nil
		canDownload := local < 0 && m.inAccount(id) && g.HasPackage && g.ABI == sdk.ABIVersion
		a = append(a, productAction{Label: "Play", Kind: "play", ID: id, Disabled: !canPlay && !canDownload})
		if m.inAccount(id) {
			a = append(a, productAction{Label: "Remove from Library", Kind: "remove", ID: id})
		} else {
			a = append(a, productAction{Label: "Add", Kind: "add", ID: id, Disabled: !m.app.snapshot.SignedIn || !m.app.snapshot.LibraryKnown})
		}
		if local >= 0 {
			a = append(a, productAction{Label: "Uninstall here", Kind: "confirm-uninstall", ID: id})
		}
	}
	for i := range a {
		if m.app.loading && a[i].Kind != "back" && a[i].Kind != "open-url" && (a[i].Kind != "play" || m.localIndex(a[i].ID) < 0 || m.app.mutating) {
			a[i].Disabled = true
		}
	}
	return a
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
		if i := m.localIndex(id); i >= 0 {
			if m.games[i].Err != nil {
				m.app.notice = "Broken package: uninstall or replace it explicitly"
				return m, nil
			}
			m.cancelProduct()
			next, cmd := m.startGame(i)
			return next.(Model), cmd
		}
		g := m.metadata(id)
		if !m.inAccount(id) || !g.HasPackage || g.ABI != sdk.ABIVersion {
			m.app.notice = "Add a compatible published game to your Library first"
			return m, nil
		}
		cmd := m.productRequest(ProductRequest{Kind: "install", ID: id})
		return m, cmd
	case "add", "remove":
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
		m.app.history = append(m.app.history, m.app.loc)
		m.app.loc = productLocation{Page: "pairing"}
		m.app.focus = 2
		m.app.action = 0
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
	choices := []string{"Resume", "Restart", "Pixel style (next start)", "Leave Play"}
	switch key {
	case "esc", "p":
		next, cmd := m.resumeGame()
		return next.(Model), cmd, true
	case "up":
		m.pauseIdx = (m.pauseIdx + len(choices) - 1) % len(choices)
	case "down":
		m.pauseIdx = (m.pauseIdx + 1) % len(choices)
	case "enter":
		switch m.pauseIdx {
		case 0:
			next, cmd := m.resumeGame()
			return next.(Model), cmd, true
		case 1:
			next, cmd := m.startGame(m.localIndex(m.game.Info().ID))
			return next.(Model), cmd, true
		case 2:
			next, cmd := m.cyclePixels()
			return next.(Model), cmd, true
		case 3:
			m = m.closeProductGame()
			next, cmd := m.openProduct(productLocation{Page: "library"}, false)
			return next, tea.Batch(cmd, saveScores(m.scores), m.syncCmd()), true
		}
	}
	return m, nil, true
}

// boundedBlock constrains both axes in display cells, including ANSI styling.
func boundedBlock(s string, w, h int) string {
	if w <= 0 || h <= 0 {
		return ""
	}
	return lipgloss.NewStyle().Width(w).MaxWidth(w).Height(h).MaxHeight(h).Render(s)
}

func (m Model) productView() string {
	if m.termW < 1 || m.termH < 1 {
		return ""
	}
	gameRoute := m.screen != screenProduct
	showRail := m.termW >= sidebarBreakpoint
	if gameRoute && m.game != nil && m.termW-sidebarWidth < m.game.Info().PixelW+2 {
		showRail = false
	}
	navFocus := (!gameRoute && m.app.focus == 0) || m.app.gameNav
	width := m.termW
	if showRail {
		width -= sidebarWidth
	}
	contentWidth := min(width, productContentMax)
	if gameRoute {
		contentWidth = width
	}
	var body string
	if gameRoute {
		body = m.productGameView(width, m.termH)
	} else if m.app.form != nil {
		body = m.productFormView(contentWidth, m.termH)
	} else {
		body = m.productPageView(contentWidth, m.termH)
	}
	body = lipgloss.PlaceHorizontal(width, lipgloss.Center, boundedBlock(body, contentWidth, m.termH))
	if navFocus && !showRail {
		columns := min(sidebarWidth, m.termW)
		background := strings.Split(boundedBlock(body, m.termW, m.termH), "\n")
		nav := strings.Split(m.productNavView(columns, m.termH), "\n")
		nav[0] = boundedBlock("NAVIGATION", columns-1, 1)
		for i := range background {
			left := ""
			if i < len(nav) {
				left = nav[i]
			}
			background[i] = boundedBlock(left, columns-1, 1) + dimStyle.Render("│") + lipgloss.NewStyle().Faint(true).Render(ansi.Cut(background[i], columns, m.termW))
		}
		return boundedBlock(strings.Join(background, "\n"), m.termW, m.termH)
	}
	if showRail {
		nav := strings.Split(m.productNavView(sidebarWidth, m.termH), "\n")
		for i := range nav {
			nav[i] += dimStyle.Render("│")
		}
		body = lipgloss.JoinHorizontal(lipgloss.Top, boundedBlock(strings.Join(nav, "\n"), sidebarWidth, m.termH), body)
	}
	return boundedBlock(body, m.termW, m.termH)
}

func (m Model) productNavView(width, height int) string {
	items := m.productNavItems()
	lines := []string{lipgloss.NewStyle().Foreground(lipgloss.Color("#3fc4c9")).Bold(true).Render("TERMCADE"), ""}
	start := max(0, m.app.nav-max(1, height-5)+1)
	for i := start; i < len(items) && len(lines) < height-2; i++ {
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
		lines = append(lines, line)
	}
	lines = append(lines, "", "Tab: change focus")
	return boundedBlock(strings.Join(lines, "\n"), width-1, height)
}

func (m Model) productPageView(width, height int) string {
	title := strings.ToUpper(m.app.loc.Page)
	if m.app.loc.ID != "" {
		title += " / " + sanitize(m.app.loc.ID)
	}
	header := titleStyle.Render(title)
	if m.app.loading {
		header += dimStyle.Render(" · loading…")
	}
	notice := m.app.notice
	if notice == "" {
		switch m.app.loc.Page {
		case "marketplace":
			notice = m.app.snapshot.CatalogError
		case "library":
			notice = m.app.snapshot.LibraryError
		case "settings":
			notice = m.app.snapshot.AccountError
		}
	}
	if notice == "" {
		notice = m.app.snapshot.AccountError
	}
	footer := m.productActionView(width) + "\n" + dimStyle.Render("Tab focus · ↑/↓ select · Enter activate · Esc back · PgUp/PgDn scroll")
	footerLines := strings.Count(footer, "\n") + 1
	if notice != "" {
		footer = marketNotice.Render(sanitize(notice)) + "\n" + footer
		footerLines++
	}
	available := max(1, height-footerLines-3)
	items := m.productItems()
	var lines []string
	selectedLine := 0
	for i, item := range items {
		if i == m.app.selection {
			selectedLine = len(lines)
		}
		label := "  " + sanitize(item.Label)
		if i == m.app.selection && m.app.focus == 1 {
			label = selectedStyle.Render("▸ " + sanitize(item.Label))
		}
		text := lipgloss.NewStyle().Width(max(1, width-2)).Render(label)
		if item.Detail != "" {
			text += "\n" + lipgloss.NewStyle().PaddingLeft(2).Width(max(1, width-2)).Render(dimStyle.Render(sanitizeBlock(item.Detail)))
		}
		lines = append(lines, strings.Split(text, "\n")...)
		lines = append(lines, "")
	}
	if len(lines) == 0 {
		lines = []string{"Nothing here yet. Use the focused actions or visit Marketplace."}
	}
	start := max(0, selectedLine-available+2)
	if m.app.manualScroll {
		start = m.app.scroll
	}
	start = min(start, max(0, len(lines)-available))
	content := boundedBlock(strings.Join(lines[start:min(len(lines), start+available)], "\n"), width, available)
	return boundedBlock(header+"\n\n"+content+"\n"+footer, width, height)
}

func (m Model) productActionView(width int) string {
	var labels []string
	for i, a := range m.productActions() {
		label := "[" + a.Label + "]"
		if a.Disabled {
			label = dimStyle.Render(label)
		} else if m.app.focus == 2 && i == m.app.action {
			label = selectedStyle.Render(label)
		}
		labels = append(labels, label)
	}
	return lipgloss.NewStyle().Width(max(1, width)).Render(strings.Join(labels, " "))
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
	buttons := []string{"Submit", "Cancel"}
	if f.Kind == "username" {
		buttons = []string{"Save handle", "Check availability", "Cancel"}
	}
	for i, b := range buttons {
		if f.Index == len(f.Values)+i {
			b = selectedStyle.Render("[" + b + "]")
		} else {
			b = "[" + b + "]"
		}
		lines = append(lines, b)
	}
	if m.app.loading {
		lines = append(lines, "Working… Esc stops waiting and refreshes state")
	}
	if m.app.notice != "" {
		lines = append(lines, marketNotice.Render(sanitize(m.app.notice)))
	}
	lines = append(lines, "", "Tab/Shift+Tab focus · Enter button · Esc cancel")
	return boundedBlock(lipgloss.NewStyle().Width(width).Render(strings.Join(lines, "\n")), width, height)
}

func sanitizeBlock(value string) string {
	lines := strings.Split(value, "\n")
	for i, line := range lines {
		lines[i] = sanitize(line)
	}
	return strings.Join(lines, "\n")
}

func (m Model) productGameView(width, height int) string {
	if m.game == nil {
		return "No active game"
	}
	info := m.game.Info()
	if width < info.PixelW+2 || height < info.PixelH/2+4 {
		return tooSmall(info.PixelW+2, info.PixelH/2+4, width, height)
	}
	frame := m.frame
	if m.screen == screenPaused {
		lines := []string{"PAUSED", ""}
		for i, label := range []string{"Resume", "Restart", "Pixel style: " + m.shape.Name + " (next start)", "Leave Play"} {
			if i == m.pauseIdx {
				label = selectedStyle.Render("▸ " + label)
			}
			lines = append(lines, label)
		}
		frame = compose(frame, borderStyle.Render(strings.Join(lines, "\n")))
	}
	if m.screen == screenCrashed {
		frame = compose(frame, crashBox(m.crash))
	}
	header := inputStyle.Bold(true).Render(sanitize(info.Title)) + dimStyle.Render(fmt.Sprintf("   best %d", m.scores.High(info.ID)))
	if played := m.scores.LastPlayed(info.ID); !played.IsZero() {
		header += dimStyle.Render(" · played " + productPlayedAgo(played, time.Now()))
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
	text := header + "\n" + frame + "\n" + strings.Join(fields, "   ") + "\n" + dimStyle.Render(sanitize(hud.Hint))
	if m.screen == screenGameOver {
		text += "\n" + titleStyle.Render("[Enter: play again]  [Esc: Library]  [Tab: navigation]")
	} else if m.screen == screenPaused {
		text += "\n" + dimStyle.Render("↑/↓ select · Enter activate · Esc resume · Tab navigation")
	} else {
		footer := "Esc pause · Ctrl+C quit"
		if m.app.notice != "" {
			footer += " · " + sanitize(m.app.notice)
		}
		text += "\n" + dimStyle.Render(footer)
	}
	return boundedBlock(text, width, height)
}
