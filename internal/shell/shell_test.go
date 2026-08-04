package shell

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/aviorstudio/termcade/internal/engine"
	"github.com/aviorstudio/termcade/internal/scores"
	"github.com/aviorstudio/termcade/internal/settings"
	"github.com/aviorstudio/termcade/sdk"
)

// fakeGame ends after a scripted number of updates.
type fakeGame struct {
	updates     int
	endAfter    int
	lastKey     sdk.Key
	lastKeyUp   sdk.Key
	resets      int
	score       int
	panicOnTick int // if >0, Update panics on that call
	closed      int
}

func (g *fakeGame) Info() sdk.Info {
	return sdk.Info{ID: "fake", Title: "FAKE", PixelW: 16, PixelH: 8}
}
func (g *fakeGame) Reset() { g.resets++; g.updates = 0 }
func (g *fakeGame) HandleKey(k sdk.Key) {
	g.lastKey = k
	g.score += 5
}
func (g *fakeGame) HandleKeyUp(k sdk.Key) { g.lastKeyUp = k }
func (g *fakeGame) Update() sdk.Status {
	g.updates++
	if g.panicOnTick > 0 && g.updates >= g.panicOnTick {
		panic("scripted crash")
	}
	if g.endAfter > 0 && g.updates >= g.endAfter {
		return sdk.StatusGameOver
	}
	return sdk.StatusRunning
}
func (g *fakeGame) Draw(c *sdk.Canvas) { c.Set(1, 1, sdk.Red) }
func (g *fakeGame) Score() int         { return g.score }
func (g *fakeGame) Close() error       { g.closed++; return nil }
func (g *fakeGame) HUD() sdk.HUD {
	return sdk.HUD{Fields: []sdk.HUDField{{Label: "fake", Value: "hud"}}}
}

func newTestShell(t *testing.T, g *fakeGame) Model {
	t.Helper()
	return newTestShellReg(t, engine.Registration{
		Info: g.Info(),
		New:  func(sdk.CellShape) (sdk.Game, error) { return g, nil },
	})
}

func newTestShellReg(t *testing.T, reg engine.Registration) Model {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	st, err := scores.Load()
	if err != nil {
		t.Fatal(err)
	}
	m := New([]engine.Registration{reg}, st, sdk.Quadrant, nil)
	mm, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	return mm.(Model)
}

func keyOf(s string) tea.Key {
	switch s {
	case "enter":
		return tea.Key{Code: tea.KeyEnter}
	case "esc":
		return tea.Key{Code: tea.KeyEscape}
	case " ":
		return tea.Key{Code: tea.KeySpace, Text: " "}
	}
	return tea.Key{Code: rune(s[0]), Text: s}
}

func key(s string) tea.KeyPressMsg { return tea.KeyPressMsg(keyOf(s)) }

func keyUp(s string) tea.KeyReleaseMsg { return tea.KeyReleaseMsg(keyOf(s)) }

// view renders the model's content, which is what the assertions inspect.
func view(m Model) string { return m.View().Content }

func step(t *testing.T, m Model, msg tea.Msg) (Model, tea.Cmd) {
	t.Helper()
	mm, cmd := m.Update(msg)
	return mm.(Model), cmd
}

// tickNow extracts the generation the model would tick with and delivers one.
func tickNow(t *testing.T, m Model) (Model, tea.Cmd) {
	t.Helper()
	return step(t, m, tickMsg{gen: m.tickGen})
}

func TestMenuToPlayingAndDraw(t *testing.T) {
	g := &fakeGame{}
	m := newTestShell(t, g)

	if !strings.Contains(view(m), "LIBRARY") {
		t.Fatal("index does not offer the library")
	}
	m, _ = step(t, m, key("l"))
	if !strings.Contains(view(m), "FAKE") {
		t.Fatal("library does not list the game")
	}
	m, cmd := step(t, m, key("enter"))
	if m.screen != screenPlaying {
		t.Fatalf("screen = %v after enter", m.screen)
	}
	if cmd == nil {
		t.Fatal("no tick scheduled on game start")
	}
	if g.resets != 1 {
		t.Fatalf("resets = %d", g.resets)
	}
	m, cmd = tickNow(t, m)
	if g.updates != 1 || cmd == nil {
		t.Fatalf("tick did not update game (updates=%d)", g.updates)
	}
	// One logical unit at quadrant density fills the lower half of its cell.
	out := view(m)
	if !strings.Contains(out, "▄") || !strings.Contains(out, "fake hud") {
		t.Error("playing view missing canvas or HUD")
	}
}

func TestGamePanicShowsCrashScreen(t *testing.T) {
	g := &fakeGame{panicOnTick: 2}
	m := newTestShell(t, g)
	m, _ = step(t, m, key("l"))
	m, _ = step(t, m, key("enter"))
	m, _ = tickNow(t, m)
	if m.screen != screenPlaying {
		t.Fatalf("screen = %v after healthy tick", m.screen)
	}
	m, _ = tickNow(t, m) // this update panics
	if m.screen != screenCrashed {
		t.Fatalf("screen = %v after panic, want crashed", m.screen)
	}
	if !strings.Contains(view(m), "GAME CRASHED") {
		t.Error("crash overlay not shown")
	}
	// A stale tick must not resurrect the dead game.
	before := g.updates
	m, _ = step(t, m, tickMsg{gen: m.tickGen - 1})
	if g.updates != before {
		t.Error("tick advanced a crashed game")
	}
	// Any key returns to the menu and the game is closed.
	m, _ = step(t, m, key(" "))
	if m.screen != screenMenu {
		t.Fatalf("screen = %v after dismissing crash", m.screen)
	}
	if g.closed != 1 {
		t.Errorf("closed = %d, want 1", g.closed)
	}
	// Relaunching builds a fresh instance rather than reusing the crashed one.
	m, _ = step(t, m, key("l"))
	m, _ = step(t, m, key("enter"))
	if m.screen != screenPlaying {
		t.Fatalf("relaunch after crash failed: %v", m.screen)
	}
}

func TestRegistrationErrorStaysOnMenu(t *testing.T) {
	reg := engine.Registration{
		Info: sdk.Info{ID: "broken/game", Title: "BROKEN", PixelW: 8, PixelH: 8},
		New:  func(sdk.CellShape) (sdk.Game, error) { return nil, errBroken },
	}
	m := newTestShellReg(t, reg)
	m, _ = step(t, m, key("l"))
	m, cmd := step(t, m, key("enter"))
	if m.screen != screenMenu || cmd == nil {
		t.Fatalf("failed launch did not return on a clean index: screen=%v cmd=%v", m.screen, cmd)
	}
	if !strings.Contains(view(m), "does not load") {
		t.Error("menu does not surface the load error")
	}
}

var errBroken = errors.New("does not load")

func TestQuitToMenuClosesGame(t *testing.T) {
	g := &fakeGame{}
	m := newTestShell(t, g)
	m, _ = step(t, m, key("l"))
	m, _ = step(t, m, key("enter"))
	m, _ = step(t, m, key("esc")) // pause
	m, _ = step(t, m, key("j"))
	m, _ = step(t, m, key("j"))
	m, _ = step(t, m, key("enter")) // Quit to Menu
	if m.screen != screenMenu {
		t.Fatalf("screen = %v", m.screen)
	}
	if g.closed != 1 {
		t.Errorf("closed = %d, want 1", g.closed)
	}
}

func TestHUDSanitizesControlCharacters(t *testing.T) {
	h := sdk.HUD{
		Fields: []sdk.HUDField{{Label: "A\x1b[31m", Value: "B\x9bC"}},
		Hint:   "hi\x07nt",
	}
	out := renderHUD(h)
	for _, bad := range []string{"\x1b[31m", "\x9b", "\x07"} {
		if strings.Contains(out, bad) {
			t.Errorf("control sequence %q survived sanitization", bad)
		}
	}
	if !strings.Contains(out, "hint") {
		t.Errorf("legit text mangled: %q", out)
	}
}

func TestKeyForwardingAndPause(t *testing.T) {
	g := &fakeGame{}
	m := newTestShell(t, g)
	m, _ = step(t, m, key("l"))
	m, _ = step(t, m, key("enter"))

	m, _ = step(t, m, key("a")) // maps to KeyLeft
	if g.lastKey != sdk.KeyLeft {
		t.Fatalf("lastKey = %v, want KeyLeft", g.lastKey)
	}

	// Releases reach the game too, on terminals that report them.
	m, _ = step(t, m, keyUp("a"))
	if g.lastKeyUp != sdk.KeyLeft {
		t.Fatalf("lastKeyUp = %v, want KeyLeft", g.lastKeyUp)
	}

	m, _ = step(t, m, key("esc"))
	if m.screen != screenPaused {
		t.Fatalf("screen = %v after esc", m.screen)
	}
	if !strings.Contains(view(m), "PAUSED") {
		t.Error("pause overlay not shown")
	}

	// Stale tick must not advance the game while paused.
	before := g.updates
	m, _ = step(t, m, tickMsg{gen: m.tickGen - 1})
	if g.updates != before {
		t.Error("stale tick advanced a paused game")
	}

	// Resume via menu selection (first choice).
	m, cmd := step(t, m, key("enter"))
	if m.screen != screenPlaying || cmd == nil {
		t.Fatalf("resume failed: screen=%v", m.screen)
	}

	// Pause again, choose Restart (second choice).
	m, _ = step(t, m, key("esc"))
	m, _ = step(t, m, key("j"))
	m, _ = step(t, m, key("enter"))
	if g.resets != 2 || m.screen != screenPlaying {
		t.Fatalf("restart failed: resets=%d screen=%v", g.resets, m.screen)
	}

	// Pause, Quit to Menu (third choice).
	m, _ = step(t, m, key("esc"))
	m, _ = step(t, m, key("j"))
	m, _ = step(t, m, key("j"))
	m, _ = step(t, m, key("enter"))
	if m.screen != screenMenu {
		t.Fatalf("quit-to-menu failed: screen=%v", m.screen)
	}
}

func TestGameOverScoreAndReplay(t *testing.T) {
	g := &fakeGame{endAfter: 3}
	m := newTestShell(t, g)
	m, _ = step(t, m, key("l"))
	m, _ = step(t, m, key("enter"))
	m, _ = step(t, m, key(" ")) // score 5 via HandleKey
	for i := 0; i < 3; i++ {
		m, _ = tickNow(t, m)
	}
	if m.screen != screenGameOver {
		t.Fatalf("screen = %v after endAfter updates", m.screen)
	}
	out := view(m)
	if !strings.Contains(out, "GAME OVER") || !strings.Contains(out, "NEW HIGH SCORE") {
		t.Errorf("game-over view missing content:\n%s", out)
	}
	if got := m.scores.High("fake"); got != 5 {
		t.Errorf("submitted high = %d, want 5", got)
	}

	// Replay resets and returns to playing.
	m, cmd := step(t, m, key("r"))
	if m.screen != screenPlaying || cmd == nil || g.resets != 2 {
		t.Fatalf("replay failed: screen=%v resets=%d", m.screen, g.resets)
	}

	// Die again with same score: no new-high flag this time.
	for i := 0; i < 3; i++ {
		m, _ = tickNow(t, m)
	}
	if strings.Contains(view(m), "NEW HIGH SCORE") {
		t.Error("equal score flagged as new high")
	}

	m, _ = step(t, m, key("m"))
	if m.screen != screenMenu {
		t.Fatalf("m did not return to menu: %v", m.screen)
	}
	if !strings.Contains(view(m), "high 5") {
		t.Error("menu does not show persisted high score")
	}
}

func TestTooSmallTerminal(t *testing.T) {
	g := &fakeGame{}
	m := newTestShell(t, g)
	m, _ = step(t, m, key("l"))
	m, _ = step(t, m, key("enter"))
	m, _ = step(t, m, tea.WindowSizeMsg{Width: 10, Height: 5})
	if !strings.Contains(view(m), "too small") {
		t.Error("no too-small notice on tiny terminal")
	}
	m, _ = step(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
	if strings.Contains(view(m), "too small") {
		t.Error("too-small notice persisted after resize")
	}
}

// fakeMarket scripts the Marketplace hooks for TUI tests.
func fakeMarket(signedIn *bool, installed *[]engine.Registration) *Marketplace {
	games := []MarketGame{
		{ID: "acme/blaster", Name: "Blaster", Description: "pew", Version: "1.0.0", HasPackage: true},
		{ID: "acme/nopack", Name: "NoPack", Version: "0.1.0", HasPackage: false},
	}
	return &Marketplace{
		List: func() ([]MarketGame, error) { return games, nil },
		Install: func(id string) error {
			*installed = append(*installed, engine.Registration{
				Info:      sdk.Info{ID: id, Title: "BLASTER", PixelW: 8, PixelH: 8},
				New:       func(sdk.CellShape) (sdk.Game, error) { return &fakeGame{}, nil },
				Installed: true,
			})
			return nil
		},
		Remove: func(id string) error {
			*installed = (*installed)[:0]
			return nil
		},
		Account: func() (string, bool) {
			if *signedIn {
				return "p@t.dev", true
			}
			return "", false
		},
		SignIn: func(email, password string) error { *signedIn = true; return nil },
		SignUp: func(username, email, password string) error {
			*signedIn = true
			signedUpAs = username
			return nil
		},
		SignOut: func() error { *signedIn = false; return nil },
		Reload: func() []engine.Registration {
			g := &fakeGame{}
			base := []engine.Registration{{
				Info: g.Info(),
				New:  func(sdk.CellShape) (sdk.Game, error) { return g, nil },
			}}
			return append(base, *installed...)
		},
	}
}

// signedUpAs records the handle the signup form submitted, so a test can
// prove the field is wired rather than merely present.
var signedUpAs string

func newMarketShell(t *testing.T) (Model, *bool, *[]engine.Registration) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	st, err := scores.Load()
	if err != nil {
		t.Fatal(err)
	}
	signedIn := false
	signedUpAs = ""
	var installed []engine.Registration
	mp := fakeMarket(&signedIn, &installed)
	m := New(mp.Reload(), st, sdk.Quadrant, mp)
	mm, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	return mm.(Model), &signedIn, &installed
}

// drain runs a command chain to completion, feeding messages back in.
func drain(t *testing.T, m Model, cmd tea.Cmd) Model {
	t.Helper()
	for cmd != nil {
		msg := cmd()
		if msg == nil {
			break
		}
		if batch, ok := msg.(tea.BatchMsg); ok {
			for _, batched := range batch {
				m = drain(t, m, batched)
			}
			break
		}
		var mm tea.Model
		mm, cmd = m.Update(msg)
		m = mm.(Model)
	}
	return m
}

func TestPageTransitionsClearTerminal(t *testing.T) {
	t.Run("game to index", func(t *testing.T) {
		g := &fakeGame{}
		m := newTestShell(t, g)
		m, _ = step(t, m, key("l"))
		m, _ = step(t, m, key("enter"))
		m, _ = step(t, m, key("esc"))
		m, _ = step(t, m, key("j"))
		m, _ = step(t, m, key("j"))
		m, cmd := step(t, m, key("enter"))
		if m.screen != screenMenu || cmd == nil {
			t.Fatalf("game exit did not clear index route: screen=%v cmd=%v", m.screen, cmd)
		}
	})

	t.Run("marketplace to account", func(t *testing.T) {
		m, _, _ := newMarketShell(t)
		m, cmd := step(t, m, key("m"))
		m = drain(t, m, cmd)
		m, cmd = step(t, m, key("l"))
		if m.screen != screenAuth || cmd == nil {
			t.Fatalf("login did not clear account route: screen=%v cmd=%v", m.screen, cmd)
		}
	})

	t.Run("pause stays on game route", func(t *testing.T) {
		g := &fakeGame{}
		m := newTestShell(t, g)
		m, _ = step(t, m, key("l"))
		m, _ = step(t, m, key("enter"))
		m, cmd := step(t, m, key("esc"))
		if m.screen != screenPaused || cmd != nil {
			t.Fatalf("pause crossed a page boundary: screen=%v cmd=%v", m.screen, cmd)
		}
	})
}

func TestMarketBrowseAnonymous(t *testing.T) {
	m, _, _ := newMarketShell(t)
	if !strings.Contains(view(m), "m marketplace") {
		t.Fatal("menu does not advertise the marketplace")
	}
	mm, cmd := step(t, m, key("m"))
	if mm.screen != screenMarket || cmd == nil {
		t.Fatalf("m key: screen=%v", mm.screen)
	}
	mm = drain(t, mm, cmd)
	out := view(mm)
	for _, want := range []string{"MARKETPLACE", "browsing as guest", "Blaster", "no package yet"} {
		if !strings.Contains(out, want) {
			t.Errorf("marketplace view missing %q", want)
		}
	}
}

func TestMarketInstallAsGuest(t *testing.T) {
	m, signedIn, installed := newMarketShell(t)
	mm, cmd := step(t, m, key("m"))
	mm = drain(t, mm, cmd)

	mm, cmd = step(t, mm, key("enter"))
	if cmd == nil {
		t.Fatal("no install command issued")
	}
	mm = drain(t, mm, cmd)

	if *signedIn {
		t.Fatal("guest install unexpectedly signed in")
	}
	if len(*installed) != 1 || (*installed)[0].Info.ID != "acme/blaster" {
		t.Fatalf("install failed: %+v", *installed)
	}
	if mm.screen != screenMarket {
		t.Fatalf("screen = %v after install", mm.screen)
	}
	if out := view(mm); !strings.Contains(out, "in your arcade") {
		t.Errorf("installed marker missing:\n%s", out)
	}

	// Back through the index, the library lists the new game.
	mm, _ = step(t, mm, key("esc"))
	mm, _ = step(t, mm, key("l"))
	if out := view(mm); !strings.Contains(out, "BLASTER") {
		t.Error("library missing the installed game")
	}
}

func TestMarketSignup(t *testing.T) {
	m, signedIn, _ := newMarketShell(t)
	mm, cmd := step(t, m, key("m"))
	mm = drain(t, mm, cmd)

	mm, _ = step(t, mm, key("l"))
	mm, _ = step(t, mm, key("j"))
	mm, _ = step(t, mm, key("enter"))

	// Signing up asks for a handle first: it is what games are published
	// under, so it is the decision being made rather than a field below the
	// password.
	typeIn := func(m Model, text string) Model {
		for _, r := range text {
			m, _ = step(t, m, key(string(r)))
		}
		return m
	}
	mm = typeIn(mm, "nicodes")
	mm, _ = step(t, mm, key("tab"))
	mm = typeIn(mm, "p@t.dev")
	mm, _ = step(t, mm, key("tab"))
	mm = typeIn(mm, "password123")

	mm, cmd = step(t, mm, key("enter"))
	if cmd == nil {
		t.Fatal("no signup command issued")
	}
	mm = drain(t, mm, cmd)

	if signedUpAs != "nicodes" {
		t.Errorf("signup submitted username %q, want nicodes", signedUpAs)
	}
	if !*signedIn {
		t.Fatal("signup hook not called")
	}
	if mm.screen != screenMarket {
		t.Fatalf("screen = %v after signup", mm.screen)
	}
}

func TestMarketRemoveAndLogout(t *testing.T) {
	m, signedIn, installed := newMarketShell(t)
	*signedIn = true
	mm, cmd := step(t, m, key("m"))
	mm = drain(t, mm, cmd)
	mm, cmd = step(t, mm, key("enter")) // install while signed in
	mm = drain(t, mm, cmd)
	if len(*installed) != 1 {
		t.Fatalf("install failed: %+v", *installed)
	}
	mm, cmd = step(t, mm, key("x")) // remove it
	mm = drain(t, mm, cmd)
	if len(*installed) != 0 {
		t.Fatal("remove hook not called")
	}
	mm, _ = step(t, mm, key("l")) // logout
	if *signedIn {
		t.Fatal("logout hook not called")
	}
	if !strings.Contains(view(mm), "browsing as guest") {
		t.Error("logout not reflected in header")
	}
}

func TestMenuRemoveInstalledGame(t *testing.T) {
	m, signedIn, installed := newMarketShell(t)
	*signedIn = true
	mm, cmd := step(t, m, key("m"))
	mm = drain(t, mm, cmd)
	mm, cmd = step(t, mm, key("enter")) // install Blaster
	mm = drain(t, mm, cmd)
	mm, _ = step(t, mm, key("esc")) // back to the index
	mm, _ = step(t, mm, key("l"))   // into the library
	if !strings.Contains(view(mm), "BLASTER") {
		t.Fatal("installed game missing from library")
	}

	// r on the installed game removes it and the menu updates.
	mm, _ = step(t, mm, key("j"))
	mm, cmd = step(t, mm, key("r"))
	if cmd == nil {
		t.Fatal("r on an installed game did nothing")
	}
	mm = drain(t, mm, cmd)
	if len(*installed) != 0 {
		t.Fatal("remove hook not called")
	}
	out := view(mm)
	if strings.Contains(out, "BLASTER") && !strings.Contains(out, "removed acme/blaster") {
		t.Errorf("library still lists the removed game:\n%s", out)
	}
	if !strings.Contains(out, "removed acme/blaster") {
		t.Error("no removal notice shown")
	}
}

func TestIndexShowsRecentlyPlayed(t *testing.T) {
	g := &fakeGame{}
	m := newTestShell(t, g)
	if strings.Contains(view(m), "recently played") {
		t.Fatal("fresh index claims recent games")
	}
	m, _ = step(t, m, key("l"))
	m, _ = step(t, m, key("enter")) // play
	m, _ = step(t, m, key("esc"))   // pause
	m, _ = step(t, m, key("j"))
	m, _ = step(t, m, key("j"))
	m, _ = step(t, m, key("enter")) // Quit to Menu

	out := view(m)
	if !strings.Contains(out, "recently played") || !strings.Contains(out, "FAKE") {
		t.Fatalf("index missing the recent game:\n%s", out)
	}
	// Enter on the recent row starts it directly, without the library.
	m, cmd := step(t, m, key("enter"))
	if m.screen != screenPlaying || cmd == nil {
		t.Fatalf("recent row did not start the game: screen=%v", m.screen)
	}
}

func TestPixelToggle(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	st, err := scores.Load()
	if err != nil {
		t.Fatal(err)
	}
	g := &fakeGame{}
	var shapes []string
	reg := engine.Registration{
		Info: g.Info(),
		New: func(sh sdk.CellShape) (sdk.Game, error) {
			shapes = append(shapes, sh.Name)
			return g, nil
		},
	}
	m := New([]engine.Registration{reg}, st, sdk.Quadrant, nil)
	mm, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = mm.(Model)

	if !strings.Contains(view(m), "p pixels") {
		t.Fatal("index hint does not mention the pixel toggle")
	}

	// p cycles quad -> sextant and persists asynchronously.
	m, cmd := step(t, m, key("p"))
	if !strings.Contains(view(m), "pixels: sextant") {
		t.Fatalf("toggle notice missing:\n%s", view(m))
	}
	if cmd == nil {
		t.Fatal("toggle issued no save command")
	}
	cmd()
	if s, _ := settings.Load(); s.Pixels != "sextant" {
		t.Fatalf("persisted pixels = %q, want sextant", s.Pixels)
	}

	// Two more: half, then ascii.
	m, _ = step(t, m, key("p"))
	m, cmd = step(t, m, key("p"))
	cmd()
	if s, _ := settings.Load(); s.Pixels != "ascii" {
		t.Fatalf("persisted pixels = %q, want ascii", s.Pixels)
	}

	// Launching sees the current shape.
	m, _ = step(t, m, key("l"))
	if !strings.Contains(view(m), "p pixels") {
		t.Error("library hint does not mention the pixel toggle")
	}
	m, _ = step(t, m, key("enter"))
	if len(shapes) != 1 || shapes[0] != "ascii" {
		t.Fatalf("New saw shapes %v, want [ascii]", shapes)
	}
	if m.canvas.Shape().Name != "ascii" {
		t.Fatalf("canvas shape = %q", m.canvas.Shape().Name)
	}

	// Quit out, toggle onward (ascii wraps to quad), relaunch the SAME game:
	// the instance and canvas must be rebuilt for the new shape.
	m, _ = step(t, m, key("esc"))
	m, _ = step(t, m, key("j"))
	m, _ = step(t, m, key("j"))
	m, _ = step(t, m, key("enter")) // Quit to Menu
	m, cmd = step(t, m, key("p"))
	cmd()
	m, _ = step(t, m, key("l"))
	m, _ = step(t, m, key("enter"))
	if len(shapes) != 2 || shapes[1] != "quad" {
		t.Fatalf("relaunch after toggle saw shapes %v, want [ascii quad]", shapes)
	}
	if m.canvas.Shape().Name != "quad" {
		t.Fatalf("canvas not rebuilt: shape = %q", m.canvas.Shape().Name)
	}
}

// -------------------------------------------------------- play continuity --

// syncingMarket is a Marketplace whose only interesting hook is Sync.
func syncingMarket(calls *int, notice string, latest map[string]string) *Marketplace {
	signedIn := false
	var installed []engine.Registration
	mp := fakeMarket(&signedIn, &installed)
	mp.Sync = func() (string, map[string]string) {
		*calls++
		return notice, latest
	}
	return mp
}

func newSyncShell(t *testing.T, mp *Marketplace, regs ...engine.Registration) Model {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	st, err := scores.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(regs) == 0 {
		g := &fakeGame{}
		regs = []engine.Registration{{
			Info: g.Info(),
			New:  func(sdk.CellShape) (sdk.Game, error) { return g, nil },
		}}
	}
	m := New(regs, st, sdk.Quadrant, mp)
	mm, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	return mm.(Model)
}

// An arcade played offline catches up when it starts, not when somebody
// remembers to ask it to.
func TestInitSyncs(t *testing.T) {
	calls := 0
	m := newSyncShell(t, syncingMarket(&calls, "", nil))

	cmd := m.Init()
	if cmd == nil {
		t.Fatal("Init issued no sync command")
	}
	cmd()
	if calls != 1 {
		t.Errorf("sync ran %d times at startup, want 1", calls)
	}
}

// A shell with no marketplace has nothing to sync to, and must not go looking.
func TestInitWithoutAMarketplaceDoesNothing(t *testing.T) {
	m := newTestShell(t, &fakeGame{})
	if m.Init() != nil {
		t.Error("an arcade with no marketplace issued a sync")
	}
}

func TestFinishingARunQueuesItAndSyncs(t *testing.T) {
	calls := 0
	g := &fakeGame{endAfter: 1, score: 42}
	m := newSyncShell(t, syncingMarket(&calls, "", nil), engine.Registration{
		Info:    g.Info(),
		New:     func(sdk.CellShape) (sdk.Game, error) { return g, nil },
		Version: "1.2.0",
	})

	m, _ = step(t, m, key("l"))     // library
	m, _ = step(t, m, key("enter")) // start the game
	m, cmd := tickNow(t, m)
	if m.screen != screenGameOver {
		t.Fatalf("screen = %v, want game over", m.screen)
	}

	pending := m.scores.Pending()
	if len(pending) != 1 {
		t.Fatalf("queued %d runs, want the one just played", len(pending))
	}
	if pending[0].Score != 42 || !pending[0].Completed {
		t.Errorf("run recorded wrong: %+v", pending[0])
	}
	// The version comes off the registration, so the account learns which
	// package produced the score.
	if pending[0].Version != "1.2.0" {
		t.Errorf("run version = %q, want the installed 1.2.0", pending[0].Version)
	}

	if cmd == nil {
		t.Fatal("game over issued no command")
	}
	drain(t, m, cmd)
	if calls != 1 {
		t.Errorf("sync ran %d times after a run, want 1", calls)
	}
}

// Signing in is the moment everything played without an account has somewhere
// to go.
func TestSigningInSyncs(t *testing.T) {
	calls := 0
	m := newSyncShell(t, syncingMarket(&calls, "", nil))

	m.screen = screenAuth
	m, cmd := step(t, m, authDoneMsg{})
	if m.screen != screenMarket {
		t.Fatalf("screen = %v after signing in", m.screen)
	}
	if cmd == nil {
		t.Fatal("signing in issued no sync")
	}
	// Batched with the page-clear the route change adds, so it has to be run
	// as a chain rather than called.
	drain(t, m, cmd)
	if calls != 1 {
		t.Errorf("sync ran %d times after sign-in, want 1", calls)
	}
}

func TestSyncNoticeReachesTheScreen(t *testing.T) {
	calls := 0
	m := newSyncShell(t, syncingMarket(&calls, "", nil))

	m, _ = step(t, m, syncedMsg{notice: "2 runs synced to your account"})
	if !strings.Contains(view(m), "2 runs synced") {
		t.Errorf("sync notice not shown:\n%s", view(m))
	}

	// A quiet sync leaves a notice somebody may be reading alone.
	m, _ = step(t, m, syncedMsg{})
	if !strings.Contains(view(m), "2 runs synced") {
		t.Errorf("a quiet sync erased the previous notice:\n%s", view(m))
	}
}

func TestLibraryMarksAnOutdatedInstall(t *testing.T) {
	calls := 0
	g := &fakeGame{}
	m := newSyncShell(t, syncingMarket(&calls, "", nil), engine.Registration{
		Info:      g.Info(),
		New:       func(sdk.CellShape) (sdk.Game, error) { return g, nil },
		Version:   "1.0.0",
		Installed: true,
	})

	// Nothing is claimed before a sync has said what the marketplace holds.
	m, _ = step(t, m, key("l"))
	if strings.Contains(view(m), "available") {
		t.Errorf("an update was claimed before any catalog was loaded:\n%s", view(m))
	}

	m, _ = step(t, m, syncedMsg{latest: map[string]string{"fake": "2.0.0"}})
	if !strings.Contains(view(m), "v2.0.0 available") {
		t.Errorf("library does not mark the outdated install:\n%s", view(m))
	}

	// A game already on the newest version says nothing at all.
	m, _ = step(t, m, syncedMsg{latest: map[string]string{"fake": "1.0.0"}})
	if strings.Contains(view(m), "available") {
		t.Errorf("a current install was marked as outdated:\n%s", view(m))
	}
}

// A game whose manifest carries no version cannot be compared, and guessing
// would put an update marker on every game that omits one.
func TestAVersionlessGameIsNeverMarkedOutdated(t *testing.T) {
	calls := 0
	m := newSyncShell(t, syncingMarket(&calls, "", nil))

	m, _ = step(t, m, syncedMsg{latest: map[string]string{"fake": "2.0.0"}})
	m, _ = step(t, m, key("l"))
	if strings.Contains(view(m), "available") {
		t.Errorf("a game with no version was marked outdated:\n%s", view(m))
	}
}
