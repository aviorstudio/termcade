package asteroid

import (
	"math"
	"math/rand"
	"testing"

	"github.com/aviorstudio/termcade/sdk"
)

const eps = 1e-9

func TestWrap(t *testing.T) {
	cases := []struct {
		in, want sdk.Vec2
	}{
		{sdk.Vec2{X: 10, Y: 10}, sdk.Vec2{X: 10, Y: 10}},     // inside
		{sdk.Vec2{X: -1, Y: 10}, sdk.Vec2{X: 71, Y: 10}},     // negative X
		{sdk.Vec2{X: 10, Y: -0.5}, sdk.Vec2{X: 10, Y: 39.5}}, // negative Y
		{sdk.Vec2{X: 73, Y: 10}, sdk.Vec2{X: 1, Y: 10}},      // over X
		{sdk.Vec2{X: 10, Y: 41}, sdk.Vec2{X: 10, Y: 1}},      // over Y
		{sdk.Vec2{X: 72, Y: 40}, sdk.Vec2{X: 0, Y: 0}},       // exact boundary
		{sdk.Vec2{X: -72.5, Y: 0}, sdk.Vec2{X: 71.5, Y: 0}},  // far negative
	}
	for _, c := range cases {
		got := wrap(c.in, 72, 40)
		if math.Abs(got.X-c.want.X) > eps || math.Abs(got.Y-c.want.Y) > eps {
			t.Errorf("wrap(%v) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestToroidalDistAcrossSeam(t *testing.T) {
	// Points on opposite horizontal edges are 2 apart through the seam.
	a := sdk.Vec2{X: 1, Y: 20}
	b := sdk.Vec2{X: 71, Y: 20}
	if d := toroidalDist(a, b, 72, 40); math.Abs(d-2) > eps {
		t.Errorf("seam distance = %v, want 2", d)
	}
	// And vertically.
	c := sdk.Vec2{X: 30, Y: 1}
	d := sdk.Vec2{X: 30, Y: 39}
	if got := toroidalDist(c, d, 72, 40); math.Abs(got-2) > eps {
		t.Errorf("vertical seam distance = %v, want 2", got)
	}
	// Non-seam distance matches plain Euclidean.
	e := sdk.Vec2{X: 10, Y: 10}
	f := sdk.Vec2{X: 13, Y: 14}
	if got := toroidalDist(e, f, 72, 40); math.Abs(got-5) > eps {
		t.Errorf("plain distance = %v, want 5", got)
	}

	if !hit(a, b, 3, 72, 40) {
		t.Error("hit missed across the seam")
	}
	if hit(a, b, 1.5, 72, 40) {
		t.Error("hit false positive across the seam")
	}
}

func TestSplitRock(t *testing.T) {
	g := &game{rng: rand.New(rand.NewSource(42))}
	parent := g.newRock(0, sdk.Vec2{X: 30, Y: 20})
	parent.Vel = sdk.Vec2{X: 10, Y: 0}

	kids := g.splitRock(parent)
	if len(kids) != 2 {
		t.Fatalf("large rock split into %d, want 2", len(kids))
	}
	for _, k := range kids {
		if k.Size != 1 {
			t.Errorf("child size = %d, want 1 (medium)", k.Size)
		}
		if k.Pos != parent.Pos {
			t.Errorf("child did not inherit position")
		}
		want := parent.Vel.Len() * 1.3
		if math.Abs(k.Vel.Len()-want) > eps {
			t.Errorf("child speed = %v, want %v", k.Vel.Len(), want)
		}
	}
	// Children diverge: one rotated +, one -.
	if kids[0].Vel.Y*kids[1].Vel.Y > 0 {
		t.Errorf("children did not diverge: %v vs %v", kids[0].Vel, kids[1].Vel)
	}

	mediums := g.splitRock(kids[0])
	if len(mediums) != 2 || mediums[0].Size != 2 {
		t.Fatalf("medium split wrong: %d rocks, size %d", len(mediums), mediums[0].Size)
	}
	if small := g.splitRock(mediums[0]); small != nil {
		t.Errorf("small rock split into %d rocks, want none", len(small))
	}
}

func TestShipNoseRotation(t *testing.T) {
	// At angle 0 the nose points along +X from the ship position.
	pos := sdk.Vec2{X: 20, Y: 20}
	nose := shipModel[0].Rotate(0).Add(pos)
	if nose.X <= pos.X || math.Abs(nose.Y-pos.Y) > eps {
		t.Errorf("angle-0 nose = %v", nose)
	}
	// At -pi/2 (pointing up in screen coords) the nose is above.
	noseUp := shipModel[0].Rotate(-math.Pi / 2).Add(pos)
	if noseUp.Y >= pos.Y || math.Abs(noseUp.X-pos.X) > eps {
		t.Errorf("up-facing nose = %v", noseUp)
	}
}

func TestRockShapeBounds(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for i := 0; i < 100; i++ {
		r := 6.0
		pts := rockShape(rng, r)
		if len(pts) < 8 || len(pts) > 10 {
			t.Fatalf("rock has %d vertices", len(pts))
		}
		for _, p := range pts {
			d := p.Len()
			if d < r*0.74 || d > r*1.16 {
				t.Fatalf("vertex radius %v outside [0.75r, 1.15r]", d)
			}
		}
	}
}

// TestWaveProgression plays the game headlessly with an aimbot to verify
// splitting, scoring, and wave advancement end-to-end.
func TestWaveProgression(t *testing.T) {
	g := &game{}
	g.Reset()
	if len(g.rocks) != 4 {
		t.Fatalf("wave 1 spawned %d rocks, want 4", len(g.rocks))
	}
	g.invuln = 1 << 30 // keep the ship safe; we're testing rocks, not dying
	startWave := g.wave
	for frame := 0; frame < sdk.TPS*120 && g.wave == startWave; frame++ {
		// Aim directly at the nearest rock and fire every few frames.
		if len(g.rocks) > 0 && frame%5 == 0 {
			r := g.rocks[0]
			g.angle = math.Atan2(r.Pos.Y-g.shipPos.Y, r.Pos.X-g.shipPos.X)
			g.HandleKey(sdk.KeyA)
		}
		g.Update()
	}
	if g.wave != startWave+1 {
		t.Fatalf("wave did not advance (wave=%d, rocks left=%d)", g.wave, len(g.rocks))
	}
	// 4 large + 8 medium + 16 small all destroyed.
	want := 4*rockClasses[0].value + 8*rockClasses[1].value + 16*rockClasses[2].value
	if g.score != want {
		t.Errorf("score = %d, want %d", g.score, want)
	}
	if len(g.rocks) != 3+g.wave {
		t.Errorf("new wave has %d rocks, want %d", len(g.rocks), 3+g.wave)
	}
}
