package shell

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/aviorstudio/termcade/internal/engine"
	"github.com/aviorstudio/termcade/internal/scores"
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
		New:  func() (sdk.Game, error) { return g, nil },
	})
}

func newTestShellReg(t *testing.T, reg engine.Registration) Model {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	st, err := scores.Load()
	if err != nil {
		t.Fatal(err)
	}
	m := New([]engine.Registration{reg}, st, sdk.Quadrant)
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

	if !strings.Contains(view(m), "FAKE") {
		t.Fatal("menu does not list the game")
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
	m, _ = step(t, m, key("enter"))
	if m.screen != screenPlaying {
		t.Fatalf("relaunch after crash failed: %v", m.screen)
	}
}

func TestRegistrationErrorStaysOnMenu(t *testing.T) {
	reg := engine.Registration{
		Info: sdk.Info{ID: "broken/game", Title: "BROKEN", PixelW: 8, PixelH: 8},
		New:  func() (sdk.Game, error) { return nil, errBroken },
	}
	m := newTestShellReg(t, reg)
	m, cmd := step(t, m, key("enter"))
	if m.screen != screenMenu || cmd != nil {
		t.Fatalf("screen = %v after failed launch", m.screen)
	}
	if !strings.Contains(view(m), "does not load") {
		t.Error("menu does not surface the load error")
	}
}

var errBroken = errors.New("does not load")

func TestQuitToMenuClosesGame(t *testing.T) {
	g := &fakeGame{}
	m := newTestShell(t, g)
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
