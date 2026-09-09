package shell

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/aviorstudio/termcade/internal/engine"
	"github.com/aviorstudio/termcade/internal/registry"
	"github.com/aviorstudio/termcade/internal/scores"
	"github.com/aviorstudio/termcade/sdk"
	"github.com/charmbracelet/x/ansi"
)

type productGame struct {
	fakeGame
	id string
}

func (g *productGame) Info() sdk.Info {
	return sdk.Info{ID: g.id, Title: "LOCAL", PixelW: 16, PixelH: 8}
}

type productFixture struct {
	snapshot ProductSnapshot
	calls    []ProductRequest
	failure  error
	game     *productGame
}

func newProductFixture(t *testing.T) (Model, *productFixture) {
	t.Helper()
	config := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", config)
	t.Setenv("HOME", config)
	t.Setenv("AppData", config)
	st, err := scores.Load()
	if err != nil {
		t.Fatal(err)
	}
	g := &productGame{id: "acme/local"}
	reg := engine.Registration{Info: g.Info(), New: func(sdk.CellShape) (sdk.Game, error) { return g, nil }, Installed: true, Version: "1.0.0"}
	f := &productFixture{game: g, snapshot: ProductSnapshot{
		Catalog: []registry.Game{{ID: "acme/local", Name: "Local", Version: "1.0.0", HasPackage: true, ABI: 1}, {ID: "acme/remote", Name: "Remote", Version: "2.0.0", HasPackage: true, ABI: 1}, {ID: "acme/future", Name: "Future", HasPackage: true, ABI: 2}, {ID: "acme/unreleased", Name: "Unreleased"}},
		Library: []registry.Game{{ID: "acme/remote", Name: "Remote", Version: "2.0.0", HasPackage: true, ABI: 1}}, Installed: []engine.Registration{reg},
		SignedIn: true, LibraryKnown: true, CredentialID: "this-device", Account: registry.Me{Email: "dev@example.test", Username: "dev", Orgs: []registry.Org{{Username: "team", Admin: true}}},
	}}
	service := &ProductServices{Request: func(ctx context.Context, r ProductRequest) (ProductReply, error) {
		f.calls = append(f.calls, r)
		if ctx.Err() != nil {
			return ProductReply{}, ctx.Err()
		}
		if f.failure != nil {
			return ProductReply{}, f.failure
		}
		switch r.Kind {
		case "game":
			for _, game := range f.snapshot.Catalog {
				if game.ID == r.ID {
					return ProductReply{Game: &game}, nil
				}
			}
			return ProductReply{}, registry.ErrNotFound
		case "owner":
			return ProductReply{Owner: &registry.HandleOwner{Name: r.ID, IsOrg: true, Bio: "studio", Games: f.snapshot.Catalog}}, nil
		case "add":
			for _, game := range f.snapshot.Catalog {
				if game.ID == r.ID {
					f.snapshot.Library = append(f.snapshot.Library, game)
				}
			}
		case "remove":
			var keep []registry.Game
			for _, game := range f.snapshot.Library {
				if game.ID != r.ID {
					keep = append(keep, game)
				}
			}
			f.snapshot.Library = keep
		case "uninstall":
			f.snapshot.Installed = nil
		case "pair-start":
			return ProductReply{Round: registry.DeviceRound{UserCode: "ABCD-EFGH", ExpiresIn: time.Minute}, PairingURL: "http://127.0.0.1:8081/pair"}, nil
		case "pair-wait":
			return ProductReply{Session: registry.Session{Token: "must-not-render", CredentialID: "new-device"}}, nil
		case "sessions":
			return ProductReply{Credentials: []registry.CLICredential{{ID: "this-device", DeviceName: "Laptop"}}}, nil
		case "members":
			return ProductReply{Members: []registry.Member{{Email: "member@example.test"}}}, nil
		}
		snapshot := f.snapshot
		return ProductReply{Snapshot: &snapshot}, nil
	}}
	m := New(f.snapshot.Installed, st, sdk.Quadrant, &Marketplace{Product: service})
	t.Cleanup(m.app.cancel)
	m, _ = step(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
	return m, f
}

func productKey(name string) tea.KeyPressMsg {
	codes := map[string]rune{"tab": tea.KeyTab, "enter": tea.KeyEnter, "esc": tea.KeyEscape, "up": tea.KeyUp, "down": tea.KeyDown, "left": tea.KeyLeft, "right": tea.KeyRight, "backspace": tea.KeyBackspace, "pgdown": tea.KeyPgDown, "pgup": tea.KeyPgUp}
	if name == "shift+tab" {
		return tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift}
	}
	if name == "ctrl+c" {
		return tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}
	}
	if code, ok := codes[name]; ok {
		return tea.KeyPressMsg{Code: code}
	}
	r := []rune(name)
	return tea.KeyPressMsg{Code: r[0], Text: name}
}

func TestProductStartsOnMarketplaceAndDefersServiceWork(t *testing.T) {
	m, f := newProductFixture(t)
	if m.screen != screenProduct || m.app.loc.Page != "marketplace" || len(f.calls) != 0 {
		t.Fatal("startup contract")
	}
	m = drain(t, m, m.Init())
	if len(f.calls) != 1 || f.calls[0].Kind != "load" || len(m.productItems()) != 4 {
		t.Fatalf("startup load: %#v", f.calls)
	}
	if !strings.Contains(view(m), "MARKETPLACE") {
		t.Fatal("no marketplace")
	}
}

func TestProductEnterOpensDetailsAndAddDoesNotInstall(t *testing.T) {
	m, f := newProductFixture(t)
	m = drain(t, m, m.Init())
	m, cmd := step(t, m, productKey("enter"))
	m = drain(t, m, cmd)
	if m.app.loc != (productLocation{Page: "game", ID: "acme/local"}) {
		t.Fatal(m.app.loc)
	}
	for _, call := range f.calls {
		if call.Kind == "install" || call.Kind == "add" {
			t.Fatal("Enter mutated library")
		}
	}
	m, cmd = m.activateProduct("add", "acme/local")
	m = drain(t, m, cmd)
	if !m.inAccount("acme/local") || len(m.games) != 1 {
		t.Fatal("Add did not preserve local copy")
	}
	for _, call := range f.calls {
		if call.Kind == "install" {
			t.Fatal("Add downloaded")
		}
	}
}

func TestProductLibraryMembershipAndFilesAreIndependent(t *testing.T) {
	m, _ := newProductFixture(t)
	m = drain(t, m, m.Init())
	if len(m.libraryGames()) != 2 || m.gameBadge("acme/remote") != "Not installed" || m.gameBadge("acme/local") != "Installed · Local only" {
		t.Fatal("union or badges")
	}
	m, cmd := m.activateProduct("add", "acme/local")
	m = drain(t, m, cmd)
	m, cmd = m.activateProduct("remove", "acme/local")
	m = drain(t, m, cmd)
	if m.inAccount("acme/local") || m.localIndex("acme/local") < 0 {
		t.Fatal("Remove deleted local copy")
	}
	m, cmd = m.activateProduct("add", "acme/local")
	m = drain(t, m, cmd)
	m, _ = m.activateProduct("confirm-uninstall", "acme/local")
	m, _ = step(t, m, tea.PasteMsg{Content: "acme/local"})
	m, _ = step(t, m, productKey("tab"))
	m, cmd = step(t, m, productKey("enter"))
	m = drain(t, m, cmd)
	if !m.inAccount("acme/local") || m.localIndex("acme/local") >= 0 {
		t.Fatal("Uninstall changed membership")
	}
}

func TestProductUnknownMembershipAndOfflinePlay(t *testing.T) {
	m, f := newProductFixture(t)
	f.snapshot.LibraryKnown = false
	f.snapshot.Library = nil
	m = drain(t, m, m.Init())
	if m.gameBadge("acme/local") != "Installed · membership unknown" {
		t.Fatal(m.gameBadge("acme/local"))
	}
	m.app.snapshot.SignedIn = false
	f.calls = nil
	m, cmd := m.activateProduct("play", "acme/local")
	if cmd == nil || m.screen != screenPlaying || len(f.calls) != 0 {
		t.Fatal("offline play contacted account")
	}
	m, _ = step(t, m, productKey("left"))
	if f.game.lastKey != sdk.KeyLeft {
		t.Fatal("game input lost")
	}
	m, _ = step(t, m, productKey("tab"))
	if m.app.gameNav {
		t.Fatal("navigation captured live game focus")
	}
	m, _ = step(t, m, productKey("esc"))
	if m.screen != screenPaused {
		t.Fatal("pause missing")
	}
	m, _ = step(t, m, productKey("tab"))
	if !m.app.gameNav {
		t.Fatal("paused navigation unavailable")
	}
	m, _ = step(t, m, productKey("esc"))
	if m.app.gameNav || m.screen != screenPaused {
		t.Fatal("navigation Escape resumed game")
	}
	m, _ = step(t, m, productKey("esc"))
	if m.screen != screenPlaying {
		t.Fatal("resume missing")
	}
	m = m.closeProductGame()
}

func TestProductOldRepliesCannotChangeNewRouteOrSaveLogin(t *testing.T) {
	m, f := newProductFixture(t)
	m = drain(t, m, m.Init())
	m, cmd := m.openProduct(productLocation{Page: "owner", ID: "old"}, true)
	reply := cmd()
	m, _ = m.openProduct(productLocation{Page: "docs"}, true)
	m, _ = step(t, m, reply)
	if m.app.loc.Page != "docs" || m.app.owner.Name != "old" || m.app.owner.Bio != "" {
		t.Fatal("stale reply replaced route")
	}
	m, _ = m.activateProduct("sign-in", "")
	gen := m.app.gen
	m, _ = m.activateProduct("back", "")
	m, cmd = step(t, m, productMsg{Gen: gen, Request: ProductRequest{Kind: "pair-wait"}, Reply: ProductReply{Session: registry.Session{Token: "must-not-render"}}})
	if cmd != nil {
		t.Fatal("stale pairing scheduled credential persistence")
	}
	if strings.Contains(view(m), "must-not-render") {
		t.Fatal("credential displayed")
	}
	for _, call := range f.calls {
		if call.Kind == "save-login" {
			t.Fatal("stale login saved")
		}
	}
}

func TestProductAccountChangesDropPrivateViews(t *testing.T) {
	m, _ := newProductFixture(t)
	m = drain(t, m, m.Init())
	m.app.loc = productLocation{Page: "members", ID: "team"}
	m.app.members = []registry.Member{{Email: "private@example.test"}}
	m.app.credentials = []registry.CLICredential{{ID: "private"}}
	m, _ = step(t, m, productMsg{Gen: m.app.gen, Request: ProductRequest{Kind: "members"}, Err: registry.ErrLoginRequired})
	if m.app.snapshot.SignedIn || len(m.app.members) != 0 || len(m.app.credentials) != 0 || m.app.loc.Page != "settings" {
		t.Fatal("private account view survived expiry")
	}
	if m.orgAdmin("team") {
		t.Fatal("expired account retained admin controls")
	}
}

func TestProductHandleAndOrgFormsKeepActionsExplicit(t *testing.T) {
	m, f := newProductFixture(t)
	m = drain(t, m, m.Init())
	f.calls = nil
	m, _ = m.activateProduct("form-username", "")
	m, _ = step(t, m, tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl})
	m, _ = step(t, m, productKey("new-handle"))
	m, _ = step(t, m, productKey("tab"))
	m, _ = step(t, m, productKey("tab"))
	m, cmd := step(t, m, productKey("enter"))
	m = drain(t, m, cmd)
	if len(f.calls) != 1 || f.calls[0].Kind != "username-check" || f.calls[0].Value != "new-handle" || m.app.form == nil {
		t.Fatal("availability check mutated or closed the form")
	}
	m, _ = step(t, m, productKey("esc"))
	m, _ = m.activateProduct("form-org", "")
	for _, value := range []string{"studio", "Makes games", "https://example.test"} {
		m, _ = step(t, m, productKey(value))
		m, _ = step(t, m, productKey("tab"))
	}
	m, cmd = step(t, m, productKey("enter"))
	m = drain(t, m, cmd)
	last := f.calls[len(f.calls)-1]
	if last.Kind != "org-create" || last.ID != "studio" || last.Value != "Makes games" || last.Extra != "https://example.test" {
		t.Fatalf("wrong org fields: %#v", last)
	}
}

func TestProductUnicodeEditingAndLongFieldsStayBounded(t *testing.T) {
	m, _ := newProductFixture(t)
	m = drain(t, m, m.Init())
	m, _ = m.activateProduct("form-org", "")
	m, _ = step(t, m, productKey("a界b"))
	m, _ = step(t, m, productKey("left"))
	m, _ = step(t, m, productKey("backspace"))
	if m.app.form.Values[0] != "ab" {
		t.Fatal("editing split a Unicode character")
	}
	m, _ = step(t, m, tea.PasteMsg{Content: strings.Repeat("x", 1500)})
	for _, line := range strings.Split(view(m), "\n") {
		if lipgloss.Width(line) > 80 {
			t.Fatal("field exceeded terminal width")
		}
	}
	if !strings.Contains(view(m), "Submit") {
		t.Fatal("long field hid submission controls")
	}
}

func TestProductHeatmapPreservesZeroDaysAndIntensity(t *testing.T) {
	days := []registry.PlayDay{{Day: "2024-02-25", Count: 0}, {Day: "2024-02-26", Count: 1}, {Day: "2024-02-27", Count: 2}, {Day: "2024-02-28", Count: 3}, {Day: "2024-02-29", Count: 4}}
	heatmap := playHeatmap(days)
	if len(strings.Split(heatmap, "\n")) != 7 {
		t.Fatal("week rows missing")
	}
	for _, glyph := range []string{"░", "▁", "▃", "▆", "█"} {
		if !strings.Contains(heatmap, glyph) {
			t.Fatal("missing activity level", glyph)
		}
	}
	if !strings.Contains(playSummary(days), "10 plays") {
		t.Fatal("wrong total")
	}
}

func TestProductDissolveReturnsToContentFocusAndChartRefreshRefetches(t *testing.T) {
	m, f := newProductFixture(t)
	m = drain(t, m, m.Init())
	m.app.loc = productLocation{Page: "members", ID: "team"}
	m.app.focus = 2
	snapshot := f.snapshot
	m, _ = step(t, m, productMsg{Gen: m.app.gen, Request: ProductRequest{Kind: "org-delete", ID: "team"}, Reply: ProductReply{Snapshot: &snapshot}})
	if m.app.loc.Page != "settings" || m.app.focus != 1 || m.app.selection != 0 {
		t.Fatal("dissolve returned to the wrong focus")
	}
	m, cmd := m.openProduct(productLocation{Page: "chart", ID: "acme/local"}, false)
	m = drain(t, m, cmd)
	if f.calls[len(f.calls)-1].Kind != "game" || m.app.loc.Page != "chart" {
		t.Fatal("chart refresh did not refetch game activity")
	}
}

func TestProductCannotPlayBrokenOrUnsupportedUninstalledGames(t *testing.T) {
	m, f := newProductFixture(t)
	f.snapshot.Installed[0].Err = errors.New("bad package")
	f.snapshot.Library = append(f.snapshot.Library, f.snapshot.Catalog[2])
	m = drain(t, m, m.Init())
	for _, id := range []string{"acme/local", "acme/future"} {
		m.app.loc = productLocation{Page: "game", ID: id}
		m.app.game = m.metadata(id)
		for _, action := range m.productActions() {
			if action.Kind == "play" && !action.Disabled {
				t.Fatal("invalid game enabled Play", id)
			}
		}
	}
}

func TestProductPagingKeepsActionsOnTheVisibleSelection(t *testing.T) {
	m, f := newProductFixture(t)
	for i := 0; i < 30; i++ {
		f.snapshot.Catalog = append(f.snapshot.Catalog, registry.Game{ID: fmt.Sprintf("acme/extra-%d", i), Name: "Extra"})
	}
	m = drain(t, m, m.Init())
	m, _ = step(t, m, productKey("pgdown"))
	if m.app.selection == 0 || m.app.manualScroll {
		t.Fatal("paging left actions targeting the hidden first row")
	}
	m.app.loc = productLocation{Page: "owner", ID: "acme"}
	m.app.owner = registry.HandleOwner{Name: "acme", Games: f.snapshot.Catalog}
	m.app.selection = 3
	found := false
	for _, a := range m.productActions() {
		if a.Kind == "play" && a.ID == "acme/local" {
			found = true
		}
	}
	if !found {
		t.Fatal("owner game cards lack Play")
	}
}

func TestProductPairingNeverOpensBrowserAutomatically(t *testing.T) {
	m, f := newProductFixture(t)
	m = drain(t, m, m.Init())
	f.calls = nil
	m, cmd := m.activateProduct("sign-in", "")
	m = drain(t, m, cmd)
	for _, call := range f.calls {
		if call.Kind == "open-url" {
			t.Fatal("pairing launched a browser without selection")
		}
	}
	m.app.loc = productLocation{Page: "pairing"}
	m.app.pairingURL = "http://127.0.0.1:8081/pair"
	m.app.focus = 2
	m.app.action = 0
	_, cmd = step(t, m, tea.KeyPressMsg{Code: tea.KeyEnter, IsRepeat: true})
	if cmd != nil {
		t.Fatal("key repeat launched browser")
	}
}

func TestProductOrdinaryMemberCannotUseAdminForms(t *testing.T) {
	m, f := newProductFixture(t)
	f.snapshot.Account.Orgs[0].Admin = false
	m = drain(t, m, m.Init())
	f.calls = nil
	m.app.loc = productLocation{Page: "members", ID: "team"}
	m.app.members = []registry.Member{{Email: "member@example.test"}}
	m, _ = m.activateProduct("form-member", "member@example.test")
	if m.app.form != nil || len(f.calls) != 0 {
		t.Fatal("member received an admin form")
	}
	for _, action := range m.productActions() {
		if strings.Contains(action.Kind, "member") || action.Kind == "confirm-org" {
			t.Fatal("admin controls visible")
		}
	}
}

func TestProductCancelAfterACommittedMutationReconcilesFiles(t *testing.T) {
	m, _ := newProductFixture(t)
	m = drain(t, m, m.Init())
	m, _ = m.activateProduct("confirm-uninstall", "acme/local")
	m, _ = step(t, m, tea.PasteMsg{Content: "acme/local"})
	m, _ = step(t, m, productKey("tab"))
	m, cmd := step(t, m, productKey("enter"))
	late := cmd() // the confirmed mutation committed before Escape
	m, refresh := step(t, m, productKey("esc"))
	if refresh == nil {
		t.Fatal("canceled mutation did not reconcile")
	}
	m = drain(t, m, refresh)
	m, _ = step(t, m, late)
	if m.localIndex("acme/local") >= 0 || m.app.loading {
		t.Fatal("stale installed state survived reconciliation")
	}
}

func TestProductRemoteContinuePlayingDoesNotRequireAnInstall(t *testing.T) {
	m, f := newProductFixture(t)
	f.snapshot.Activity = []registry.Activity{{ID: "acme/remote", PersonalBest: 42, LastPlayed: time.Now().UTC().Format(time.RFC3339)}}
	m = drain(t, m, m.Init())
	items := m.productRecentItems()
	if len(items) != 1 || items[0].ID != "acme/remote" || items[0].Kind != "play" || !strings.Contains(items[0].Detail, "best 42") {
		t.Fatal("remote recent game missing")
	}
	if m.localIndex("acme/remote") >= 0 {
		t.Fatal("reading recent games installed one")
	}
	m.app.snapshot.SignedIn = false
	if len(m.productRecentItems()) != 0 {
		t.Fatal("signed-out view retained account-only history")
	}
}

func TestProductRelativeTimeMatchesWebPresentation(t *testing.T) {
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	for elapsed, want := range map[time.Duration]string{0: "just now", 2 * time.Minute: "2 minutes ago", time.Hour: "an hour ago", 24 * time.Hour: "yesterday", 60 * 24 * time.Hour: "2 months ago"} {
		if got := productPlayedAgo(now.Add(-elapsed), now); got != want {
			t.Fatalf("%v: %s", elapsed, got)
		}
	}
	if productPlayedAgo(time.Time{}, now) != "never" {
		t.Fatal("missing play time")
	}
}

func TestProductSidebarBreakpointAndContentCap(t *testing.T) {
	m, _ := newProductFixture(t)
	m = drain(t, m, m.Init())
	m, _ = step(t, m, tea.WindowSizeMsg{Width: 119, Height: 30})
	first := strings.Split(ansi.Strip(view(m)), "\n")[0]
	if strings.Contains(first, "│") {
		t.Fatal("sidebar appeared below breakpoint")
	}
	for _, width := range []int{120, 200} {
		m, _ = step(t, m, tea.WindowSizeMsg{Width: width, Height: 30})
		first = strings.Split(ansi.Strip(view(m)), "\n")[0]
		if []rune(first)[21] != '│' {
			t.Fatal("sidebar is not 22 columns")
		}
		at := strings.Index(first, "MARKETPLACE")
		// The sidebar border is multibyte Unicode: compare display cells, not bytes.
		if width == 200 && (at < 0 || lipgloss.Width(first[:at]) != 61) {
			t.Fatal("content is not centered in its 100-column cap", first)
		}
	}
	m, _ = step(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
	m.app.focus = 0
	first = strings.Split(ansi.Strip(view(m)), "\n")[0]
	if !strings.HasPrefix(first, "NAVIGATION") || []rune(first)[21] != '│' {
		t.Fatal("compact navigation is not an overlay")
	}
}

func TestProductPausePixelSelectionAppliesOnRestart(t *testing.T) {
	m, _ := newProductFixture(t)
	m = drain(t, m, m.Init())
	m, _ = m.activateProduct("play", "acme/local")
	m, _ = step(t, m, productKey("esc"))
	m, _ = step(t, m, productKey("down"))
	m, _ = step(t, m, productKey("down"))
	m, cmd := step(t, m, productKey("enter"))
	if cmd != nil {
		cmd()
	}
	if m.shape.Name != sdk.Sextant.Name || m.canvas.Shape().Name != sdk.Quadrant.Name {
		t.Fatal("pixel selector changed an active framebuffer")
	}
	m, _ = step(t, m, productKey("up"))
	m, _ = step(t, m, productKey("enter"))
	if m.screen != screenPlaying || m.canvas.Shape().Name != sdk.Sextant.Name {
		t.Fatal("restart did not apply pixel selection")
	}
	m = m.closeProductGame()
}

func TestProductFormsConsumeTypingAndRequireConfirmation(t *testing.T) {
	m, f := newProductFixture(t)
	m = drain(t, m, m.Init())
	f.calls = nil
	m, _ = m.activateProduct("confirm-account", "")
	m, _ = step(t, m, productKey("pqr"))
	m, _ = step(t, m, tea.PasteMsg{Content: "\x1b[31mtext\n"})
	if m.app.form == nil || !strings.HasPrefix(m.app.form.Values[0], "pqr") || len(f.calls) != 0 {
		t.Fatal("typing triggered an action")
	}
	m, _ = step(t, m, productKey("tab"))
	m, cmd := step(t, m, productKey("enter"))
	if cmd != nil || !strings.Contains(m.app.notice, "Confirmation") {
		t.Fatal("destructive action accepted wrong confirmation")
	}
	m, _ = step(t, m, productKey("esc"))
	if m.app.form != nil {
		t.Fatal("Escape did not dismiss form")
	}
}

func TestProductLayoutsRemainBoundedAndResizeKeepsFocus(t *testing.T) {
	m, f := newProductFixture(t)
	for i := 0; i < 80; i++ {
		f.snapshot.Catalog = append(f.snapshot.Catalog, registry.Game{ID: fmt.Sprintf("acme/game-%d", i), Name: strings.Repeat("界", 40), Description: strings.Repeat("long description ", 20)})
	}
	m = drain(t, m, m.Init())
	m.app.selection = 50
	m.app.focus = 2
	m.app.action = 1
	for _, size := range [][2]int{{80, 24}, {119, 30}, {120, 30}, {121, 30}, {180, 50}, {80, 24}, {10, 4}} {
		m, _ = step(t, m, tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		lines := strings.Split(view(m), "\n")
		if len(lines) > size[1] {
			t.Fatalf("height %d > %d", len(lines), size[1])
		}
		for _, line := range lines {
			if lipgloss.Width(line) > size[0] {
				t.Fatalf("width %d > %d", lipgloss.Width(line), size[0])
			}
		}
		if m.app.selection != 50 || m.app.focus != 2 {
			t.Fatal("resize lost focus")
		}
	}
}

func TestProductFailedRequestsRemainReadableAndCancelable(t *testing.T) {
	m, f := newProductFixture(t)
	m = drain(t, m, m.Init())
	f.failure = errors.New("offline")
	m, cmd := m.activateProduct("add", "acme/local")
	if !m.app.loading {
		t.Fatal("no pending state")
	}
	m = drain(t, m, cmd)
	if m.app.loading || m.app.notice != "offline" || m.inAccount("acme/local") {
		t.Fatal("failed mutation presented as success")
	}
	m, _ = m.openProduct(productLocation{Page: "settings"}, true)
	ctx := m.app.ctx
	m, _ = step(t, m, productKey("esc"))
	if ctx.Err() == nil {
		t.Fatal("Escape did not cancel operation")
	}
}

func TestProductGameVersionSurvivesCatalogReordering(t *testing.T) {
	m, f := newProductFixture(t)
	m = drain(t, m, m.Init())
	m, _ = m.activateProduct("play", "acme/local")
	m.games = nil // discovery changes cannot change an active run's version
	f.game.endAfter = 1
	m, _ = tickNow(t, m)
	if m.screen != screenGameOver {
		t.Fatal("game did not end")
	}
	pending := m.scores.Pending()
	if len(pending) != 1 || pending[0].Version != "1.0.0" {
		t.Fatal("run version changed")
	}
	m = m.closeProductGame()
}
