package registry

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aviorstudio/termcade/sdk"
)

// The catalog is paged, so browsing the marketplace is several requests. The
// failure this guards against is quiet: a client that stops early shows a
// short marketplace and nothing anywhere says a game is missing.

// pagedCatalog serves n games, limit per page, in the registry's envelope.
func pagedCatalog(t *testing.T, n, perPage int) (*httptest.Server, *int) {
	t.Helper()
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		start := 0
		if cursor := r.URL.Query().Get("cursor"); cursor != "" {
			fmt.Sscanf(cursor, "at-%d", &start)
		}
		page := struct {
			Games []Game `json:"games"`
			Next  string `json:"next,omitempty"`
		}{}
		for i := start; i < n && i < start+perPage; i++ {
			page.Games = append(page.Games, Game{
				ID: fmt.Sprintf("aviorstudio/game-%03d", i), Name: "Game", HasPackage: true,
			})
		}
		if start+perPage < n {
			page.Next = fmt.Sprintf("at-%d", start+perPage)
		}
		json.NewEncoder(w).Encode(page)
	}))
	t.Cleanup(server.Close)
	return server, &requests
}

func TestGamesFollowsEveryCursor(t *testing.T) {
	server, requests := pagedCatalog(t, 47, 10)

	games, err := New(server.URL, "").Games()
	if err != nil {
		t.Fatalf("Games: %v", err)
	}
	if len(games) != 47 {
		t.Fatalf("got %d games, want all 47 — a page was dropped", len(games))
	}
	if *requests != 5 {
		t.Errorf("made %d requests for 5 pages", *requests)
	}
	seen := map[string]bool{}
	for _, g := range games {
		if seen[g.ID] {
			t.Fatalf("%s came back twice", g.ID)
		}
		seen[g.ID] = true
	}
}

// The end is an absent cursor. A page that happens to be empty in the middle
// of a walk — which the registry's compatibility filter can produce — is not
// the end, and stopping there loses everything after it.
func TestAnEmptyPageIsNotTheEnd(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("cursor") {
		case "":
			fmt.Fprint(w, `{"games":[],"next":"second"}`)
		case "second":
			fmt.Fprint(w, `{"games":[{"id":"aviorstudio/tetris","has_package":true}]}`)
		}
	}))
	defer server.Close()

	games, err := New(server.URL, "").Games()
	if err != nil {
		t.Fatalf("Games: %v", err)
	}
	if len(games) != 1 || games[0].ID != "aviorstudio/tetris" {
		t.Fatalf("an empty first page ended the walk: %v", games)
	}
}

// A registry that never stops handing out cursors must not hang the arcade.
func TestGamesStopsWalkingEventually(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"games":[{"id":"a/b","has_package":true}],"next":"forever"}`)
	}))
	defer server.Close()

	games, err := New(server.URL, "").Games()
	if err != nil {
		t.Fatalf("Games: %v", err)
	}
	if len(games) != maxCatalogPages {
		t.Errorf("walked %d pages, want it bounded at %d", len(games), maxCatalogPages)
	}
}

// Browsing asks for what this arcade can run. A marketplace full of entries
// that refuse to install is worse than a shorter one.
func TestGamesAsksForRunnableReleases(t *testing.T) {
	var query string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query = r.URL.RawQuery
		fmt.Fprint(w, `{"games":[]}`)
	}))
	defer server.Close()

	if _, err := New(server.URL, "").Games(); err != nil {
		t.Fatal(err)
	}
	if want := fmt.Sprintf("abi=%d", sdk.ABIVersion); !strings.Contains(query, want) {
		t.Errorf("catalog request %q does not carry %s", query, want)
	}
}

func TestCatalogQueryEncodesOnlyWhatWasSet(t *testing.T) {
	if got := (CatalogQuery{}).values().Encode(); got != "" {
		t.Errorf("an empty query encoded as %q", got)
	}
	got := CatalogQuery{Cursor: "c", Limit: 10, Search: "tet", ABI: 1, Sort: "newest"}.values().Encode()
	for _, want := range []string{"cursor=c", "limit=10", "q=tet", "abi=1", "sort=newest"} {
		if !strings.Contains(got, want) {
			t.Errorf("query %q is missing %s", got, want)
		}
	}
}

// ------------------------------------------------------------- contract --

// The examples in contract/ are copied from aviorstudio/termcade-be, which
// generates them from the API itself. Decoding them here is what stops this
// client's idea of the wire format drifting from the server's: a renamed field
// becomes a zero value, and a zero value looks exactly like a game with no
// release rather than like a bug.
func contractExample(t *testing.T, name string, out any) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("contract", name))
	if err != nil {
		t.Fatalf("reading the contract example: %v", err)
	}
	if err := json.Unmarshal(raw, out); err != nil {
		t.Fatalf("%s does not decode into %T: %v", name, out, err)
	}
}

func TestCatalogExampleDecodes(t *testing.T) {
	var page CatalogPage
	contractExample(t, "catalog.json", &page)

	if len(page.Games) == 0 {
		t.Fatal("the recorded catalog page has no games")
	}
	// The field the arcade has to learn to follow.
	if page.Next == "" {
		t.Error("the recorded page carries no cursor, so following one is untested")
	}

	game := page.Games[0]
	if !strings.Contains(game.ID, "/") {
		t.Errorf("id %q is not namespaced — the handle expansion is not reaching the wire", game.ID)
	}
	if game.Name == "" || !game.HasPackage {
		t.Errorf("catalog row decoded thin: %+v", game)
	}
	if game.Version == "" || game.SHA256 == "" {
		t.Errorf("release fields did not decode: %+v", game)
	}
	if game.CreatedAt == "" || game.ReleasedAt == "" {
		t.Errorf("timestamps did not decode: %+v", game)
	}
}

func TestGameExampleDecodes(t *testing.T) {
	var game Game
	contractExample(t, "game.json", &game)
	if !strings.Contains(game.ID, "/") || game.Name == "" {
		t.Errorf("game example decoded thin: %+v", game)
	}
}
