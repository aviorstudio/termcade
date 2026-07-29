// Package brickough is a Breakout-style brick breaker.
package brickough

import (
	"fmt"
	"math"
	"strings"

	"github.com/aviorstudio/termcade/sdk"
)

const (
	fieldW = 64.0
	fieldH = 40.0

	paddleW     = 12.0
	paddleH     = 2.0
	paddleY     = 37.0
	paddleSpeed = 55.0 // px/s

	ballSize      = 2.0
	baseBallSpeed = 32.0 // px/s, level 1
	speedGrowth   = 1.1  // per level

	startLives = 3
)

type game struct {
	paddleX   float64 // paddle center
	ball      sdk.Vec2
	vel       sdk.Vec2
	ballStuck bool
	bricks    []Brick
	level     int
	lives     int
	score     int
	keys      *sdk.KeyTracker
}

var info = sdk.Info{ID: "aviorstudio/brickough", Title: "BRICKOUGH", PixelW: 64, PixelH: 40}

// New constructs a fresh game; the wasm entrypoint registers it. Brickough
// is deliberately not compiled into the arcade — it is the reference
// installable game, and depends only on the sdk module, exactly like a
// third-party game would.
func New() sdk.Game { return &game{} }

func (g *game) Info() sdk.Info { return info }

func (g *game) Reset() {
	g.score = 0
	g.lives = startLives
	g.level = 1
	g.keys = sdk.NewKeyTracker(sdk.DefaultHoldTicks)
	g.startLevel()
}

func (g *game) startLevel() {
	g.bricks = buildLevel(g.level)
	g.resetBall()
}

func (g *game) resetBall() {
	g.paddleX = fieldW / 2
	g.ballStuck = true
}

func (g *game) ballSpeed() float64 {
	return baseBallSpeed * math.Pow(speedGrowth, float64(g.level-1))
}

func (g *game) HandleKeyUp(k sdk.Key) { g.keys.Release(k) }

func (g *game) HandleKey(k sdk.Key) {
	g.keys.Press(k)
	if k == sdk.KeyA && g.ballStuck {
		g.ballStuck = false
		g.vel = paddleBounce(g.ballSpeed(), 0.2) // slight angle so it's not a pure vertical loop
	}
}

func (g *game) Update() sdk.Status {
	const dt = sdk.Dt
	g.keys.Tick()

	if g.keys.Held(sdk.KeyLeft) {
		g.paddleX -= paddleSpeed * dt
	}
	if g.keys.Held(sdk.KeyRight) {
		g.paddleX += paddleSpeed * dt
	}
	g.paddleX = math.Max(paddleW/2, math.Min(fieldW-paddleW/2, g.paddleX))

	if g.ballStuck {
		g.ball = sdk.Vec2{X: g.paddleX, Y: paddleY - ballSize}
		return sdk.StatusRunning
	}

	// Sub-step ball motion so it can never tunnel through a 3px brick.
	move := g.vel.Scale(dt)
	steps := int(math.Ceil(math.Max(math.Abs(move.X), math.Abs(move.Y)))) // <=1px per step
	if steps < 1 {
		steps = 1
	}
	stepVec := move.Scale(1 / float64(steps))
	for i := 0; i < steps; i++ {
		g.ball = g.ball.Add(stepVec)
		if g.stepCollide() {
			stepVec = g.vel.Scale(dt / float64(steps)) // velocity changed; re-derive
		}
		if g.ball.Y > fieldH {
			g.lives--
			if g.lives <= 0 {
				return sdk.StatusGameOver
			}
			g.resetBall()
			return sdk.StatusRunning
		}
	}

	if g.levelCleared() {
		g.level++
		g.startLevel()
	}
	return sdk.StatusRunning
}

// stepCollide resolves wall, paddle, and brick collisions at the ball's
// current position; reports whether velocity changed.
func (g *game) stepCollide() bool {
	changed := false
	half := ballSize / 2

	if g.ball.X < half {
		g.ball.X = half
		g.vel.X = math.Abs(g.vel.X)
		changed = true
	} else if g.ball.X > fieldW-half {
		g.ball.X = fieldW - half
		g.vel.X = -math.Abs(g.vel.X)
		changed = true
	}
	if g.ball.Y < half {
		g.ball.Y = half
		g.vel.Y = math.Abs(g.vel.Y)
		changed = true
	}

	ballBox := AABB{X: g.ball.X - half, Y: g.ball.Y - half, W: ballSize, H: ballSize}

	paddleBox := AABB{X: g.paddleX - paddleW/2, Y: paddleY, W: paddleW, H: paddleH}
	if g.vel.Y > 0 && ballBox.overlaps(paddleBox) {
		off := (g.ball.X - g.paddleX) / (paddleW / 2)
		g.vel = paddleBounce(g.ballSpeed(), off)
		g.ball.Y = paddleY - half
		return true
	}

	for i := range g.bricks {
		b := &g.bricks[i]
		if !b.Alive {
			continue
		}
		hit, axis := brickHit(ballBox, b.Box)
		if !hit {
			continue
		}
		if axis == AxisX {
			g.vel.X = -g.vel.X
		} else {
			g.vel.Y = -g.vel.Y
		}
		b.HP--
		if b.HP <= 0 {
			b.Alive = false
			g.score += b.Value * g.level
		}
		return true // one brick per sub-step
	}
	return changed
}

func (g *game) levelCleared() bool {
	for _, b := range g.bricks {
		if b.Alive {
			return false
		}
	}
	return true
}

func (g *game) Draw(c *sdk.Canvas) {
	for _, b := range g.bricks {
		if !b.Alive {
			continue
		}
		col := b.Color
		if b.HP > 1 {
			col = brighten(col)
		}
		// 1px gaps between bricks read as mortar lines.
		c.FillRectF(b.Box.X, b.Box.Y, b.Box.W-1, b.Box.H-1, col)
	}
	// Float coordinates so the paddle slides smoothly instead of snapping a
	// whole unit at a time, and the ball is an actual disc.
	c.FillRectF(g.paddleX-paddleW/2, paddleY, paddleW, paddleH, sdk.White)
	c.FillCircle(g.ball.X, g.ball.Y, ballSize/2, sdk.Yellow)
}

// brighten lightens a color to signal a 2-HP brick.
func brighten(c sdk.Color) sdk.Color {
	r, gg, b := c.RGB()
	f := func(v uint8) uint32 { return uint32(v) + (255-uint32(v))/2 }
	return sdk.Color(f(r)<<16 | f(gg)<<8 | f(b))
}

func (g *game) Score() int { return g.score }

func (g *game) HUD() sdk.HUD {
	h := sdk.HUD{Fields: []sdk.HUDField{
		{Label: "SCORE", Value: fmt.Sprintf("%06d", g.score), Accent: true},
		{Label: "LEVEL", Value: fmt.Sprintf("%d", g.level)},
		{Value: strings.TrimRight(strings.Repeat("● ", g.lives), " "), Accent: true},
	}}
	if g.ballStuck {
		h.Hint = "space to launch"
	}
	return h
}
