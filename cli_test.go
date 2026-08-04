package main

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/aviorstudio/termcade/internal/registry"
)

func TestAddAcceptsMarketplaceIDsOnly(t *testing.T) {
	for _, input := range []string{"game.tcade", "https://example.test/game.tcade", "./game.tcade"} {
		err := cmdAdd([]string{input})
		if err == nil || !strings.Contains(err.Error(), "dev install") {
			t.Errorf("cmdAdd(%q) = %v, want dev-install guidance", input, err)
		}
	}
}

func TestExpiredSessionDoesNotPretendLibraryRemovalSucceeded(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(srv.Close)

	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if err := registry.SaveSession(registry.Session{Registry: srv.URL, Token: "expired"}); err != nil {
		t.Fatal(err)
	}
	err := syncLibraryRemove("aviorstudio", "tetris")
	if err == nil || !strings.Contains(err.Error(), "session has expired") {
		t.Fatalf("remove = %v, want expired-session error", err)
	}
}

func TestMarketplaceAddRequestOrderIsStable(t *testing.T) {
	// Guard the exact invariant independently of any later diagnostic request:
	// choosing the game is durable before attempting to fill the local cache.
	want := []string{"PUT /v1/library/aviorstudio/tetris", "GET /v1/games/aviorstudio/tetris/resolve"}
	var got []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = append(got, r.Method+" "+r.URL.Path)
		if r.Method == http.MethodPut {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		http.Error(w, `{"message":"stop"}`, http.StatusBadGateway)
	}))
	t.Cleanup(srv.Close)
	_ = addFromRegistry(&registry.Session{Registry: srv.URL, Token: "session"}, "aviorstudio/tetris")
	if !slices.Equal(got, want) {
		t.Fatalf("requests = %v, want %v", got, want)
	}
}
