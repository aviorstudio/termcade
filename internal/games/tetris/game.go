// Package tetris is a falling-block stacker with Super Rotation System kicks.
package tetris

import (
	"fmt"
	"math/rand"
	"time"

	"github.com/aviorstudio/termcade/internal/engine"
	"github.com/aviorstudio/termcade/sdk"
)

const (
	// A square block: logical units are square, and the well is two units per
	// board square in each axis. That makes the 10x20 well exactly 20x40
	// units, which is the tallest playfield that still fits an 80x24 terminal.
	cellW = 2.0
	cellH = 2.0

	wellX = 0.0
	wellY = 0.0

	sepX      = 21
	previewX  = 23.0
	previewY  = 6.0
	previewSz = 4 // the preview grid is 4x4 board squares

	spawnX = 3

	// Timings are in ticks at the shell's 60 TPS.
	lockDelay     = 30 // grounded grace before a piece sets
	maxLockResets = 15 // cap so a piece cannot be stalled forever
	dasDelay      = 10 // held-shift pause before auto-repeat
	dasRate       = 2  // squares per auto-repeat step
	softDropTicks = 2
	rotateCool    = 8  // debounce, so key auto-repeat cannot spin a piece
	dropCool      = 12 // debounce, so auto-repeat cannot chain hard drops

	linesPerLevel = 10
)

// lineScore is the guideline award for clearing n rows at once, before the
// level multiplier. Clearing four at a time is worth far more than four singles.
var lineScore = [5]int{0, 100, 300, 500, 800}

// gravityTicks is how long a piece hangs at each row, by level. Levels past the
// table stay at the final speed.
var gravityTicks = []int{48, 43, 38, 33, 28, 23, 18, 13, 8, 6, 5, 5, 5, 4, 4, 4, 3, 3, 3, 2}

type game struct {
	board *Board
	cur   Piece
	next  Kind
	bag   *bag

	score int
	lines int
	level int
	dead  bool

	fall       int // ticks since the last gravity step
	lockTimer  int // ticks grounded
	lockResets int
	softTick   int
	dasLeft    int
	dasRight   int
	rotateCD   int
	dropCD     int

	keys *sdk.KeyTracker
	rng  *rand.Rand
}

var info = sdk.Info{ID: "aviorstudio/tetris", Title: "TETRIS", PixelW: 32, PixelH: 40}

func Register() engine.Registration {
	return engine.Registration{Info: info, New: func() (sdk.Game, error) { return &game{}, nil }}
}

func (g *game) Info() sdk.Info { return info }

func (g *game) Reset() {
	g.board = NewBoard(boardW, boardH)
	g.score = 0
	g.lines = 0
	g.level = 1
	g.dead = false
	g.keys = sdk.NewKeyTracker(sdk.DefaultHoldTicks)
	g.rng = rand.New(rand.NewSource(time.Now().UnixNano()))
	g.bag = newBag(g.rng)
	g.next = g.bag.Take()
	g.dasLeft, g.dasRight, g.rotateCD, g.dropCD = 0, 0, 0, 0
	g.spawn()
}

// spawn brings in the next piece and ends the run if it has nowhere to go.
func (g *game) spawn() {
	g.cur = Piece{Kind: g.next, X: spawnX, Y: 0}
	g.next = g.bag.Take()
	g.fall, g.lockTimer, g.lockResets, g.softTick = 0, 0, 0, 0
	if !g.board.Fits(g.cur) {
		g.dead = true
	}
}

func (g *game) HandleKeyUp(k sdk.Key) { g.keys.Release(k) }

func (g *game) HandleKey(k sdk.Key) {
	g.keys.Press(k)
	if g.dead {
		return
	}
	switch k {
	case sdk.KeyUp:
		if g.rotateCD > 0 {
			return
		}
		g.rotateCD = rotateCool
		if p, ok := g.board.Rotate(g.cur, 1); ok {
			g.cur = p
			g.touchLock()
		}
	case sdk.KeyA:
		if g.dropCD > 0 {
			return
		}
		g.dropCD = dropCool
		n := g.board.DropDistance(g.cur)
		g.cur.Y += n
		g.score += 2 * n
		g.lockPiece()
	}
}

func (g *game) Update() sdk.Status {
	g.keys.Tick()
	if g.dead {
		return sdk.StatusGameOver
	}
	if g.rotateCD > 0 {
		g.rotateCD--
	}
	if g.dropCD > 0 {
		g.dropCD--
	}

	g.shiftHeld()
	g.softDrop()
	g.applyGravity()

	if g.dead {
		return sdk.StatusGameOver
	}
	return sdk.StatusRunning
}

// shiftHeld implements delayed auto shift: one square on press, a pause, then
// a steady slide while the key stays down.
func (g *game) shiftHeld() {
	g.dasLeft = stepDAS(g.dasLeft, g.keys.Held(sdk.KeyLeft), func() { g.shift(-1) })
	g.dasRight = stepDAS(g.dasRight, g.keys.Held(sdk.KeyRight), func() { g.shift(1) })
}

func stepDAS(held int, down bool, move func()) int {
	if !down {
		return 0
	}
	held++
	if held == 1 || (held > dasDelay && (held-dasDelay)%dasRate == 0) {
		move()
	}
	return held
}

func (g *game) shift(dx int) {
	p := g.cur
	p.X += dx
	if g.board.Fits(p) {
		g.cur = p
		g.touchLock()
	}
}

func (g *game) softDrop() {
	if !g.keys.Held(sdk.KeyDown) {
		g.softTick = 0
		return
	}
	g.softTick++
	if g.softTick < softDropTicks {
		return
	}
	g.softTick = 0
	p := g.cur
	p.Y++
	if g.board.Fits(p) {
		g.cur = p
		g.score++
		g.fall = 0
	}
}

func (g *game) applyGravity() {
	if g.grounded() {
		g.lockTimer++
		if g.lockTimer >= lockDelay {
			g.lockPiece()
		}
		return
	}
	g.lockTimer, g.lockResets = 0, 0
	g.fall++
	if g.fall >= g.gravity() {
		g.fall = 0
		g.cur.Y++
	}
}

func (g *game) grounded() bool {
	p := g.cur
	p.Y++
	return !g.board.Fits(p)
}

// touchLock gives a grounded piece more time after the player moves it, up to
// a cap so the well cannot be held open indefinitely.
func (g *game) touchLock() {
	if !g.grounded() || g.lockResets >= maxLockResets {
		return
	}
	g.lockResets++
	g.lockTimer = 0
}

func (g *game) lockPiece() {
	g.board.Lock(g.cur)
	if n := g.board.ClearLines(); n > 0 {
		g.lines += n
		g.score += lineScore[min(n, len(lineScore)-1)] * g.level
		g.level = g.lines/linesPerLevel + 1
	}
	g.spawn()
}

func (g *game) gravity() int {
	i := g.level - 1
	if i < 0 {
		i = 0
	}
	if i >= len(gravityTicks) {
		i = len(gravityTicks) - 1
	}
	return gravityTicks[i]
}

func (g *game) Draw(c *sdk.Canvas) {
	for y := 0; y < g.board.H; y++ {
		for x := 0; x < g.board.W; x++ {
			if col := g.board.At(x, y); col != 0 {
				drawBlock(c, wellX+float64(x)*cellW, wellY+float64(y)*cellH, col)
			}
		}
	}

	if !g.dead {
		// Ghost first, so the live piece paints over it where they overlap.
		ghost := g.cur
		ghost.Y += g.board.DropDistance(g.cur)
		for _, cc := range ghost.Cells() {
			if cc[1] >= 0 {
				c.FillRectF(wellX+float64(cc[0])*cellW, wellY+float64(cc[1])*cellH,
					cellW, cellH, sdk.DimGray)
			}
		}
		for _, cc := range g.cur.Cells() {
			if cc[1] >= 0 {
				drawBlock(c, wellX+float64(cc[0])*cellW, wellY+float64(cc[1])*cellH,
					g.cur.Kind.Color())
			}
		}
	}

	c.FillRectF(sepX, 0, 0.5, float64(info.PixelH), sdk.DimGray)
	drawPreview(c, g.next)
}

// drawBlock paints one board square with a lighter top edge, which reads as a
// bevel and keeps adjacent blocks of the same color from merging into a slab.
func drawBlock(c *sdk.Canvas, x, y float64, col sdk.Color) {
	c.FillRectF(x, y, cellW, cellH, col)
	c.FillRectF(x, y, cellW, cellH/2, lighten(col))
}

// drawPreview centers the upcoming piece in the panel beside the well.
func drawPreview(c *sdk.Canvas, k Kind) {
	cells := offsets[k][0]
	minX, maxX := cells[0][0], cells[0][0]
	minY, maxY := cells[0][1], cells[0][1]
	for _, off := range cells {
		minX, maxX = min(minX, off[0]), max(maxX, off[0])
		minY, maxY = min(minY, off[1]), max(maxY, off[1])
	}
	w, h := maxX-minX+1, maxY-minY+1
	ox := previewX + (float64(previewSz-w)/2-float64(minX))*cellW
	oy := previewY + (float64(previewSz-h)/2-float64(minY))*cellH
	for _, off := range cells {
		drawBlock(c, ox+float64(off[0])*cellW, oy+float64(off[1])*cellH, k.Color())
	}
}

// lighten raises a color toward white for the block bevel.
func lighten(c sdk.Color) sdk.Color {
	r, g, b := c.RGB()
	f := func(v uint8) uint32 { return uint32(v) + (255-uint32(v))/3 }
	return sdk.Color(f(r)<<16 | f(g)<<8 | f(b))
}

func (g *game) Score() int { return g.score }

func (g *game) HUD() sdk.HUD {
	return sdk.HUD{
		Fields: []sdk.HUDField{
			{Label: "SCORE", Value: fmt.Sprintf("%06d", g.score), Accent: true},
			{Label: "LEVEL", Value: fmt.Sprintf("%d", g.level)},
			{Label: "LINES", Value: fmt.Sprintf("%d", g.lines)},
		},
		Hint: "←/→ move · ↑ rotate · ↓ soft · space drop",
	}
}
