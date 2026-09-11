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
		case "catalog-search":
			var games []registry.Game
			for _, game := range f.snapshot.Catalog {
				if matchesProductSearch(game, r.Value) {
					games = append(games, game)
				}
			}
			return ProductReply{Catalog: games}, nil
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
		case "like", "unlike":
			liked := r.Kind == "like"
			for i, game := range f.snapshot.Catalog {
				if game.ID == r.ID {
					if liked && !game.Liked {
						game.Likes++
					}
					if !liked && game.Liked && game.Likes > 0 {
						game.Likes--
					}
					game.Liked = liked
					f.snapshot.Catalog[i] = game
				}
			}
			for i, game := range f.snapshot.Library {
				if game.ID == r.ID {
					game.Liked = liked
					game.Likes = 0
					for _, catalog := range f.snapshot.Catalog {
						if catalog.ID == r.ID {
							game.Likes = catalog.Likes
							break
						}
					}
					f.snapshot.Library[i] = game
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
	if name == "ctrl+n" {
		return tea.KeyPressMsg{Code: 'n', Mod: tea.ModCtrl}
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

func TestProductCtrlNMatchesEvenWhenStringHidesTheModifier(t *testing.T) {
	msg := tea.KeyPressMsg{Code: 'n', Mod: tea.ModCtrl, Text: "n"}
	if !productNavToggle(msg) {
		t.Fatal("Ctrl+N with letter text was not recognized as the navigation toggle")
	}
	m, _ := newProductFixture(t)
	m = drain(t, m, m.Init())
	m.app.focus = 1
	m, _ = step(t, m, msg)
	if m.app.focus != 0 {
		t.Fatal("Ctrl+N with hidden modifier did not open navigation")
	}
}

func TestProductCtrlNTogglesNavigationWithoutFooterStop(t *testing.T) {
	for _, width := range []int{80, 119, 120, 200} {
		for _, initialFocus := range []int{0, 1, 2} {
			t.Run(fmt.Sprintf("width-%d-focus-%d", width, initialFocus), func(t *testing.T) {
				m, fixture := newProductFixture(t)
				m = drain(t, m, m.Init())
				m, _ = step(t, m, tea.WindowSizeMsg{Width: width, Height: 24})
				m.app.focus, m.app.selection, m.app.action = initialFocus, 1, 2
				location, generation, calls := m.app.loc, m.app.gen, len(fixture.calls)
				for press := 0; press < 6; press++ {
					want := 0
					if m.app.focus == 0 {
						want = 1
					}
					var cmd tea.Cmd
					m, cmd = step(t, m, productKey("ctrl+n"))
					if m.app.focus != want {
						t.Fatalf("press %d: focus = %d, want %d", press+1, m.app.focus, want)
					}
					if cmd != nil || m.app.loc != location || m.app.selection != 1 || m.app.gen != generation || len(fixture.calls) != calls {
						t.Fatal("navigation toggle changed page, selection, or service state")
					}
					if width < sidebarBreakpoint && strings.Contains(ansi.Strip(view(m)), "TERMCADE") != (want == 0) {
						t.Fatal("compact navigation did not open/close with the toggle")
					}
				}
			})
		}
	}
}

func TestProductShiftTabPreservesFormAndGameplayControls(t *testing.T) {
	m, _ := newProductFixture(t)
	m = drain(t, m, m.Init())
	m, _ = m.activateProduct("form-org", "")
	m.app.form.Index = 1
	m, _ = step(t, m, productKey("shift+tab"))
	if m.app.form == nil || m.app.form.Index != 0 || m.app.focus != 1 {
		t.Fatal("Shift+Tab no longer traverses form controls")
	}
	m, _ = step(t, m, productKey("ctrl+n"))
	if m.app.form == nil || m.app.focus != 0 {
		t.Fatal("Ctrl+N did not open navigation from a form")
	}
	m, _ = step(t, m, productKey("esc"))
	m, _ = m.activateProduct("play", "acme/local")
	m, _ = step(t, m, productKey("shift+tab"))
	if m.screen != screenPlaying || m.app.gameNav {
		t.Fatal("navigation interrupted live gameplay")
	}
	m, _ = step(t, m, productKey("ctrl+n"))
	if m.screen != screenPlaying || m.app.gameNav {
		t.Fatal("Ctrl+N interrupted live gameplay")
	}
	m, _ = step(t, m, productKey("esc"))
	for _, want := range []bool{true, false, true, false} {
		m, _ = step(t, m, productKey("ctrl+n"))
		if m.app.gameNav != want || m.screen != screenPaused {
			t.Fatal("paused navigation did not toggle directly")
		}
	}
	m = m.closeProductGame()
}

func TestProductPlainTabNeverOpensOrClosesNavigation(t *testing.T) {
	for _, width := range []int{80, 120, 200} {
		for _, focus := range []int{0, 1, 2} {
			t.Run(fmt.Sprintf("width-%d-focus-%d", width, focus), func(t *testing.T) {
				m, fixture := newProductFixture(t)
				m = drain(t, m, m.Init())
				m, _ = step(t, m, tea.WindowSizeMsg{Width: width, Height: 24})
				m.app.focus = focus
				open, calls := focus == 0, len(fixture.calls)
				for press := 0; press < 6; press++ {
					var cmd tea.Cmd
					key := "tab"
					if press%2 == 1 {
						key = "shift+tab"
					}
					m, cmd = step(t, m, productKey(key))
					if (m.app.focus == 0) != open || cmd != nil || len(fixture.calls) != calls {
						t.Fatal("Tab/Shift+Tab changed navigation or invoked an action")
					}
				}
			})
		}
	}
}

func TestProductTabAndShiftTabStepNextAndPrevious(t *testing.T) {
	m, _ := newProductFixture(t)
	m = drain(t, m, m.Init())
	if len(m.productItems()) < 2 {
		t.Fatal("need at least two marketplace rows")
	}
	m.app.focus, m.app.selection = 1, 0
	m, _ = step(t, m, productKey("tab"))
	if m.app.focus != 1 || m.app.selection != 1 {
		t.Fatal("Tab did not move to the next content row")
	}
	m, _ = step(t, m, productKey("shift+tab"))
	if m.app.focus != 1 || m.app.selection != 0 {
		t.Fatal("Shift+Tab did not move to the previous content row")
	}
	m.app.focus, m.app.action = 2, 0
	m, _ = step(t, m, productKey("tab"))
	if m.app.focus != 2 || m.app.action != 1 {
		t.Fatal("Tab did not move to the next action")
	}
	m, _ = step(t, m, productKey("shift+tab"))
	if m.app.focus != 2 || m.app.action != 0 {
		t.Fatal("Shift+Tab did not move to the previous action")
	}
	m.app.focus, m.app.nav = 0, 1
	m, _ = step(t, m, productKey("tab"))
	if m.app.focus != 0 || m.app.nav != 2 {
		t.Fatal("Tab did not move to the next navigation item")
	}
	m, _ = step(t, m, productKey("shift+tab"))
	if m.app.focus != 0 || m.app.nav != 1 {
		t.Fatal("Shift+Tab did not move to the previous navigation item")
	}
	m, _ = m.activateProduct("play", "acme/local")
	m, _ = step(t, m, productKey("esc"))
	m, _ = step(t, m, productKey("tab"))
	if m.screen != screenPaused || m.app.gameNav {
		t.Fatal("Tab left pause")
	}
	m = m.closeProductGame()
}

func TestProductMarketplaceKeysFireActions(t *testing.T) {
	m, f := newProductFixture(t)
	m = drain(t, m, m.Init())
	if !strings.Contains(ansi.Strip(view(m)), "↻ R Refresh") || strings.Contains(ansi.Strip(view(m)), "[Refresh]") {
		t.Fatal("marketplace actions are not keyed in the shared footer")
	}
	f.calls = nil
	m.app.focus = 0
	m, cmd := step(t, m, productKey("r"))
	if cmd != nil || len(f.calls) != 0 {
		t.Fatal("action keys leaked into navigation")
	}
	m.app.focus = 1
	m, cmd = step(t, m, productKey("r"))
	m = drain(t, m, cmd)
	if len(f.calls) == 0 || f.calls[len(f.calls)-1].Kind != "load" {
		t.Fatalf("R did not refresh: %#v", f.calls)
	}
	for i, item := range m.productItems() {
		if item.ID == "acme/remote" {
			m.app.selection = i
			break
		}
	}
	f.calls = nil
	m, cmd = step(t, m, productKey("p"))
	m = drain(t, m, cmd)
	if len(f.calls) == 0 || f.calls[len(f.calls)-1].Kind != "install" {
		t.Fatalf("P did not play/install: %#v", f.calls)
	}
	m = m.closeProductGame()
	m, cmd = m.openProduct(productLocation{Page: "marketplace"}, false)
	m = drain(t, m, cmd)
	for i, item := range m.productItems() {
		if item.ID == "acme/unreleased" {
			m.app.selection = i
			break
		}
	}
	f.calls = nil
	m, cmd = step(t, m, productKey("p"))
	if cmd != nil || len(f.calls) != 0 {
		t.Fatal("P played a game that cannot run")
	}
	for i, item := range m.productItems() {
		if item.ID == "acme/local" {
			m.app.selection = i
			break
		}
	}
	m, _ = step(t, m, productKey("u"))
	if m.app.form == nil || m.app.form.Kind != "uninstall" {
		t.Fatal("U did not open uninstall confirmation")
	}
}

func TestProductPlainTabDoesNotToggleGameNavigation(t *testing.T) {
	for _, state := range []screen{screenPlaying, screenPaused, screenGameOver, screenCrashed} {
		for _, open := range []bool{false, true} {
			if state == screenPlaying && open {
				continue
			}
			t.Run(fmt.Sprintf("state-%d-open-%t", state, open), func(t *testing.T) {
				m, _ := newProductFixture(t)
				m = drain(t, m, m.Init())
				m, _ = m.activateProduct("play", "acme/local")
				m.screen, m.app.gameNav = state, open
				for press := 0; press < 4; press++ {
					m, _ = step(t, m, productKey("tab"))
					if m.app.gameNav != open || m.screen != state {
						t.Fatal("plain Tab changed game navigation or its state")
					}
				}
				m = m.closeProductGame()
			})
		}
	}
}

func TestProductStartsOnMarketplaceAndDefersServiceWork(t *testing.T) {
	m, f := newProductFixture(t)
	if m.screen != screenProduct || m.app.loc.Page != "marketplace" || len(f.calls) != 0 {
		t.Fatal("startup contract")
	}
	m = drain(t, m, m.Init())
	if len(f.calls) != 1 || f.calls[0].Kind != "load" || len(m.productItems()) != 5 {
		t.Fatalf("startup load: %#v", f.calls)
	}
	if !strings.Contains(view(m), "MARKETPLACE") {
		t.Fatal("no marketplace")
	}
}

func TestProductFooterDividerStaysAboveInstructionsWithinHeight(t *testing.T) {
	for _, width := range []int{80, 98, 100} {
		for _, notice := range []string{"", "Request completed"} {
			t.Run(fmt.Sprintf("width-%d-notice-%t", width, notice != ""), func(t *testing.T) {
				m, _ := newProductFixture(t)
				m = drain(t, m, m.Init())
				m.app.notice = notice
				lines := strings.Split(ansi.Strip(m.productPageView(width, 24)), "\n")
				if len(lines) > 24 {
					t.Fatal("footer exceeded the terminal height")
				}
				divider, actions := -1, -1
				for i, line := range lines {
					if line == strings.Repeat("─", width) {
						divider = i
					}
					if strings.Contains(line, "Esc Back") {
						actions = i
					}
					if lipgloss.Width(line) > width {
						t.Fatal("footer exceeded the content width")
					}
				}
				if divider < 0 || actions <= divider {
					t.Fatalf("missing or misplaced footer: divider=%d actions=%d", divider, actions)
				}
			})
		}
	}
}

func assertProductBottomFooter(t *testing.T, rendered string, width, height int, controls ...string) {
	t.Helper()
	lines := strings.Split(ansi.Strip(rendered), "\n")
	if len(lines) != height {
		t.Fatalf("rendered %d rows, want %d", len(lines), height)
	}
	divider := -1
	for i, line := range lines {
		if lipgloss.Width(line) > width {
			t.Fatalf("line exceeds %d columns", width)
		}
		if line == strings.Repeat("─", width) {
			divider = i
		}
	}
	if divider < 0 || divider == len(lines)-1 {
		t.Fatal("missing bottom divider or footer")
	}
	if strings.TrimSpace(lines[len(lines)-1]) == "" {
		t.Fatal("controls are not anchored to the bottom")
	}
	above, below := strings.Join(lines[:divider], "\n"), strings.Join(lines[divider+1:], "\n")
	for _, control := range controls {
		if strings.Contains(above, control) || !strings.Contains(below, control) {
			t.Fatalf("control %q is not exclusively below the divider", control)
		}
	}
}

func TestEveryProductPageUsesTheBottomFooter(t *testing.T) {
	for _, page := range []string{"marketplace", "game", "owner", "chart", "settings", "members", "sessions", "pairing", "docs"} {
		t.Run(page, func(t *testing.T) {
			m, fixture := newProductFixture(t)
			m = drain(t, m, m.Init())
			m.app.loc = productLocation{Page: page, ID: "team"}
			m.app.game = fixture.snapshot.Catalog[0]
			m.app.owner = registry.HandleOwner{Name: "team"}
			m.app.pairingURL = "http://127.0.0.1:8081/pair"
			action := "↻ R Refresh"
			if page == "pairing" {
				action = "↗ B Open"
			}
			for _, width := range []int{80, 100} {
				assertProductBottomFooter(t, m.productPageView(width, 24), width, 24, action)
			}
		})
	}
}

func TestFormsAndNavigationKeepControlsBelowTheDivider(t *testing.T) {
	m, _ := newProductFixture(t)
	m = drain(t, m, m.Init())
	for _, kind := range []string{"form-org", "form-username", "confirm-account"} {
		m, _ = m.activateProduct(kind, "")
		assertProductBottomFooter(t, m.productFormView(80, 24), 80, 24, "Enter confirm", "Esc cancel")
		m.app.form = nil
	}
	nav := ansi.Strip(m.productNavView(sidebarWidth, 24))
	if strings.Contains(nav, "↓ Tab") {
		t.Fatal("navigation still draws a key legend")
	}
	assertNavLibraryDivider(t, nav)
}

func TestProductNavigationPinsIdentityBelowTheList(t *testing.T) {
	for _, signedIn := range []bool{false, true} {
		for _, count := range []int{0, 40} {
			t.Run(fmt.Sprintf("signed-in=%v/games=%d", signedIn, count), func(t *testing.T) {
				m, f := newProductFixture(t)
				f.snapshot.SignedIn = signedIn
				f.snapshot.Library = nil
				if signedIn {
					for i := 0; i < count; i++ {
						f.snapshot.Library = append(f.snapshot.Library, registry.Game{ID: fmt.Sprintf("acme/game-%02d", i), Name: fmt.Sprintf("Game %02d", i)})
					}
				}
				m = drain(t, m, m.Init())
				m.app.focus = 0
				items := m.productNavItems()
				identity := "Sign in"
				if signedIn {
					identity = "@dev"
				}
				for _, height := range []int{12, 24, 40} {
					for _, selection := range []int{0, len(items) - 2, len(items) - 1} {
						m.app.nav = selection
						rendered := m.productNavView(sidebarWidth, height)
						lines := strings.Split(ansi.Strip(rendered), "\n")
						if len(lines) != height {
							t.Fatalf("rendered %d rows, want %d", len(lines), height)
						}
						if strings.Contains(ansi.Strip(rendered), "↓ Tab") {
							t.Fatal("navigation still draws a key legend")
						}
						assertNavLibraryDivider(t, rendered)
						if !strings.Contains(lines[height-1], identity) || strings.Count(ansi.Strip(rendered), identity) != 1 {
							t.Fatal("identity did not remain in its single reserved bottom row")
						}
						if selection < len(items)-1 && !strings.Contains(ansi.Strip(rendered), strings.TrimSpace(items[selection].Label)) {
							t.Fatal("scrolling hid the selected navigation item")
						}
						if selection == len(items)-1 && !strings.Contains(lines[height-1], "▸ "+identity) {
							t.Fatal("pinned identity lost its focus indicator")
						}
					}
				}
				m.app.nav = 0
				m, _ = step(t, m, productKey("up"))
				if m.app.nav != len(items)-1 {
					t.Fatal("up did not wrap to pinned identity")
				}
				m, cmd := step(t, m, productKey("enter"))
				m = drain(t, m, cmd)
				if signedIn && m.app.loc.Page != "settings" {
					t.Fatal("identity activation changed")
				}
				if !signedIn && m.app.loc.Page != "marketplace" {
					t.Fatal("sign-in dialog navigated away from its background")
				}
				if !signedIn {
					found := false
					for _, item := range items {
						if item.Kind == "game" && item.ID == "acme/local" {
							found = true
						}
					}
					if !found || m.localIndex("acme/local") < 0 {
						t.Fatal("signed-out offline installation was removed from navigation")
					}
				}
			})
		}
	}
}

type footerTestGame struct{ fakeGame }

func (g *footerTestGame) Info() sdk.Info {
	return sdk.Info{ID: "acme/footer", Title: "TETRIS", PixelW: 32, PixelH: 40}
}
func (g *footerTestGame) HUD() sdk.HUD {
	return sdk.HUD{Fields: []sdk.HUDField{{Label: "SCORE", Value: "000000", Accent: true}, {Label: "LEVEL", Value: "1"}, {Label: "LINES", Value: "0"}}, Hint: "←/→ move · ↑ rotate · ↓ soft · space drop"}
}

func TestProductPlaySitsBelowTheGameHero(t *testing.T) {
	m, _ := newProductFixture(t)
	m = drain(t, m, m.Init())
	m.app.days = []registry.PlayDay{{Day: "2026-01-01", Count: 1}}
	m, _ = m.activateProduct("play", "acme/local")
	page := ansi.Strip(m.View().Content)
	if m.app.loc.Page != "game" || m.screen != screenPlaying {
		t.Fatal("play did not stay on the game page")
	}
	if !strings.Contains(page, "@acme / local") {
		t.Fatalf("playing view lost the hero:\n%s", page)
	}
	nameAt, scoreAt := -1, -1
	for i, line := range strings.Split(page, "\n") {
		if nameAt < 0 && strings.Contains(line, "@acme / local") {
			nameAt = i
		}
		if strings.Contains(line, "SCORE") {
			scoreAt = i
		}
	}
	if nameAt < 0 || scoreAt <= nameAt {
		t.Fatalf("playfield is not below the hero: name=%d score=%d\n%s", nameAt, scoreAt, page)
	}
	m = m.closeProductGame()
}

func TestGameplayStatesKeepBottomControlsAndFitAn80By24Terminal(t *testing.T) {
	m, _ := newProductFixture(t)
	m = drain(t, m, m.Init())
	g := &footerTestGame{}
	m.games = []engine.Registration{{Info: g.Info(), New: func(sdk.CellShape) (sdk.Game, error) { return g, nil }}}
	m.shape = sdk.Sextant // longest pixel-mode label in the pause controls
	m, _ = m.activateProduct("play", g.Info().ID)
	for _, state := range []struct {
		screen   screen
		controls []string
	}{
		{screenPlaying, []string{"space drop", "Esc pause", "Ctrl+C quit"}},
		{screenPaused, []string{"Enter select", "Esc resume"}},
		{screenGameOver, []string{"Enter play again", "Esc Marketplace"}},
		{screenCrashed, []string{"Any key Marketplace"}},
	} {
		candidate := m
		candidate.screen, candidate.crash = state.screen, "fixture failure"
		rendered := candidate.productGameView(80, 24)
		if strings.Contains(rendered, "Terminal too small") {
			t.Fatalf("state %v no longer fits the standard playfield", state.screen)
		}
		assertProductBottomFooter(t, rendered, 80, 24, state.controls...)
	}
	tooSmallView := m.productGameView(80, 23)
	if !strings.Contains(tooSmallView, "Terminal too small") {
		t.Fatal("insufficient room should not crop the game")
	}
	assertProductBottomFooter(t, tooSmallView, 80, 23, "space drop", "Esc pause")
	assertProductBottomFooter(t, (Model{}).productGameView(80, 24), 80, 24, "✕ Esc Back")
	m = m.closeProductGame()
}

func TestProductGamePageMatchesWebHero(t *testing.T) {
	m, _ := newProductFixture(t)
	m = drain(t, m, m.Init())
	m.app.loc = productLocation{Page: "game", ID: "acme/local"}
	m.app.game = m.metadata("acme/local")
	m.app.game.Description = "A local toy"
	m.app.game.Repo = "https://example.test/local"
	m.app.days = []registry.PlayDay{{Day: "2026-01-01", Count: 2}, {Day: "2026-01-08", Count: 4}}
	page := ansi.Strip(m.productPageView(80, 24))
	if strings.Contains(page, "GAME /") || strings.Count(page, "@acme") != 1 {
		t.Fatalf("username should appear once as the name, not in the page title:\n%s", page)
	}
	if strings.Contains(page, "▸") || strings.Contains(page, "Public play activity") {
		t.Fatalf("game page is still a stacked list:\n%s", page)
	}
	if !strings.Contains(page, "@acme / local") || !strings.Contains(page, "https://example.test/local") || !strings.Contains(page, "A local toy") {
		t.Fatalf("left column missing name/url/desc:\n%s", page)
	}
	sideBySide := false
	for _, line := range strings.Split(page, "\n") {
		hasLeft := strings.Contains(line, "@acme") || strings.Contains(line, "example.test") || strings.Contains(line, "A local toy")
		hasChart := strings.Contains(line, "PLAYS") || strings.Contains(line, "■")
		if hasLeft && hasChart {
			sideBySide = true
			break
		}
	}
	if !sideBySide {
		t.Fatalf("chart is not to the right of name/url/desc:\n%s", page)
	}
	floated, centered := false, false
	for _, line := range strings.Split(page, "\n") {
		stripped := strings.TrimRight(ansi.Strip(line), " ")
		if strings.Contains(stripped, "PLAYS") && strings.Contains(stripped, "More") {
			floated = strings.HasSuffix(stripped, "More") && strings.Index(stripped, "PLAYS") < strings.Index(stripped, "Less")
		}
		if at := strings.Index(ansi.Strip(line), "@acme / local"); at >= 4 {
			centered = true
		}
	}
	if !floated || !centered {
		t.Fatalf("legend should float right and name/url/desc should be centered:\n%s", page)
	}
	var owner, repo, activity bool
	for _, a := range m.productActions() {
		owner = owner || (a.Kind == "owner" && a.ID == "acme")
		repo = repo || (a.Kind == "open-url" && a.ID == m.app.game.Repo)
		activity = activity || (a.Kind == "chart" && a.ID == "acme/local")
	}
	if !owner || !repo || !activity {
		t.Fatalf("game page lost owner/repo/activity actions: %#v", m.productActions())
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
	m, _ = step(t, m, productKey("ctrl+n"))
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
	if !strings.Contains(view(m), "Enter confirm") {
		t.Fatal("long field hid submission controls")
	}
}

func TestProductHeatmapPreservesZeroDaysAndIntensity(t *testing.T) {
	days := []registry.PlayDay{{Day: "2024-02-25", Count: 0}, {Day: "2024-02-26", Count: 1}, {Day: "2024-02-27", Count: 2}, {Day: "2024-02-28", Count: 3}, {Day: "2024-02-29", Count: 4}}
	heatmap := playHeatmap(days)
	if len(strings.Split(heatmap, "\n")) != 7 {
		t.Fatal("week rows missing")
	}
	if strings.ContainsAny(ansi.Strip(heatmap), "░▁▃▆█") || !strings.Contains(heatmap, "■") {
		t.Fatal("heatmap should use colored squares, not bar glyphs")
	}
	for _, count := range []int{0, 1, 2, 3, 4} {
		if playLevel(count, 4) != count {
			t.Fatal("missing activity level", count)
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

func assertNavLibraryDivider(t *testing.T, nav string) {
	t.Helper()
	lines := strings.Split(ansi.Strip(nav), "\n")
	for i, line := range lines {
		if !strings.Contains(line, "Library") || strings.Contains(line, "login") || strings.Contains(line, "No games") {
			continue
		}
		if i == 0 || strings.Trim(strings.TrimSpace(lines[i-1]), "─") != "" {
			t.Fatalf("Library is not preceded by a divider:\n%s", nav)
		}
		return
	}
}

func TestProductNavListsLibraryGamesOnce(t *testing.T) {
	m, _ := newProductFixture(t)
	m = drain(t, m, m.Init())
	seen := map[string]int{}
	pages := 0
	for _, item := range m.productNavItems() {
		if item.Kind == "page" && item.ID == "library" {
			t.Fatal("library is still a navigation page")
		}
		if item.Kind == "game" {
			seen[item.ID]++
		}
		if item.Label == "Library" {
			pages++
		}
	}
	if pages != 1 || seen["acme/remote"] != 1 || seen["acme/local"] != 1 {
		t.Fatalf("nav library games = %v heading=%d", seen, pages)
	}
}

func TestProductNavLibraryEmptyStates(t *testing.T) {
	m, _ := newProductFixture(t)
	m = drain(t, m, m.Init())
	m.games = nil
	m.app.snapshot.Library = nil
	m.app.snapshot.SignedIn = false
	labels := func() []string {
		var out []string
		for _, item := range m.productNavItems() {
			out = append(out, item.Label)
		}
		return out
	}
	got := strings.Join(labels(), "\n")
	if !strings.Contains(got, "login to access your library") || strings.Contains(got, "No games added") {
		t.Fatalf("signed-out empty library:\n%s", got)
	}
	m.app.snapshot.SignedIn = true
	m.app.snapshot.LibraryKnown = true
	got = strings.Join(labels(), "\n")
	if !strings.Contains(got, "No games added") || strings.Contains(got, "login to access your library") {
		t.Fatalf("signed-in empty library:\n%s", got)
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
	m, _ = step(t, m, productKey("ctrl+n"))
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
	if !strings.HasPrefix(first, "TERMCADE") || []rune(first)[21] != '│' {
		t.Fatal("compact navigation is not an overlay")
	}
}

func TestProductNavigationLeavesMainFooterUncovered(t *testing.T) {
	for _, width := range []int{80, 120} {
		t.Run(fmt.Sprintf("width-%d", width), func(t *testing.T) {
			m, _ := newProductFixture(t)
			m = drain(t, m, m.Init())
			m, _ = step(t, m, tea.WindowSizeMsg{Width: width, Height: 24})
			closed := strings.Split(ansi.Strip(view(m)), "\n")
			cut := productFooterRow(closed)
			if cut >= len(closed) || strings.TrimSpace(closed[cut]) != strings.Repeat("─", width) || !strings.Contains(strings.Join(closed[cut:], "\n"), "↻ R Refresh") {
				t.Fatal("control: closed view missing main footer")
			}
			m, _ = step(t, m, productKey("ctrl+n"))
			opened := strings.Split(ansi.Strip(view(m)), "\n")
			openCut := productFooterRow(opened)
			if openCut >= len(opened) || !strings.Contains(strings.Join(opened[openCut:], "\n"), "↻ R Refresh") {
				t.Fatal("open navigation missing the shared footer")
			}
			for i := openCut; i < len(opened); i++ {
				if strings.Contains(opened[i], "TERMCADE") || (len([]rune(opened[i])) > sidebarWidth-1 && []rune(opened[i])[sidebarWidth-1] == '│' && strings.Count(opened[i], "─") >= 8) {
					t.Fatalf("navigation covered main footer row %d: %q", i, opened[i])
				}
			}
		})
	}
}

func TestProductNavigationOverlaysSmallAndPushesLargeOnlyWhenOpen(t *testing.T) {
	for _, width := range []int{80, 119, 120, 200} {
		t.Run(fmt.Sprintf("width-%d", width), func(t *testing.T) {
			m, _ := newProductFixture(t)
			m = drain(t, m, m.Init())
			m, _ = step(t, m, tea.WindowSizeMsg{Width: width, Height: 24})
			closed := ansi.Strip(view(m))
			closedLines := strings.Split(closed, "\n")
			cut := productFooterRow(closedLines)
			if cut >= len(closedLines) || strings.TrimSpace(closedLines[cut]) != strings.Repeat("─", width) {
				t.Fatal("closed footer divider is not full terminal width")
			}
			if width < sidebarBreakpoint && strings.Contains(closedLines[0], "│") {
				t.Fatal("closed navigation still reserves content width")
			}
			m, _ = step(t, m, productKey("ctrl+n"))
			opened := strings.Split(ansi.Strip(view(m)), "\n")
			if width < sidebarBreakpoint {
				background := strings.Split(closed, "\n")
				limit := min(productFooterRow(opened), productFooterRow(background))
				for i := 0; i < limit; i++ {
					if ansi.Cut(opened[i], sidebarWidth, width) != ansi.Cut(background[i], sidebarWidth, width) {
						t.Fatalf("overlay reflowed content on row %d", i)
					}
				}
				if !strings.HasPrefix(opened[0], "TERMCADE") {
					t.Fatal("small navigation is not an overlay")
				}
			} else {
				if []rune(opened[0])[sidebarWidth-1] != '│' {
					t.Fatal("large navigation did not reserve sidebar width")
				}
				at := strings.Index(opened[0], "MARKETPLACE")
				wantColumn := sidebarWidth + (width-sidebarWidth-min(width-sidebarWidth, productContentMax))/2
				if at < 0 || lipgloss.Width(opened[0][:at]) != wantColumn {
					t.Fatal("large navigation did not push the content")
				}
			}
			m, _ = step(t, m, productKey("ctrl+n"))
			if ansi.Strip(view(m)) != closed {
				t.Fatal("closing navigation did not restore the full content area")
			}
		})
	}
}

type wideNavigationGame struct{ fakeGame }

func (g *wideNavigationGame) Info() sdk.Info {
	return sdk.Info{ID: "acme/wide", Title: "WIDE", PixelW: 110, PixelH: 8}
}

func TestProductOpenNavigationAdaptsDuringResizeWithoutSqueezingGames(t *testing.T) {
	m, _ := newProductFixture(t)
	m = drain(t, m, m.Init())
	g := &wideNavigationGame{}
	m.games = []engine.Registration{{Info: g.Info(), New: func(sdk.CellShape) (sdk.Game, error) { return g, nil }}}
	m, _ = step(t, m, tea.WindowSizeMsg{Width: 120, Height: 24})
	m, _ = m.activateProduct("play", g.Info().ID)
	m, _ = step(t, m, productKey("esc"))
	frame := m.frame
	m, _ = step(t, m, productKey("ctrl+n"))
	for _, width := range []int{120, 200, 120} {
		m, _ = step(t, m, tea.WindowSizeMsg{Width: width, Height: 24})
		rendered := ansi.Strip(view(m))
		if !m.app.gameNav || m.frame != frame || strings.Contains(rendered, "Terminal too small") {
			t.Fatal("resize lost navigation or squeezed the game")
		}
		pushed := false
		for _, line := range strings.Split(rendered, "\n") {
			if strings.Contains(ansi.Cut(line, sidebarWidth, width), "SCORE") {
				pushed = true
				break
			}
		}
		wantPush := width-sidebarWidth >= g.Info().PixelW+2
		if pushed != wantPush {
			t.Fatalf("wrong navigation mode at width %d", width)
		}
	}
	m = m.closeProductGame()
}

func TestProductPauseDialogResumesAndExits(t *testing.T) {
	m, _ := newProductFixture(t)
	m = drain(t, m, m.Init())
	m, _ = m.activateProduct("play", "acme/local")
	m, _ = step(t, m, productKey("esc"))
	if m.screen != screenPaused {
		t.Fatal("esc did not pause")
	}
	if !strings.Contains(ansi.Strip(view(m)), "Paused") || !strings.Contains(ansi.Strip(view(m)), "[Resume]") {
		t.Fatal("pause dialog missing")
	}
	m, _ = step(t, m, productKey("enter"))
	if m.screen != screenPlaying {
		t.Fatal("resume did not continue the game")
	}
	m, _ = step(t, m, productKey("esc"))
	m, _ = step(t, m, productKey("tab"))
	m, cmd := step(t, m, productKey("enter"))
	m = drain(t, m, cmd)
	if m.screen != screenProduct || m.app.loc.Page != "marketplace" {
		t.Fatal("exit did not leave the game")
	}
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

func TestProductGameRowsShowIDDescriptionAndLikes(t *testing.T) {
	m, f := newProductFixture(t)
	f.snapshot.Catalog[0].Description = "A local toy"
	f.snapshot.Catalog[0].Likes = 4
	m = drain(t, m, m.Init())
	item := m.productItems()[1]
	if item.Label != "acme/local" || item.Detail != "A local toy" || item.Meta != "♡ 4" {
		t.Fatalf("game row = %+v", item)
	}
	view := ansi.Strip(m.productPageView(80, 24))
	if !strings.Contains(view, "acme/local") || !strings.Contains(view, "♡ 4") || !strings.Contains(view, "A local toy") {
		t.Fatalf("marketplace row missing id, likes, or description:\n%s", view)
	}
	m.app.focus, m.app.selection = 1, 1
	m, cmd := step(t, m, productKey("l"))
	m = drain(t, m, cmd)
	if !m.metadata("acme/local").Liked || m.metadata("acme/local").Likes != 5 {
		t.Fatalf("L did not like: %+v", m.metadata("acme/local"))
	}
	if m.productItems()[1].Meta != "♥ 5" {
		t.Fatalf("liked meta = %q", m.productItems()[1].Meta)
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
