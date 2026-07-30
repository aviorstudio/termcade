package settings

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadMissingIsZero(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	s, err := Load()
	if err != nil || s.Pixels != "" {
		t.Fatalf("Load = %+v, %v", s, err)
	}
}

func TestRoundTrip(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if err := (Settings{Pixels: "ascii"}).Save(); err != nil {
		t.Fatal(err)
	}
	s, err := Load()
	if err != nil || s.Pixels != "ascii" {
		t.Fatalf("round trip = %+v, %v", s, err)
	}
}

func TestCorruptFileErrors(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	p := filepath.Join(dir, "termcade", "settings.json")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("{nope"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(); err == nil {
		t.Fatal("corrupt settings loaded silently")
	}
}
