package plugin

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aviorstudio/termcade/sdk"
)

// buildWasm compiles a guest package once per test run. The Go build cache
// makes repeat runs cheap.
var (
	buildMu   sync.Mutex
	buildDir  string
	buildOuts = map[string]string{}
)

func buildWasm(t *testing.T, pkg string) string {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping wasm build in -short mode")
	}
	buildMu.Lock()
	defer buildMu.Unlock()
	if out, ok := buildOuts[pkg]; ok {
		return out
	}
	if buildDir == "" {
		dir, err := os.MkdirTemp("", "termcade-wasm-*")
		if err != nil {
			t.Fatal(err)
		}
		buildDir = dir
	}
	out := filepath.Join(buildDir, strings.ReplaceAll(pkg, "/", "-")+".wasm")
	cmd := exec.Command("go", "build", "-buildmode=c-shared", "-o", out,
		"github.com/aviorstudio/termcade/"+pkg)
	cmd.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm")
	if raw, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("building %s: %v\n%s", pkg, err, raw)
	}
	buildOuts[pkg] = out
	return out
}

func loadGame(t *testing.T, pkg string, info sdk.Info) sdk.Game {
	t.Helper()
	raw, err := os.ReadFile(buildWasm(t, pkg))
	if err != nil {
		t.Fatal(err)
	}
	rt := NewRuntime(context.Background())
	t.Cleanup(func() { rt.Close() })
	g, err := rt.Load(info.ID, raw, info, sdk.Quadrant)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	t.Cleanup(func() {
		if c, ok := g.(interface{ Close() error }); ok {
			c.Close()
		}
	})
	return g
}

var brickInfo = sdk.Info{ID: "aviorstudio/brickough", Title: "BRICKOUGH", PixelW: 64, PixelH: 40}

// paddleCentroid finds the mean x of white pixels in the paddle's rows.
func paddleCentroid(t *testing.T, c *sdk.Canvas) float64 {
	t.Helper()
	fw, fh := c.PixelSize()
	sum, n := 0, 0
	for fy := fh - 4; fy < fh; fy++ {
		for fx := 0; fx < fw; fx++ {
			if c.AtPixel(fx, fy) == sdk.White {
				sum += fx
				n++
			}
		}
	}
	if n == 0 {
		t.Fatal("no paddle pixels found")
	}
	return float64(sum) / float64(n)
}

func TestE2EBrickoughPlays(t *testing.T) {
	g := loadGame(t, "games/brickough/cmd/wasm", brickInfo)
	c := sdk.NewCanvas(brickInfo.PixelW, brickInfo.PixelH, sdk.Black, sdk.Quadrant)

	g.Reset()
	g.Draw(c)
	start := paddleCentroid(t, c)

	// Hold right for a second of game time; the paddle must move right.
	g.HandleKey(sdk.KeyRight)
	for range sdk.TPS {
		g.HandleKey(sdk.KeyRight) // refresh the auto-repeat fallback window
		if g.Update() != sdk.StatusRunning {
			t.Fatal("game ended while moving the paddle")
		}
	}
	g.Draw(c)
	if moved := paddleCentroid(t, c); moved <= start {
		t.Errorf("paddle did not move right: centroid %v -> %v", start, moved)
	}

	h := g.HUD()
	found := false
	for _, f := range h.Fields {
		if f.Label == "SCORE" {
			found = true
		}
	}
	if !found {
		t.Errorf("HUD missing SCORE field: %+v", h)
	}
	if g.Score() != 0 {
		t.Errorf("score = %d before any brick hit", g.Score())
	}
}

func TestE2EBrickoughGameOver(t *testing.T) {
	g := loadGame(t, "games/brickough/cmd/wasm", brickInfo)
	g.Reset()
	g.HandleKey(sdk.KeyA) // launch the ball
	g.HandleKeyUp(sdk.KeyA)

	// Hide in the left corner, relaunching every drained ball; three lost
	// lives end the run.
	over := false
	for range sdk.TPS * 120 {
		g.HandleKey(sdk.KeyLeft)
		g.HandleKey(sdk.KeyA)
		if g.Update() == sdk.StatusGameOver {
			over = true
			break
		}
	}
	if !over {
		t.Fatal("game never ended")
	}
	if g.Score() < 0 {
		t.Errorf("score = %d at game over", g.Score())
	}
}

func TestE2EReplayAfterReset(t *testing.T) {
	g := loadGame(t, "games/brickough/cmd/wasm", brickInfo)
	g.Reset()
	for range 10 {
		g.Update()
	}
	g.Reset()
	c := sdk.NewCanvas(brickInfo.PixelW, brickInfo.PixelH, sdk.Black, sdk.Quadrant)
	g.Draw(c) // must not trap after a mid-run reset
}

func TestWatchdogKillsHungGuest(t *testing.T) {
	g := loadGame(t, "internal/plugin/testdata/hang", sdk.Info{
		ID: "test/hang", Title: "HANG", PixelW: 8, PixelH: 8,
	})
	g.Reset()

	start := time.Now()
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("hung Update returned instead of panicking")
		}
		if e := time.Since(start); e > 5*time.Second {
			t.Errorf("watchdog took %v, want < 5s", e)
		}
	}()
	g.Update() // never returns from the guest; the deadline must kill it
}

func TestCompileRejectsGarbage(t *testing.T) {
	rt := NewRuntime(context.Background())
	defer rt.Close()
	if _, err := rt.Compile("junk", []byte("not wasm at all")); err == nil {
		t.Fatal("garbage bytes compiled")
	}
}

func TestLoadRejectsWrongPlayfield(t *testing.T) {
	raw, err := os.ReadFile(buildWasm(t, "games/brickough/cmd/wasm"))
	if err != nil {
		t.Fatal(err)
	}
	rt := NewRuntime(context.Background())
	defer rt.Close()
	lying := sdk.Info{ID: "aviorstudio/brickough", Title: "X", PixelW: 10, PixelH: 10}
	if _, err := rt.Load("lying", raw, lying, sdk.Quadrant); err == nil {
		t.Fatal("manifest/playfield mismatch accepted")
	}
}
