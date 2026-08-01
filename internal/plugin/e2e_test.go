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

// The probe guest under internal/plugin/testdata/probe. These tests drove
// Tetris through the sandbox until the games moved to their own repository;
// a purpose-built guest is the better fixture anyway, because it makes the
// host's behaviour observable without anyone having to know a game's rules.
const probePkg = "internal/plugin/testdata/probe"

// Kept in step with the probe's own constants.
const probeGameOverTick = 30

var probeInfo = sdk.Info{ID: "test/probe", Title: "PROBE", PixelW: 16, PixelH: 8}

func newCanvas() *sdk.Canvas {
	return sdk.NewCanvas(probeInfo.PixelW, probeInfo.PixelH, sdk.Black, sdk.Quadrant)
}

// litPixels counts everything the guest painted.
func litPixels(c *sdk.Canvas) int {
	fw, fh := c.PixelSize()
	n := 0
	for fy := range fh {
		for fx := range fw {
			if c.AtPixel(fx, fy) != sdk.Black {
				n++
			}
		}
	}
	return n
}

// TestE2EGuestRoundTrip drives every export the ABI defines and checks that
// what the guest computed is what the host reads back: ticks it counted,
// pixels it drew into its own linear memory, a score moved by a keypress, and
// a HUD marshalled across as JSON.
func TestE2EGuestRoundTrip(t *testing.T) {
	g := loadGame(t, probePkg, probeInfo)
	c := newCanvas()

	g.Reset()
	g.Draw(c)
	if lit := litPixels(c); lit != 0 {
		t.Fatalf("canvas not empty after Reset: %d pixels", lit)
	}

	// The probe paints one pixel per elapsed tick, so the pixel buffer is a
	// direct readout of guest state crossing the boundary.
	const ticks = 5
	for range ticks {
		if s := g.Update(); s != sdk.StatusRunning {
			t.Fatalf("ended early at tick %d", ticks)
		}
	}
	g.Draw(c)
	if lit := litPixels(c); lit != ticks {
		t.Errorf("read %d pixels back from the guest, want %d", lit, ticks)
	}

	if g.Score() != 0 {
		t.Errorf("score = %d before any keypress", g.Score())
	}
	g.HandleKey(sdk.KeyA)
	if g.Score() != 10 {
		t.Errorf("score = %d after KeyA, want 10 — the keypress did not reach the guest", g.Score())
	}
	g.Draw(c)
	if lit := litPixels(c); lit != ticks+1 {
		t.Errorf("held key not drawn: %d pixels, want %d", lit, ticks+1)
	}

	// Key release is reported separately, and terminals that cannot send it
	// simply never call this — so the host must carry it when they can.
	g.HandleKeyUp(sdk.KeyA)
	g.Draw(c)
	if lit := litPixels(c); lit != ticks {
		t.Errorf("key release did not reach the guest: %d pixels, want %d", lit, ticks)
	}

	h := g.HUD()
	if len(h.Fields) != 1 || h.Fields[0].Label != "SCORE" || h.Fields[0].Value != "10" {
		t.Errorf("HUD did not survive the crossing: %+v", h)
	}
}

func TestE2EGuestGameOver(t *testing.T) {
	g := loadGame(t, probePkg, probeInfo)
	g.Reset()

	for i := 1; i < probeGameOverTick; i++ {
		if g.Update() != sdk.StatusRunning {
			t.Fatalf("reported game over at tick %d, want %d", i, probeGameOverTick)
		}
	}
	if g.Update() != sdk.StatusGameOver {
		t.Fatalf("still running at tick %d", probeGameOverTick)
	}
}

// TestE2EReplayAfterReset covers the arcade's Restart: an instance that has
// already ended has to come back, in the same wasm instance, without trapping.
func TestE2EReplayAfterReset(t *testing.T) {
	g := loadGame(t, probePkg, probeInfo)
	g.Reset()
	for range probeGameOverTick {
		g.Update()
	}

	g.Reset()
	c := newCanvas()
	g.Draw(c)
	if lit := litPixels(c); lit != 0 {
		t.Fatalf("Reset left %d pixels behind", lit)
	}
	if g.Score() != 0 {
		t.Errorf("Reset left score at %d", g.Score())
	}
	if g.Update() != sdk.StatusRunning {
		t.Error("still reporting game over after Reset")
	}
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
	raw, err := os.ReadFile(buildWasm(t, probePkg))
	if err != nil {
		t.Fatal(err)
	}
	rt := NewRuntime(context.Background())
	defer rt.Close()
	// The manifest is the authority on identity, so a manifest that disagrees
	// with the module it ships is refused rather than believed.
	lying := sdk.Info{ID: "test/probe", Title: "X", PixelW: 10, PixelH: 10}
	if _, err := rt.Load("lying", raw, lying, sdk.Quadrant); err == nil {
		t.Fatal("manifest/playfield mismatch accepted")
	}
}
