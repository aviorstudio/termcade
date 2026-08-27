package registry

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSaveSessionIsAtomicAndAlways0600(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	path := filepath.Join(dir, "termcade", "session.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"token":"old"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := SaveSession(Session{Registry: "https://api.termca.de", Token: testToken}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("session permissions = %04o, want 0600", got)
	}
	loaded, err := LoadSession()
	if err != nil || loaded == nil || loaded.Token != testToken {
		t.Fatalf("loaded = %#v, %v", loaded, err)
	}
}

func TestSaveSessionRefusesOtherCredentialTypes(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	for _, token := range []string{"", "tck_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", "Bearer " + testToken} {
		if err := SaveSession(Session{Token: token}); err == nil {
			t.Fatalf("saved non-CLI credential %q", token[:min(len(token), 4)])
		}
	}
}
