package main

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/aviorstudio/termcade/internal/godot"
	"github.com/aviorstudio/termcade/sdk"
)

func TestGodotTerminalKeysAndEscaping(t *testing.T) {
	for key, expected := range map[string]int{"left": 4194319, "right": 4194321, "up": 4194320, "down": 4194322, "enter": 4194309, "a": 65, "space": 32, "unknown": 0} {
		if got := godotKey(key); got != expected {
			t.Errorf("%s: got %d, want %d", key, got, expected)
		}
	}
	if text := safeGodotText("title\x1b[2J\n\x07"); strings.ContainsAny(text, "\x1b\n\x07") {
		t.Fatal("game diagnostics can inject terminal controls")
	}
}

func TestGodotPlayerPauseQueuesReleaseAndResizeRetainsFrame(t *testing.T) {
	m := godotPlayer{held: map[int]int{65: 0}, canvas: sdk.NewCanvas(72, 40, sdk.Black, sdk.Quadrant), runtime: &godot.Runtime{Title: "game"}}
	model, _ := m.Update(tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})
	paused := model.(godotPlayer)
	if !paused.paused || len(paused.held) != 0 || len(paused.keys) != 1 || paused.keys[0].Down {
		t.Fatal("pause left a game key held")
	}
	model, _ = paused.Update(tea.WindowSizeMsg{Width: 50, Height: 15})
	resized := model.(godotPlayer)
	if resized.canvas != paused.canvas || resized.width != 50 {
		t.Fatal("resize discarded the scene framebuffer")
	}
	model, _ = resized.Update(tea.KeyReleaseMsg{Code: 'a'})
	if !model.(godotPlayer).exact {
		t.Fatal("terminal release events did not activate exact input mode")
	}
}
