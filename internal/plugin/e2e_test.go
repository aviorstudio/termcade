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

var tetrisInfo = sdk.Info{ID: "aviorstudio/tetris", Title: "TETRIS", PixelW: 32, PixelH: 40}

// litPixels counts colored pixels in the bottom rows of the well. The ghost
// piece (DimGray) and the separator line sit there from the first frame;
// only a LOCKED piece paints real colors.
func litPixels(c *sdk.Canvas) int {
	_, fh := c.PixelSize()
	n := 0
	for fy := fh - 4; fy < fh; fy++ {
		for fx := range 40 { // the well is 20 logical units wide = 40 px
			if px := c.AtPixel(fx, fy); px != sdk.Black && px != sdk.DimGray {
				n++
			}
		}
	}
	return n
}

// hardDrop presses the drop button and runs enough ticks to clear the
// debounce, locking one piece per call.
func hardDrop(g sdk.Game) sdk.Status {
	g.HandleKey(sdk.KeyA)
	for range 13 { // dropCool is 12 ticks
		if s := g.Update(); s == sdk.StatusGameOver {
			return s
		}
	}
	return sdk.StatusRunning
}

func TestE2ETetrisPlays(t *testing.T) {
	g := loadGame(t, "games/tetris/cmd/wasm", tetrisInfo)
	c := sdk.NewCanvas(tetrisInfo.PixelW, tetrisInfo.PixelH, sdk.Black, sdk.Quadrant)

	g.Reset()
	g.Draw(c)
	if lit := litPixels(c); lit != 0 {
		t.Fatalf("well floor occupied at start: %d pixels", lit)
	}

	// One hard drop locks a piece on the floor.
	if hardDrop(g) != sdk.StatusRunning {
		t.Fatal("game ended on the first drop")
	}
	g.Draw(c)
	if lit := litPixels(c); lit == 0 {
		t.Error("hard-dropped piece left no pixels on the well floor")
	}
	if g.Score() <= 0 {
		t.Errorf("score = %d after a hard drop (drops score distance)", g.Score())
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
}

func TestE2ETetrisGameOver(t *testing.T) {
	g := loadGame(t, "games/tetris/cmd/wasm", tetrisInfo)
	g.Reset()

	// Dropping forever tops out the well in bounded time.
	over := false
	for range 500 {
		if hardDrop(g) == sdk.StatusGameOver {
			over = true
			break
		}
	}
	if !over {
		t.Fatal("game never ended")
	}
	if g.Score() <= 0 {
		t.Errorf("score = %d at game over", g.Score())
	}
}

func TestE2EReplayAfterReset(t *testing.T) {
	g := loadGame(t, "games/tetris/cmd/wasm", tetrisInfo)
	g.Reset()
	for range 10 {
		g.Update()
	}
	g.Reset()
	c := sdk.NewCanvas(tetrisInfo.PixelW, tetrisInfo.PixelH, sdk.Black, sdk.Quadrant)
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
	raw, err := os.ReadFile(buildWasm(t, "games/tetris/cmd/wasm"))
	if err != nil {
		t.Fatal(err)
	}
	rt := NewRuntime(context.Background())
	defer rt.Close()
	lying := sdk.Info{ID: "aviorstudio/tetris", Title: "X", PixelW: 10, PixelH: 10}
	if _, err := rt.Load("lying", raw, lying, sdk.Quadrant); err == nil {
		t.Fatal("manifest/playfield mismatch accepted")
	}
}
