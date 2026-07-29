// Package asteroid is an Asteroids-style rock shooter on a wrapping field.
package asteroid

import (
	"fmt"
	"math"
	"math/rand"
	"strings"
	"time"

	"github.com/aviorstudio/termcade/sdk"
)

const (
	fieldW = 72.0
	fieldH = 40.0

	turnRate = 4.0  // rad/s
	thrust   = 30.0 // px/s^2
	drag     = 0.4  // fraction of velocity shed per second
	maxSpeed = 45.0 // px/s
	shipHitR = 2.0

	bulletSpeed = 40.0 // px/s added to ship velocity
	bulletTTL   = 72   // ticks (~1.2s at 60 TPS)
	maxBullets  = 4

	startLives  = 3
	invulnTicks = 120 // ~2s at 60 TPS

	// blinkTicks is the on/off period of the invulnerability and thrust
	// flicker, in ticks.
	blinkTicks = 4
)

type rockClass struct {
	radius float64
	speed  float64
	value  int
}

// rock size classes: index 0 large, 1 medium, 2 small.
var rockClasses = []rockClass{
	{radius: 6.0, speed: 8.0, value: 20},
	{radius: 3.5, speed: 13.0, value: 50},
	{radius: 2.0, speed: 20.0, value: 100},
}

type Rock struct {
	Pos, Vel sdk.Vec2
	Size     int // index into rockClasses
	Shape    []sdk.Vec2
	Rot      float64
	Spin     float64
}

type Bullet struct {
	Pos, Vel sdk.Vec2
	TTL      int
}

type game struct {
	shipPos  sdk.Vec2
	shipVel  sdk.Vec2
	angle    float64
	bullets  []Bullet
	rocks    []Rock
	wave     int
	lives    int
	score    int
	invuln   int
	frame    int
	thrustOn bool
	keys     *sdk.KeyTracker
	rng      *rand.Rand
}

var info = sdk.Info{ID: "aviorstudio/asteroid", Title: "ASTEROID", PixelW: 72, PixelH: 40}

// New constructs a fresh game; the arcade compiles it in as a builtin,
// and cmd/wasm packages the same code as an installable .tcade.
func New() sdk.Game { return &game{} }

func (g *game) Info() sdk.Info { return info }

func (g *game) Reset() {
	g.score = 0
	g.lives = startLives
	g.wave = 1
	g.keys = sdk.NewKeyTracker(sdk.DefaultHoldTicks)
	g.rng = rand.New(rand.NewSource(time.Now().UnixNano()))
	g.respawn()
	g.spawnWave()
}

func (g *game) respawn() {
	g.shipPos = sdk.Vec2{X: fieldW / 2, Y: fieldH / 2}
	g.shipVel = sdk.Vec2{}
	g.angle = -math.Pi / 2 // pointing up
	g.invuln = invulnTicks
	g.bullets = g.bullets[:0]
}

func (g *game) spawnWave() {
	n := 3 + g.wave
	g.rocks = g.rocks[:0]
	for i := 0; i < n; i++ {
		g.rocks = append(g.rocks, g.newRock(0, g.randEdgePos()))
	}
}

// randEdgePos picks a spawn point at least 15px (toroidally) from the ship.
func (g *game) randEdgePos() sdk.Vec2 {
	for {
		p := sdk.Vec2{X: g.rng.Float64() * fieldW, Y: g.rng.Float64() * fieldH}
		if toroidalDist(p, g.shipPos, fieldW, fieldH) >= 15 {
			return p
		}
	}
}

func (g *game) newRock(size int, pos sdk.Vec2) Rock {
	class := rockClasses[size]
	dir := g.rng.Float64() * 2 * math.Pi
	speed := class.speed * (0.7 + g.rng.Float64()*0.6)
	return Rock{
		Pos:   pos,
		Vel:   sdk.Vec2{X: math.Cos(dir), Y: math.Sin(dir)}.Scale(speed),
		Size:  size,
		Shape: rockShape(g.rng, class.radius),
		Spin:  (g.rng.Float64() - 0.5) * 3,
	}
}

// splitRock returns the fragments of a destroyed rock: two of the next size
// down, inheriting position with diverging, faster velocities.
func (g *game) splitRock(r Rock) []Rock {
	if r.Size >= len(rockClasses)-1 {
		return nil
	}
	out := make([]Rock, 0, 2)
	for _, sign := range []float64{1, -1} {
		child := g.newRock(r.Size+1, r.Pos)
		angle := sign * (0.7 + g.rng.Float64()*0.4)
		child.Vel = r.Vel.Rotate(angle).Scale(1.3)
		out = append(out, child)
	}
	return out
}

func (g *game) HandleKeyUp(k sdk.Key) { g.keys.Release(k) }

func (g *game) HandleKey(k sdk.Key) {
	g.keys.Press(k)
	if k == sdk.KeyA && len(g.bullets) < maxBullets {
		nose := shipModel[0].Rotate(g.angle).Add(g.shipPos)
		dir := sdk.Vec2{X: math.Cos(g.angle), Y: math.Sin(g.angle)}
		g.bullets = append(g.bullets, Bullet{
			Pos: nose,
			Vel: g.shipVel.Add(dir.Scale(bulletSpeed)),
			TTL: bulletTTL,
		})
	}
}

func (g *game) Update() sdk.Status {
	const dt = sdk.Dt
	g.keys.Tick()
	g.frame++
	if g.invuln > 0 {
		g.invuln--
	}

	// Ship control and motion.
	if g.keys.Held(sdk.KeyLeft) {
		g.angle -= turnRate * dt
	}
	if g.keys.Held(sdk.KeyRight) {
		g.angle += turnRate * dt
	}
	g.thrustOn = g.keys.Held(sdk.KeyUp)
	if g.thrustOn {
		dir := sdk.Vec2{X: math.Cos(g.angle), Y: math.Sin(g.angle)}
		g.shipVel = g.shipVel.Add(dir.Scale(thrust * dt))
	}
	g.shipVel = g.shipVel.Scale(1 - drag*dt)
	if s := g.shipVel.Len(); s > maxSpeed {
		g.shipVel = g.shipVel.Scale(maxSpeed / s)
	}
	g.shipPos = wrap(g.shipPos.Add(g.shipVel.Scale(dt)), fieldW, fieldH)

	// Bullets.
	alive := g.bullets[:0]
	for _, b := range g.bullets {
		b.TTL--
		if b.TTL <= 0 {
			continue
		}
		b.Pos = wrap(b.Pos.Add(b.Vel.Scale(dt)), fieldW, fieldH)
		alive = append(alive, b)
	}
	g.bullets = alive

	// Rocks.
	for i := range g.rocks {
		r := &g.rocks[i]
		r.Pos = wrap(r.Pos.Add(r.Vel.Scale(dt)), fieldW, fieldH)
		r.Rot += r.Spin * dt
	}

	// Bullet-rock collisions.
	var spawned []Rock
	for bi := 0; bi < len(g.bullets); bi++ {
		for ri := 0; ri < len(g.rocks); ri++ {
			r := g.rocks[ri]
			if !hit(g.bullets[bi].Pos, r.Pos, rockClasses[r.Size].radius, fieldW, fieldH) {
				continue
			}
			g.score += rockClasses[r.Size].value
			spawned = append(spawned, g.splitRock(r)...)
			g.rocks = append(g.rocks[:ri], g.rocks[ri+1:]...)
			g.bullets = append(g.bullets[:bi], g.bullets[bi+1:]...)
			bi--
			break
		}
	}
	g.rocks = append(g.rocks, spawned...)

	// Ship-rock collisions.
	if g.invuln == 0 {
		for _, r := range g.rocks {
			if hit(g.shipPos, r.Pos, rockClasses[r.Size].radius+shipHitR, fieldW, fieldH) {
				g.lives--
				if g.lives <= 0 {
					return sdk.StatusGameOver
				}
				g.respawn()
				break
			}
		}
	}

	if len(g.rocks) == 0 {
		g.wave++
		g.spawnWave()
		g.invuln = invulnTicks / 2 // brief grace as the new wave appears
	}
	return sdk.StatusRunning
}

func (g *game) Draw(c *sdk.Canvas) {
	for _, r := range g.rocks {
		c.Polyline(transform(r.Shape, r.Rot, r.Pos), true, sdk.Gray)
	}
	for _, b := range g.bullets {
		// A small disc rather than a snapped unit: same on-screen size as
		// before, but placed at sub-unit precision.
		c.FillCircle(b.Pos.X, b.Pos.Y, 0.6, sdk.Yellow)
	}
	// Blink while invulnerable by skipping draw on alternating spans.
	if g.invuln > 0 && (g.frame/blinkTicks)%2 == 1 {
		return
	}
	c.Polyline(transform(shipModel, g.angle, g.shipPos), true, sdk.White)
	if g.thrustOn && (g.frame/blinkTicks)%2 == 0 { // flicker
		c.Polyline(transform(flameModel, g.angle, g.shipPos), false, sdk.Orange)
	}
}

func (g *game) Score() int { return g.score }

func (g *game) HUD() sdk.HUD {
	return sdk.HUD{
		Fields: []sdk.HUDField{
			{Label: "SCORE", Value: fmt.Sprintf("%06d", g.score), Accent: true},
			{Label: "WAVE", Value: fmt.Sprintf("%d", g.wave)},
			{Value: strings.TrimRight(strings.Repeat("▲ ", g.lives), " "), Accent: true},
		},
		Hint: "←/→ turn · ↑ thrust · space fire",
	}
}
