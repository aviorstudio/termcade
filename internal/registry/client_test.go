package registry

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/aviorstudio/termcade/sdk"
)

const pkg = "a package, as far as this test is concerned"

func digestOf(b string) string {
	sum := sha256.Sum256([]byte(b))
	return hex.EncodeToString(sum[:])
}

// stubRegistry answers resolve and download the way the API does. body is what
// /download returns; sha is what /resolve claims it hashes to, so a test can
// make the two disagree.
type stubRegistry struct {
	sha  string
	body string
	// downloadAuth records the Authorization header the download arrived with.
	downloadAuth string
	// resolveQuery records the query resolve arrived with.
	resolveQuery url.Values
	// assetHits counts requests to the GitHub URL resolve hands out. It must
	// stay zero: packages come through the registry now.
	assetHits int
	status    int
}

func (s *stubRegistry) serve(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/games/aviorstudio/brickough/resolve", func(w http.ResponseWriter, r *http.Request) {
		s.resolveQuery = r.URL.Query()
		json.NewEncoder(w).Encode(Resolved{
			ID: "aviorstudio/brickough", Name: "Brickough", Version: "1.0.0",
			Asset: "brickough.tcade", SHA256: s.sha, ABI: sdk.ABIVersion,
			// Deliberately pointing back at this same server, so a client
			// that still fetched from here would be caught by assetHits
			// rather than by a network error that looks like a flake.
			URL: "https://" + r.Host + "/asset",
		})
	})
	mux.HandleFunc("/v1/games/aviorstudio/brickough/download", func(w http.ResponseWriter, r *http.Request) {
		s.downloadAuth = r.Header.Get("Authorization")
		if s.status != 0 {
			w.WriteHeader(s.status)
			json.NewEncoder(w).Encode(apiMessage{Message: "nope"})
			return
		}
		w.Write([]byte(s.body))
	})
	mux.HandleFunc("/asset", func(w http.ResponseWriter, r *http.Request) {
		s.assetHits++
		w.Write([]byte(s.body))
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// The whole point of the change: a package arrives through the registry, with
// the session attached, and the GitHub URL resolve mentions is never fetched.
func TestDownloadComesThroughTheRegistry(t *testing.T) {
	stub := &stubRegistry{sha: digestOf(pkg), body: pkg}
	srv := stub.serve(t)

	path, err := New(srv.URL, "session-token").Download("aviorstudio", "brickough")
	if err != nil {
		t.Fatalf("download: %v", err)
	}
	defer os.Remove(path)

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != pkg {
		t.Errorf("downloaded %q, want the package", got)
	}
	if stub.downloadAuth != "session-token" {
		t.Errorf("download sent Authorization %q; installing is supposed to require an account", stub.downloadAuth)
	}
	if stub.assetHits != 0 {
		t.Errorf("the client fetched the GitHub asset %d times; packages come through the registry now", stub.assetHits)
	}
}

// The digest is the tie between what arrives and what the registry validated
// at publish time. A package that does not match it must not reach the disk.
func TestDownloadRefusesAMismatchedDigest(t *testing.T) {
	stub := &stubRegistry{sha: digestOf("what was published"), body: "something else entirely"}
	srv := stub.serve(t)

	path, err := New(srv.URL, "session-token").Download("aviorstudio", "brickough")
	if err == nil {
		os.Remove(path)
		t.Fatal("a package that did not match its digest was installed")
	}
	if path != "" {
		t.Errorf("a rejected download left %s behind", path)
	}
	if got := err.Error(); !strings.Contains(got, "checksum mismatch") {
		t.Errorf("error does not say what went wrong: %q", got)
	}
}

// A registry that publishes no digest leaves nothing to verify against, which
// is worse than refusing.
func TestDownloadRefusesAMissingDigest(t *testing.T) {
	stub := &stubRegistry{sha: "", body: pkg}
	srv := stub.serve(t)

	if _, err := New(srv.URL, "session-token").Download("aviorstudio", "brickough"); err == nil {
		t.Fatal("a package with no attested digest was installed")
	}
}

// An expired or absent session has to be legible as one, so the CLI and the
// TUI can say "sign in" rather than "HTTP 401".
func TestDownloadReportsAnUnauthorizedResponse(t *testing.T) {
	stub := &stubRegistry{sha: digestOf(pkg), body: pkg, status: http.StatusUnauthorized}
	srv := stub.serve(t)

	_, err := New(srv.URL, "").Download("aviorstudio", "brickough")
	if !errors.Is(err, ErrLoginRequired) {
		t.Fatalf("401 gave %v, want ErrLoginRequired", err)
	}
}

func TestNotFoundKeepsItsMessageAndCanBeClassified(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"message":"not in your library"}`))
	}))
	t.Cleanup(srv.Close)

	err := New(srv.URL, "session").LibraryRemove("aviorstudio", "tetris")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("error = %v, want ErrNotFound", err)
	}
	if err.Error() != "not in your library" {
		t.Fatalf("message = %q", err)
	}
}

// Resolve sends the ABI so the registry can pick a release this binary can
// run, and sends no version at all — pinning is gone.
func TestResolveSendsTheABIAndNoVersion(t *testing.T) {
	stub := &stubRegistry{sha: digestOf(pkg), body: pkg}
	srv := stub.serve(t)

	if _, err := New(srv.URL, "").Resolve("aviorstudio", "brickough"); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got := stub.resolveQuery.Get("abi"); got != strconv.Itoa(sdk.ABIVersion) {
		t.Errorf("abi = %q, want %d", got, sdk.ABIVersion)
	}
	if stub.resolveQuery.Has("version") {
		t.Errorf("resolve still asks for a version: %v", stub.resolveQuery)
	}
}
