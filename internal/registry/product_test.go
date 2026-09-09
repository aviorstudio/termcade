package registry

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestProductReadAndCredentialContracts(t *testing.T) {
	var calls []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.RequestURI())
		switch r.URL.Path {
		case "/v1/usernames/acme":
			json.NewEncoder(w).Encode(map[string]any{"name": "acme", "is_org": true, "bio": "Studio", "link": "https://example.test", "games": []Game{{ID: "acme/game", Name: "Game", HasPackage: true, ABI: 1, Width: 32, Height: 40}}})
		case "/v1/games/acme/game":
			json.NewEncoder(w).Encode(Game{ID: "acme/game", Name: "Game", Repo: "https://github.com/acme/game", Version: "1.2.3", HasPackage: true, ABI: 1, Width: 32, Height: 40})
		case "/v1/games/acme/game/plays", "/v1/usernames/acme/plays":
			if r.URL.Query().Get("days") != "30" {
				t.Error("wrong day window")
			}
			json.NewEncoder(w).Encode(map[string]any{"days": []PlayDay{{Day: "2026-09-01", Count: 3}, {Day: "2026-09-02", Count: 0}}})
		case "/v1/cli-credentials":
			json.NewEncoder(w).Encode([]CLICredential{{ID: "cred-1", ClientID: "termcade-cli", DeviceName: "Laptop", ExpiresAt: "2026-10-01T00:00:00Z", CreatedAt: "2026-09-01T00:00:00Z", LastUsedAt: "2026-09-02T00:00:00Z"}})
		case "/v1/cli-credentials/cred-1":
			if r.Method != http.MethodDelete {
				t.Error("wrong revoke verb")
			}
			w.WriteHeader(204)
		default:
			t.Error("unexpected endpoint", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	c := New(srv.URL, testToken).WithContext(context.Background())
	o, err := c.Owner("acme")
	if err != nil || !o.IsOrg || o.Bio != "Studio" || len(o.Games) != 1 || o.Link == "" {
		t.Fatal("owner contract", err)
	}
	g, err := c.Game("acme", "game")
	if err != nil || g.Version != "1.2.3" || g.ABI != 1 || g.Width != 32 || !g.HasPackage {
		t.Fatal("game contract", err)
	}
	for _, slug := range []string{"game", ""} {
		days, err := c.PlayDays("acme", slug, 30)
		if err != nil || len(days) != 2 || days[0].Count != 3 || days[1].Count != 0 {
			t.Fatal("play-day contract", err)
		}
	}
	creds, err := c.CLICredentials()
	if err != nil || len(creds) != 1 || creds[0].DeviceName != "Laptop" || creds[0].ExpiresAt == "" || creds[0].CreatedAt == "" || creds[0].LastUsedAt == "" {
		t.Fatal("credential contract", err)
	}
	if err := c.RevokeCLICredential("cred-1"); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 6 {
		t.Fatal(calls)
	}
	for _, days := range []int{0, 367} {
		if _, err = c.PlayDays("acme", "game", days); err == nil {
			t.Fatal("unbounded chart request")
		}
	}
}

func TestProductContextCancellationAndOriginBinding(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := New("http://127.0.0.1:1", testToken).WithContext(ctx).Owner("acme"); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation lost", err)
	}
	reached := false
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { reached = true; w.WriteHeader(200) }))
	defer destination.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, destination.URL, 302) }))
	defer source.Close()
	if _, err := New(source.URL, testToken).WithContext(context.Background()).Me(); err == nil || reached {
		t.Fatal("cross-origin credential redirect accepted")
	}
}

func TestProductDetailIdentityCannotChangeActionTargets(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/games/") {
			json.NewEncoder(w).Encode(Game{ID: "other/game", Name: "Other"})
		} else {
			json.NewEncoder(w).Encode(HandleOwner{Name: "other"})
		}
	}))
	defer srv.Close()
	c := New(srv.URL, "")
	if _, err := c.Game("acme", "game"); err == nil {
		t.Fatal("mismatched game accepted")
	}
	if _, err := c.Owner("acme"); err == nil {
		t.Fatal("mismatched owner accepted")
	}
}

func TestTUIStoreLocalIsolationAndLegacyCompatibility(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", root)
	t.Setenv("HOME", root)
	t.Setenv("AppData", root)
	t.Setenv("TERMCADE_REGISTRY", "http://127.0.0.1:8080")
	t.Setenv("TERMCADE_DEV_APP_URL", "http://127.0.0.1:8081")
	if err := SaveSession(Session{Registry: DefaultURL, Token: testToken, Email: "production@example.test"}); err != nil {
		t.Fatal(err)
	}
	store, err := NewTUIStore()
	if err != nil {
		t.Fatal(err)
	}
	if store.PairingURL != "http://127.0.0.1:8081/pair" || !strings.Contains(store.Path, string(filepath.Separator)+"local"+string(filepath.Separator)) {
		t.Fatal("wrong local store")
	}
	if s, err := store.Load(); err != nil || s != nil {
		t.Fatal("local mode read production credentials")
	}
	session := Session{Registry: store.Registry, Token: testToken, CredentialID: "new-credential", ExpiresAt: "2026-10-01T00:00:00Z"}
	if err = store.Save(session); err != nil {
		t.Fatal(err)
	}
	s, err := store.Load()
	if err != nil || s.CredentialID != session.CredentialID {
		t.Fatal("credential metadata lost")
	}
	info, _ := os.Stat(store.Path)
	if info.Mode().Perm() != 0600 {
		t.Fatal("credentials not private")
	}
	if err = store.Clear(); err != nil {
		t.Fatal(err)
	}
	old, err := LoadSession()
	if err != nil || old == nil || old.Email != "production@example.test" {
		t.Fatal("local logout touched production session")
	}
	t.Setenv("TERMCADE_DEV_APP_URL", "")
	ordinary, err := NewTUIStore()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = ordinary.Load(); err == nil {
		t.Fatal("registry override reused unrelated credential")
	}
}

func TestTUILocalPairingRejectsUnsafeOrigins(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", root)
	t.Setenv("HOME", root)
	t.Setenv("AppData", root)
	for _, pair := range [][2]string{{DefaultURL, "http://127.0.0.1:8081"}, {"http://127.0.0.1:8080", "https://evil.test"}, {"http://localhost:8080", "http://localhost:8081"}, {"http://127.0.0.1:8080", "http://user@127.0.0.1:8081"}, {"http://127.0.0.1:8080", "http://127.0.0.1:8081/path"}, {"http://127.0.0.1:8080", "http://127.0.0.1:8081?x=1"}} {
		t.Setenv("TERMCADE_REGISTRY", pair[0])
		t.Setenv("TERMCADE_DEV_APP_URL", pair[1])
		if _, err := NewTUIStore(); err == nil {
			t.Fatal("unsafe pairing configuration accepted", pair)
		}
	}
}

func TestTUIRegistryDoesNotAcceptCredentialsInURLs(t *testing.T) {
	for _, value := range []string{"https://user:password@example.test", "https://example.test?token=secret", "https://example.test#secret", "file:///tmp/registry", "ftp://example.test"} {
		if err := validateTUIRegistry(value); err == nil || strings.Contains(err.Error(), "secret") {
			t.Fatal("unsafe registry URL handling")
		}
	}
	if err := validateTUIRegistry(DefaultURL); err != nil {
		t.Fatal(err)
	}
}

func TestTUICompleteDeviceRetriesVerificationWithoutLosingMetadata(t *testing.T) {
	var checks atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/device/poll":
			json.NewEncoder(w).Encode(DevicePoll{Status: "approved", Token: testToken, CredentialID: "verified-device", ExpiresAt: time.Now().Add(24 * time.Hour).Format(time.RFC3339)})
		case "/v1/me":
			if checks.Add(1) == 1 {
				http.Error(w, `{"message":"temporary"}`, 503)
				return
			}
			json.NewEncoder(w).Encode(Me{Email: "person@example.test", Username: "person"})
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	session, err := New(srv.URL, "").CompleteDevice(context.Background(), DeviceRound{DeviceCode: testDevice, ExpiresIn: 10 * time.Second, Interval: time.Millisecond})
	if err != nil || session.CredentialID != "verified-device" || checks.Load() != 2 {
		t.Fatal("verification recovery failed", err)
	}
}
