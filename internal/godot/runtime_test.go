package godot

import (
	"bufio"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFrameValidation(t *testing.T) {
	valid := Frame{Width: 2, Height: 1, Pixels: base64.StdEncoding.EncodeToString([]byte{255, 0, 0, 0, 255, 0})}
	pixels, err := decodeFrame(valid, 2, 1)
	if err != nil || len(pixels) != 2 || pixels[0] != 0xff0000 || pixels[1] != 0x00ff00 {
		t.Fatalf("RGB frame: %v, %v", pixels, err)
	}
	for name, frame := range map[string]Frame{
		"error":              {Error: "unsupported Camera2D"},
		"changed dimensions": {Width: 3, Height: 1, Pixels: valid.Pixels},
		"short pixels":       {Width: 2, Height: 1, Pixels: "AAAA"},
		"bad base64":         {Width: 2, Height: 1, Pixels: "!!!!!!!!"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := decodeFrame(frame, 2, 1); err == nil {
				t.Fatal("accepted a malformed engine frame")
			}
		})
	}
	if _, err := decodeFrame(valid, 1<<30, 1<<30); err == nil {
		t.Fatal("unbounded allocation accepted")
	}
}

func TestBoundedProtocolAndErrorLogs(t *testing.T) {
	reader := bufio.NewReaderSize(strings.NewReader(strings.Repeat("x", maxMessage)+"\n"), maxMessage)
	if _, err := boundedLine(reader); err == nil {
		t.Fatal("accepted oversized protocol message")
	}
	log := &engineLog{}
	log.Write([]byte(strings.Repeat("a", 100000) + "SCRIPT ER"))
	log.Write([]byte("ROR: game failed"))
	text, failed := log.snapshot()
	if !failed || len(text) > 32<<10 {
		t.Fatal("split script error was lost or logs grew unbounded")
	}
}

func TestProjectCopyIsolatedAndSymlinksRejected(t *testing.T) {
	source := t.TempDir()
	destination := t.TempDir()
	for name, content := range map[string]string{"project.godot": "config_version=5", ".env": "private", "scene.tscn": "scene"} {
		if err := os.WriteFile(filepath.Join(source, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := copyProject(source, destination); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(destination, ".env")); !os.IsNotExist(err) {
		t.Fatal("hidden local state copied into export staging")
	}
	if err := os.Symlink(filepath.Join(source, "scene.tscn"), filepath.Join(source, "linked.tscn")); err != nil {
		t.Fatal(err)
	}
	if err := copyProject(source, t.TempDir()); err == nil {
		t.Fatal("symlink was followed")
	}
}

func TestAddonDoesNotOverwriteAuthorChanges(t *testing.T) {
	project := t.TempDir()
	if err := writeAddon(project); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(project, "addons", "termcade", "renderer.gd")
	if err := os.WriteFile(file, []byte("author edits"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writeAddon(project); err == nil {
		t.Fatal("edited addon overwritten")
	}
	data, _ := os.ReadFile(file)
	if string(data) != "author edits" {
		t.Fatal("author edits lost")
	}
}
