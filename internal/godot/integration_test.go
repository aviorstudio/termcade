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
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
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
		t.Fatal("packed imported Sprite2D did not render without a GPU")
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
	probe, _ := filepath.Abs("testdata/renderer_test.gd")
	output, err := runEngine(ctx, bin, "--headless", "--path", project, "--script", probe)
	if err != nil || strings.Count(output, "PASS termcade-godot renderer reachable=1") != 1 {
		t.Fatalf("real renderer assertions did not complete: %v; %s", err, output)
	}
	// Unsupported visuals must fail before creating any destination package.
	scene, _ := os.ReadFile(filepath.Join(project, "main.tscn"))
	scene = append(scene, []byte("\n[node name=\"Unsupported\" type=\"Camera2D\" parent=\".\"]\n")...)
	os.WriteFile(filepath.Join(project, "main.tscn"), scene, 0o644)
	badPack := filepath.Join(t.TempDir(), "unsupported.tgd")
	if err := Export(ctx, bin, project, badPack); err == nil || !strings.Contains(err.Error(), "Camera2D") {
		t.Fatalf("unsupported camera did not fail explicitly: %v", err)
	}
	if _, err := os.Stat(badPack); !os.IsNotExist(err) {
		t.Fatal("failed export left a shipping artifact")
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
		if abs(int(r)-int(tr)) < 3 && abs(int(g)-int(tg)) < 3 && abs(int(b)-int(tb)) < 3 {
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
	if err == nil || time.Since(start) > 2*time.Second {
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
