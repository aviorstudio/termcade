// Package shell is the Bubble Tea arcade frame: menu, game loop, overlays.
package shell

import (
	"fmt"
	"strconv"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/aviorstudio/termcade/internal/engine"
	"github.com/aviorstudio/termcade/internal/scores"
	"github.com/aviorstudio/termcade/internal/settings"
	"github.com/aviorstudio/termcade/sdk"
)

const (
	tickStep = time.Second / sdk.TPS
	// maxCatchUp bounds how much simulation a single tick may replay. Without
	// it, one long stall (a suspend, a slow repaint) would be paid back as a
	// burst of steps and the game would visibly fast-forward.
	maxCatchUp = 5 * tickStep
)

type screen int

const (
	screenMenu screen = iota
	screenPlaying
	screenPaused
	screenGameOver
	screenCrashed
	screenMarket
	screenAuth
	screenLibrary
)

// route is the full-screen page boundary. Gameplay overlays share one route;
// every other screen owns its own terminal page.
type route int

const (
	routeIndex route = iota
	routeGame
	routeMarketplace
	routeAccount
	routeLibrary
)

func routeFor(s screen) route {
	switch s {
	case screenMenu:
		return routeIndex
	case screenMarket:
		return routeMarketplace
	case screenAuth:
		return routeAccount
	case screenLibrary:
		return routeLibrary
	default:
		return routeGame
	}
}

type tickMsg struct {
	gen int
	at  time.Time
}

var pauseChoices = []string{"Resume", "Restart", "Quit to Menu"}

type Model struct {
	screen   screen
	games    []engine.Registration
	menuIdx  int
	pauseIdx int
	game     *engine.SafeGame
	gameIdx  int
	shape    sdk.CellShape
	canvas   *sdk.Canvas
	frame    string // cached canvas render, reused under overlays
	scores   *scores.Store
	newHigh  bool
	notice   string // menu-level message, e.g. a game that failed to load
	crash    string // what the crash screen shows
	mp       *Marketplace
	// latest is the newest published version of each game, by id, as of the
	// last sync or marketplace load. Empty until one has happened, which is
	// why nothing claims an update is available before then.
	latest   map[string]string
	market   marketState
	auth     authState
	libIdx   int
	termW    int
	termH    int
	tickGen  int
	lastTick time.Time
	accum    time.Duration
}

// New builds the arcade model. mp may be nil, which hides the marketplace.
func New(games []engine.Registration, st *scores.Store, shape sdk.CellShape, mp *Marketplace) Model {
	return Model{games: games, scores: st, shape: shape, mp: mp}
}

// Init syncs once at startup, which is where an arcade that was played offline
// catches up and where one on a new machine learns what the account already
// knows. It runs off the UI loop like every other marketplace call, so a slow
// or absent network delays nothing.
func (m Model) Init() tea.Cmd { return m.syncCmd() }

func (m Model) tick() tea.Cmd {
	gen := m.tickGen
	return tea.Tick(tickStep, func(t time.Time) tea.Msg { return tickMsg{gen: gen, at: t} })
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	previousRoute := routeFor(m.screen)
	next, cmd := m.update(msg)
	nextModel, ok := next.(Model)
	if ok && routeFor(nextModel.screen) != previousRoute {
		// Bubble Tea normally repaints incrementally. A hard clear at page
		// boundaries prevents cells from a larger route showing through a
		// smaller one in terminals that do not erase blank cells reliably.
		cmd = tea.Batch(tea.ClearScreen, cmd)
	}
	return next, cmd
}

func (m Model) update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if m.mp != nil {
		if mm, cmd, handled := m.updateMarketMsg(msg); handled {
			return mm, cmd
		}
	}
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.termW, m.termH = msg.Width, msg.Height
		return m, nil
	case tickMsg:
		return m.updateTick(msg)
	case tea.KeyPressMsg:
		if m.screen == screenAuth {
			return m.updateAuthKey(msg)
		}
		return m.updateKey(msg.String())
	case tea.KeyReleaseMsg:
		if m.screen == screenPlaying {
			if k := translateKey(msg.String()); k != sdk.KeyNone {
				m.game.HandleKeyUp(k)
			}
			if mm, crashed := m.checkCrash(); crashed {
				return mm, nil
			}
		}
		return m, nil
	}
	return m, nil
}

// updateTick advances the simulation by however much real time has passed,
// in fixed steps. tea.Tick only reschedules once Update returns, so its period
// is the interval plus the frame's own work; stepping off the measured clock
// instead keeps the game running at true speed rather than drifting slow.
func (m Model) updateTick(msg tickMsg) (tea.Model, tea.Cmd) {
	if m.screen != screenPlaying || msg.gen != m.tickGen {
		return m, nil // stale tick from before a pause/quit; drop it
	}
	if m.lastTick.IsZero() {
		m.lastTick = msg.at.Add(-tickStep) // first tick after start/resume: one step
	}
	m.accum += msg.at.Sub(m.lastTick)
	m.lastTick = msg.at
	if m.accum > maxCatchUp {
		m.accum = maxCatchUp
	}

	stepped := false
	for m.accum >= tickStep {
		m.accum -= tickStep
		stepped = true
		status := m.game.Update()
		if mm, crashed := m.checkCrash(); crashed {
			return mm, nil
		}
		if status == sdk.StatusGameOver {
			// Recorded, not merely scored: this also queues the run for the
			// account, which is what carries the same arcade to another
			// machine. It queues signed out too — signing in later sends it.
			m.newHigh = m.scores.Record(
				m.game.Info().ID, m.game.Score(), m.games[m.gameIdx].Version, true)
			m.screen = screenGameOver
			// Saved off the tick path, and synced after it; neither may stall
			// the loop the player is still looking at.
			return m, tea.Batch(saveScores(m.scores), m.syncCmd())
		}
	}
	if stepped {
		m.redraw()
		if mm, crashed := m.checkCrash(); crashed {
			return mm, nil
		}
	}
	return m, m.tick()
}

// saveScores persists asynchronously so a slow disk never stalls the UI loop.
func saveScores(st *scores.Store) tea.Cmd {
	return func() tea.Msg { st.Save(); return nil }
}

// shapeCycle is the order the p key walks through pixel styles.
var shapeCycle = []sdk.CellShape{sdk.Quadrant, sdk.Sextant, sdk.HalfBlock, sdk.ASCII}

// cyclePixels advances the pixel style for the next game start and persists
// the choice. TERMCADE_PIXELS still overrides it at startup.
func (m Model) cyclePixels() (tea.Model, tea.Cmd) {
	next := shapeCycle[0]
	for i, sh := range shapeCycle {
		if sh.Name == m.shape.Name {
			next = shapeCycle[(i+1)%len(shapeCycle)]
			break
		}
	}
	m.shape = next
	m.notice = "pixels: " + m.shape.Name
	st := settings.Settings{Pixels: m.shape.Name}
	return m, func() tea.Msg { st.Save(); return nil }
}

// checkCrash moves a crashed game to the crash screen; reports whether it did.
func (m Model) checkCrash() (Model, bool) {
	if m.game == nil || m.game.Err() == nil {
		return m, false
	}
	m.crash = m.game.Err().Error()
	m.screen = screenCrashed
	m.tickGen++ // freeze the loop; the instance is dead
	return m, true
}

func (m *Model) redraw() {
	m.canvas.Clear()
	m.game.Draw(m.canvas)
	m.frame = m.canvas.Render()
}

func (m Model) updateKey(key string) (tea.Model, tea.Cmd) {
	if key == "ctrl+c" {
		m.scores.Save()
		return m, tea.Quit
	}
	switch m.screen {
	case screenMenu:
		return m.updateMenuKey(key)
	case screenPlaying:
		switch key {
		case "esc", "p":
			m.screen = screenPaused
			m.pauseIdx = 0
			m.tickGen++ // invalidate in-flight ticks: game freezes
			return m, nil
		}
		if k := translateKey(key); k != sdk.KeyNone {
			m.game.HandleKey(k)
		}
		if mm, crashed := m.checkCrash(); crashed {
			return mm, nil
		}
		return m, nil
	case screenPaused:
		return m.updatePausedKey(key)
	case screenGameOver:
		switch key {
		case "enter", "r":
			return m.startGame(m.gameIdx)
		case "esc", "m", "q":
			return m.quitToMenu(), nil
		}
	case screenCrashed:
		return m.quitToMenu(), nil // any key dismisses the wreckage
	case screenMarket:
		return m.updateMarketKey(key)
	case screenLibrary:
		return m.updateLibraryKey(key)
	}
	return m, nil
}

// quitToMenu releases the current game and returns to the menu.
func (m Model) quitToMenu() Model {
	if m.game != nil {
		m.game.Close()
		m.game = nil
	}
	m.screen = screenMenu
	return m
}

func (m Model) updatePausedKey(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "esc", "p":
		return m.resumeGame()
	case "up", "k":
		m.pauseIdx = (m.pauseIdx + len(pauseChoices) - 1) % len(pauseChoices)
	case "down", "j":
		m.pauseIdx = (m.pauseIdx + 1) % len(pauseChoices)
	case "enter":
		switch pauseChoices[m.pauseIdx] {
		case "Resume":
			return m.resumeGame()
		case "Restart":
			m.game.Reset()
			return m.resumeGame()
		case "Quit to Menu":
			return m.quitToMenu(), nil
		}
	}
	return m, nil
}

func (m Model) startGame(idx int) (tea.Model, tea.Cmd) {
	if m.games[idx].Err != nil {
		return m, nil // broken install; listed only so the player sees why
	}
	m.gameIdx = idx
	m.notice = ""
	// A wasm guest sizes its pixel buffer from the shape at init, so a shape
	// change needs a fresh instance, not just a fresh canvas.
	sameShape := m.canvas != nil && m.canvas.Shape().Name == m.shape.Name
	if m.game == nil || m.game.Info().ID != m.games[idx].Info.ID || m.game.Err() != nil || !sameShape {
		if m.game != nil {
			m.game.Close()
			m.game = nil
		}
		g, err := m.games[idx].New(m.shape)
		if err != nil {
			m.notice = m.games[idx].Info.Title + ": " + err.Error()
			m.screen = screenMenu
			return m, nil
		}
		m.game = engine.Safe(g)
		info := m.game.Info()
		m.canvas = sdk.NewCanvas(info.PixelW, info.PixelH, sdk.Black, m.shape)
	}
	m.game.Reset()
	if mm, crashed := m.checkCrash(); crashed {
		return mm, nil
	}
	m.scores.Touch(m.game.Info().ID) // feeds the index's recently-played list
	m.newHigh = false
	m.screen = screenPlaying
	m.tickGen++
	m.resetClock()
	m.redraw()
	return m, tea.Batch(m.tick(), saveScores(m.scores))
}

func (m Model) resumeGame() (tea.Model, tea.Cmd) {
	m.screen = screenPlaying
	m.tickGen++
	m.resetClock()
	return m, m.tick()
}

// resetClock drops accumulated time so a pause is not replayed as catch-up.
func (m *Model) resetClock() {
	m.lastTick = time.Time{}
	m.accum = 0
}

func translateKey(key string) sdk.Key {
	switch key {
	case "left", "a", "h":
		return sdk.KeyLeft
	case "right", "d", "l":
		return sdk.KeyRight
	case "up", "w":
		return sdk.KeyUp
	case "down", "s":
		return sdk.KeyDown
	case " ", "space", "z":
		return sdk.KeyA
	case "x":
		return sdk.KeyB
	case "enter":
		return sdk.KeyStart
	}
	return sdk.KeyNone
}

var (
	titleStyle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#e6c945"))
	dimStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("#8a8a8a"))
	borderStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("#3a3a3a"))
)

func (m Model) View() tea.View {
	v := tea.NewView("")
	v.AltScreen = true
	// Ask for key repeat/release reporting. Terminals that support it give the
	// games exact held-key state; the rest silently ignore the request and the
	// KeyTracker falls back to auto-repeat timing.
	v.KeyboardEnhancements.ReportEventTypes = true
	if m.termW == 0 {
		return v
	}
	var block string
	switch m.screen {
	case screenMenu:
		block = m.viewMenu()
	case screenMarket:
		block = m.viewMarket()
	case screenAuth:
		block = m.viewAuth()
	case screenLibrary:
		block = m.viewLibrary()
	default:
		block = m.viewGame()
	}
	v.SetContent(lipgloss.Place(m.termW, m.termH, lipgloss.Center, lipgloss.Center, block))
	return v
}

// viewGame renders title + bordered canvas (+ overlay) + HUD, or a
// too-small notice if the terminal can't fit the playfield.
func (m Model) viewGame() string {
	info := m.game.Info()
	needW, needH := info.PixelW+2, info.PixelH/2+4
	if m.termW < needW || m.termH < needH {
		return tooSmall(needW, needH, m.termW, m.termH)
	}

	frame := m.frame
	switch m.screen {
	case screenPaused:
		frame = compose(frame, pauseBox(m.pauseIdx))
	case screenGameOver:
		frame = compose(frame, gameOverBox(m.game.Score(), m.newHigh))
	case screenCrashed:
		frame = compose(frame, crashBox(m.crash))
	}

	title := titleStyle.Render(info.Title)
	high := dimStyle.Render("HIGH " + strconv.Itoa(m.scores.High(info.ID)))
	gap := info.PixelW - lipgloss.Width(title) - lipgloss.Width(high)
	header := title + spaces(gap) + high

	return header + "\n" + borderStyle.Render(frame) + "\n" + renderHUD(m.game.HUD())
}

func tooSmall(needW, needH, haveW, haveH int) string {
	return dimStyle.Render(fmt.Sprintf(
		"Terminal too small\nneed %dx%d, have %dx%d", needW, needH, haveW, haveH))
}
