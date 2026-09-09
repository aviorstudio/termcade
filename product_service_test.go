package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/aviorstudio/termcade/internal/plugin"
	"github.com/aviorstudio/termcade/internal/registry"
	"github.com/aviorstudio/termcade/internal/scores"
	"github.com/aviorstudio/termcade/internal/shell"
	"github.com/aviorstudio/termcade/manifest"
)

type productAPIFixture struct {
	mu            sync.Mutex
	calls         []string
	member        bool
	revokeFailure bool
}

func productBackendFixture(t *testing.T) (*productBackend, *productAPIFixture, string) {
	t.Helper()
	config := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", config)
	t.Setenv("HOME", config)
	t.Setenv("AppData", config)
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	f := &productAPIFixture{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.calls = append(f.calls, r.Method+" "+r.URL.Path)
		g := registry.Game{ID: "acme/game", Name: "Game", Version: "1.0.0", ABI: 1, HasPackage: true}
		switch r.URL.Path {
		case "/v1/games":
			if r.Header.Get("Authorization") != "" {
				t.Error("catalog received credentials")
			}
			if r.URL.Query().Get("abi") != "" {
				t.Error("web-aligned catalog was filtered")
			}
			json.NewEncoder(w).Encode(registry.CatalogPage{Games: []registry.Game{g}})
		case "/v1/me":
			json.NewEncoder(w).Encode(registry.Me{Email: "local@example.test", Username: "local"})
		case "/v1/library":
			games := []registry.Game{}
			if f.member {
				games = append(games, g)
			}
			json.NewEncoder(w).Encode(games)
		case "/v1/activity":
			json.NewEncoder(w).Encode([]registry.Activity{})
		case "/v1/library/acme/game":
			if r.Method == http.MethodPut {
				f.member = true
			} else if r.Method == http.MethodDelete {
				f.member = false
			} else {
				t.Error("unexpected library verb")
			}
			w.WriteHeader(204)
		case "/v1/cli-credentials/current":
			if r.Method != http.MethodDelete {
				t.Error("revoke verb")
			}
			if f.revokeFailure {
				http.Error(w, `{"message":"unavailable"}`, 503)
			} else {
				w.WriteHeader(204)
			}
		default:
			t.Error("unexpected product request", r.Method, r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(srv.Close)
	t.Setenv("TERMCADE_REGISTRY", srv.URL)
	t.Setenv("TERMCADE_DEV_APP_URL", "http://127.0.0.1:8081")
	store, err := registry.NewTUIStore()
	if err != nil {
		t.Fatal(err)
	}
	if err = store.Save(registry.Session{Registry: srv.URL, Email: "local@example.test", Token: "tcc_" + strings.Repeat("A", 43), CredentialID: "current"}); err != nil {
		t.Fatal(err)
	}
	st, err := scores.Load()
	if err != nil {
		t.Fatal(err)
	}
	rt := plugin.NewRuntime(context.Background())
	t.Cleanup(func() { rt.Close() })
	backend := &productBackend{store: store, runtime: rt, scores: st}
	dir, err := plugin.GamesDir()
	if err != nil {
		t.Fatal(err)
	}
	return backend, f, filepath.Join(dir, "acme", "game")
}

func TestTUIAddAndSignInDoNotDownload(t *testing.T) {
	p, f, _ := productBackendFixture(t)
	for _, req := range []shell.ProductRequest{{Kind: "load"}, {Kind: "add", ID: "acme/game"}} {
		reply, err := p.request(context.Background(), req)
		if err != nil {
			t.Fatal(err)
		}
		if reply.Snapshot == nil || !reply.Snapshot.SignedIn {
			t.Fatal("missing account snapshot")
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.member {
		t.Fatal("Add did not save membership")
	}
	for _, call := range f.calls {
		if strings.Contains(call, "resolve") || strings.Contains(call, "download") {
			t.Fatal("metadata flow downloaded a package")
		}
	}
}

func TestTUIRemoveAndUninstallHaveSeparateEffects(t *testing.T) {
	p, f, dir := productBackendFixture(t)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, manifest.FileName)
	if err := os.WriteFile(file, []byte("broken local fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	f.member = true
	if _, err := p.request(context.Background(), shell.ProductRequest{Kind: "remove", ID: "acme/game"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(file); err != nil {
		t.Fatal("Remove deleted files", err)
	}
	f.mu.Lock()
	f.member = true
	before := len(f.calls)
	f.mu.Unlock()
	if _, err := p.request(context.Background(), shell.ProductRequest{Kind: "uninstall", ID: "acme/game"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("Uninstall left the copy")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.member {
		t.Fatal("Uninstall removed account membership")
	}
	for _, call := range f.calls[before:] {
		if strings.HasPrefix(call, "DELETE /v1/library") {
			t.Fatal("Uninstall changed remote membership")
		}
	}
}

func TestTUISignOutClearsEvenWhenRevokeFailsAndLegacyNeverGuesses(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		t.Run(map[bool]string{false: "failed revoke", true: "legacy"}[legacy], func(t *testing.T) {
			p, f, _ := productBackendFixture(t)
			f.revokeFailure = true
			if legacy {
				s, _ := p.store.Load()
				s.CredentialID = ""
				if err := p.store.Save(*s); err != nil {
					t.Fatal(err)
				}
			}
			reply, err := p.request(context.Background(), shell.ProductRequest{Kind: "logout"})
			if err != nil {
				t.Fatal(err)
			}
			s, err := p.store.Load()
			if err != nil || s != nil {
				t.Fatal("local credentials retained")
			}
			if reply.Notice == "" || reply.Snapshot.SignedIn {
				t.Fatal("sign-out failure hidden")
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			revokes := 0
			for _, call := range f.calls {
				if strings.HasPrefix(call, "DELETE /v1/cli-credentials/") {
					revokes++
				}
			}
			if (!legacy && revokes != 1) || (legacy && revokes != 0) {
				t.Fatal("wrong revocation target count", revokes)
			}
		})
	}
}

func TestTUIRevokeSelfAndCanceledMutation(t *testing.T) {
	p, f, _ := productBackendFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := p.request(ctx, shell.ProductRequest{Kind: "add", ID: "acme/game"}); !errors.Is(err, context.Canceled) {
		t.Fatal("canceled operation ran", err)
	}
	if len(f.calls) != 0 {
		t.Fatal("canceled operation contacted API")
	}
	reply, err := p.request(context.Background(), shell.ProductRequest{Kind: "session-revoke", ID: "current"})
	if err != nil || reply.Snapshot.SignedIn {
		t.Fatal("self revoke retained identity", err)
	}
}

func TestTUIInstallDoesNotOverwriteExistingCopy(t *testing.T) {
	p, f, dir := productBackendFixture(t)
	f.member = true
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	_, err := p.request(context.Background(), shell.ProductRequest{Kind: "install", ID: "acme/game"})
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatal("existing installation would be overwritten", err)
	}
}

func TestUnsafeProductLinksAreRefusedWithoutLaunching(t *testing.T) {
	for _, link := range []string{"javascript:alert(1)", "file:///tmp/example", "https://user:pass@example.test", ""} {
		if err := openProductURL(link); err == nil {
			t.Fatal("unsafe URL accepted")
		}
	}
}

func TestCanceledProductInstallCannotStartValidationOrWrite(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := installPackageBytesContext(ctx, []byte("not a package")); !errors.Is(err, context.Canceled) {
		t.Fatal("canceled install proceeded", err)
	}
}

func TestShippingMarketplaceUsesTheProductServicePath(t *testing.T) {
	p, f, _ := productBackendFixture(t)
	market := newMarketplace(p.runtime, p.scores)
	if market.Product == nil || market.Product.Request == nil {
		t.Fatal("main still uses legacy menu-only hooks")
	}
	if len(f.calls) != 0 {
		t.Fatal("constructing the TUI performed a network request")
	}
}
