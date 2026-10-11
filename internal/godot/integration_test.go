package godot

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aviorstudio/termcade/sdk"
)

// Required by make test and CI. No skip/fake engine path: this exercises the
// actual export platform, packed resources and engine/terminal boundary.
func TestGodotExportAndTerminalRuntime(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	bin, err := Binary(ctx)
	if err != nil {
		t.Fatal(err)
	}
	project := t.TempDir()
	if err := copyProject("../../examples/godot/paddle", project); err != nil {
		t.Fatal(err)
	}
	original, _ := os.ReadFile(filepath.Join(project, "project.godot"))
	pack := filepath.Join(t.TempDir(), "paddle.tgd")
	if err := Export(ctx, bin, project, pack); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(filepath.Join(project, "project.godot"))
	if string(original) != string(after) {
		t.Fatal("export modified source project settings")
	}
	if _, err := os.Stat(filepath.Join(project, ".godot")); !os.IsNotExist(err) {
		t.Fatal("export created source import state")
	}
	if err := Export(ctx, bin, project, pack); err == nil {
		t.Fatal("export overwrote an existing artifact")
	}
	runtime, err := Start(ctx, bin, pack)
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	first, err := runtime.Reset(144, 40)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.Title != "Godot Terminal Paddle" {
		t.Fatalf("wrong project title %q", runtime.Title)
	}
	if colored(first) < 30 {
		t.Fatal("scene polygons, lines and sprite were not rendered")
	}
	if first[4*144+132] == 0 {
		t.Fatal("packed imported Sprite2D did not render through Godot")
	}
	frame := first
	for i := range 20 {
		var events []KeyEvent
		if i == 0 {
			events = []KeyEvent{{Code: 4194321, Down: true}}
		}
		frame, err = runtime.Step(events)
		if err != nil {
			t.Fatal(err)
		}
	}
	if centerOfColor(frame, 0x40c4c9) <= centerOfColor(first, 0x40c4c9)+15 {
		t.Fatalf("ordinary Godot Input and physics did not move the paddle: before %.2f, after %.2f, ball %.2f -> %.2f", centerOfColor(first, 0x40c4c9), centerOfColor(frame, 0x40c4c9), centerOfColor(first, 0xe6c945), centerOfColor(frame, 0xe6c945))
	}
	if centerOfColor(frame, 0xe6c945) == centerOfColor(first, 0xe6c945) {
		t.Fatal("Godot simulation did not advance")
	}
	canvas := sdk.NewCanvas(72, 40, sdk.Black, sdk.Quadrant)
	copy(canvas.Pix(), frame)
	if !strings.Contains(canvas.Render(), "\x1b[") {
		t.Fatal("frame did not become terminal output")
	}
	reset, err := runtime.Reset(144, 40)
	if err != nil {
		t.Fatal(err)
	}
	if centerOfColor(reset, 0x40c4c9) > 75 {
		t.Fatal("reset did not recreate the scene")
	}
	if err := runtime.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-runtime.done:
	default:
		t.Fatal("owned engine process survived cleanup")
	}
	if _, err := os.Stat(runtime.temp); !os.IsNotExist(err) {
		t.Fatal("owned runtime state survived cleanup")
	}
	// Editor installation follows the same public API and preserves unrelated settings.
	if err := InstallAddon(ctx, bin, project); err != nil {
		t.Fatal(err)
	}
	if err := InstallAddon(ctx, bin, project); err != nil {
		t.Fatal("addon setup is not idempotent:", err)
	}
	if _, err := runEngine(ctx, bin, "--headless", "--quiet", "--editor", "--path", project, "--import"); err != nil {
		t.Fatal("editor could not load the installed platform:", err)
	}
	// Cameras are ordinary Godot nodes now, not exporter compatibility errors.
	scene, _ := os.ReadFile(filepath.Join(project, "main.tscn"))
	scene = append(scene, []byte("\n[node name=\"Camera\" type=\"Camera2D\" parent=\".\"]\n")...)
	os.WriteFile(filepath.Join(project, "main.tscn"), scene, 0o644)
	if err := Export(ctx, bin, project, filepath.Join(t.TempDir(), "camera.tgd")); err != nil {
		t.Fatalf("ordinary camera was rejected: %v", err)
	}
	select {
	case <-runtime.display.done:
	default:
		t.Fatal("owned virtual display survived cleanup")
	}

}

func colored(pixels []sdk.Color) int {
	n := 0
	for _, pixel := range pixels {
		if pixel != 0 {
			n++
		}
	}
	return n
}

func centerOfColor(pixels []sdk.Color, target sdk.Color) float64 {
	total, count := 0, 0
	tr, tg, tb := target.RGB()
	for index, pixel := range pixels {
		r, g, b := pixel.RGB()
		if abs(int(r)-int(tr)) < 40 && abs(int(g)-int(tg)) < 40 && abs(int(b)-int(tb)) < 40 {
			total += index % 144
			count++
		}
	}
	if count == 0 {
		return -1
	}
	return float64(total) / float64(count)
}

func abs(value int) int {
	if value < 0 {
		return -value
	}
	return value
}

func TestGodotHungGameIsKilledAndCleanedUp(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	bin, err := Binary(ctx)
	if err != nil {
		t.Fatal(err)
	}
	project := t.TempDir()
	if err := copyProject("../../examples/godot/paddle", project); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(project, "game.gd")
	data, _ := os.ReadFile(path)
	data = []byte(strings.Replace(string(data), "func _physics_process(delta: float) -> void:", "func _physics_process(delta: float) -> void:\n\tif Input.is_key_pressed(KEY_B):\n\t\twhile true:\n\t\t\tpass", 1))
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	pack := filepath.Join(t.TempDir(), "hung.tgd")
	if err := Export(ctx, bin, project, pack); err != nil {
		t.Fatal(err)
	}
	runtime, err := Start(ctx, bin, pack)
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	if _, err := runtime.Reset(144, 40); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	_, err = runtime.Step([]KeyEvent{{Code: 66, Down: true}})
	if err == nil { // Physics sees input on the following fixed engine iteration.
		_, err = runtime.Step(nil)
	}
	if err == nil || time.Since(start) > 3500*time.Millisecond {
		t.Fatalf("hung game escaped its frame watchdog: %v", err)
	}
	select {
	case <-runtime.done:
	default:
		t.Fatal("watchdog did not reap its own process")
	}
	if _, err := os.Stat(runtime.temp); !os.IsNotExist(err) {
		t.Fatal("watchdog left owned runtime state")
	}
}

func TestGodotNativeRenderingAndSceneTransitions(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	bin, err := Binary(ctx)
	if err != nil {
		t.Fatal(err)
	}
	pack := filepath.Join(t.TempDir(), "framebuffer.tgd")
	if err := Export(ctx, bin, "testdata/framebuffer", pack); err != nil {
		t.Fatal(err)
	}
	r, err := Start(ctx, bin, pack)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	pixels, err := r.Reset(160, 80)
	if err != nil {
		t.Fatal(err)
	}
	// Use square pixels so test regions match the project's authored viewport.
	for range 3 {
		pixels, err = r.StepCanvas(nil, 160, 80, 1)
		if err != nil {
			t.Fatal(err)
		}
	}
	for name, target := range map[string]sdk.Color{"custom draw": 0xff0000, "canvas shader": 0x00ff00, "animated sprite": 0x0000ff, "3D viewport": 0x00ffff, "UI text": 0xffffff} {
		count := 0
		for _, pixel := range pixels {
			pr, pg, pb := pixel.RGB()
			tr, tg, tb := target.RGB()
			if abs(int(pr)-int(tr)) < 20 && abs(int(pg)-int(tg)) < 20 && abs(int(pb)-int(tb)) < 20 {
				count++
			}
		}
		if count < 10 {
			t.Errorf("%s did not render: %d matching pixels", name, count)
		}
	}
	// Low-resolution rendering preserves authored coordinates and gameplay.
	if _, err = r.StepCanvas(nil, 80, 40, 1); err != nil {
		t.Fatal(err)
	}
	if r.LastRenderSize[0] > 80 || r.LastRenderSize[1] > 40 || r.LastRenderSize[0] < 1 {
		t.Fatalf("render target stayed oversized: %v", r.LastRenderSize)
	}
	// Capture resize preserves the scene, focused button and autoload state.
	if _, err = r.StepCanvas(nil, 320, 160, 1); err != nil {
		t.Fatal(err)
	}
	if _, err = r.Step([]KeyEvent{{Code: 4194309, Down: true}}); err != nil {
		t.Fatal(err)
	}
	if _, err = r.Step([]KeyEvent{{Code: 4194309, Down: false}}); err != nil {
		t.Fatal(err)
	}
	for range 4 {
		pixels, err = r.Step(nil)
		if err != nil {
			t.Fatal(err)
		}
	}
	magenta := 0
	for _, pixel := range pixels {
		if pixel == 0xff00ff {
			magenta++
		}
	}
	if magenta < len(pixels)/3 {
		t.Fatal("focused UI, autoload state or scene transition failed")
	}
	// Games that depend on the original render target can opt out without
	// changing their source or the terminal capture dimensions.
	r.Close()
	t.Setenv("TERMCADE_GODOT_FULL_RES", "1")
	native, err := Start(ctx, bin, pack)
	if err != nil {
		t.Fatal(err)
	}
	defer native.Close()
	if _, err = native.Reset(80, 40); err != nil {
		t.Fatal(err)
	}
	if native.LastRenderSize != [2]int{160, 80} {
		t.Fatalf("native-resolution override ignored: %v", native.LastRenderSize)
	}

}
