package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/aviorstudio/termcade/internal/registry"
	"github.com/aviorstudio/termcade/internal/scores"
)

// The sync is the only place where an arcade that works offline meets an
// account that might not be reachable. Every test here is a way that can go
// wrong without anybody noticing: a run delivered twice, a run silently
// dropped, a local high quietly replaced by a worse one from the account.

// fakeRegistry answers /v1/activity and records what was submitted.
type fakeRegistry struct {
	*httptest.Server
	submitted []registry.Run
	paths     []string
	// records is what GET /v1/activity returns.
	records []registry.Activity
	// fail, when set, is the status every submission gets.
	fail int
}

func newFakeRegistry(t *testing.T) *fakeRegistry {
	t.Helper()
	f := &fakeRegistry{}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.paths = append(f.paths, r.Method+" "+r.URL.Path)
		switch {
		case r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/v1/activity/"):
			if f.fail != 0 {
				w.WriteHeader(f.fail)
				json.NewEncoder(w).Encode(map[string]string{"message": "no"})
				return
			}
			var run registry.Run
			json.NewDecoder(r.Body).Decode(&run)
			f.submitted = append(f.submitted, run)
			json.NewEncoder(w).Encode(registry.Activity{ID: strings.TrimPrefix(r.URL.Path, "/v1/activity/")})
		case r.URL.Path == "/v1/activity":
			json.NewEncoder(w).Encode(f.records)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(f.Close)
	return f
}

// session points the arcade at the fake registry with a token, in a config
// directory of its own.
func session(t *testing.T, f *fakeRegistry) *scores.Store {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("TERMCADE_REGISTRY", f.URL)
	if err := registry.SaveSession(registry.Session{
		Registry: f.URL, Email: "p@t.dev", Token: "tcc_" + strings.Repeat("A", 43),
	}); err != nil {
		t.Fatalf("saving a session: %v", err)
	}
	st, err := scores.Load()
	if err != nil {
		t.Fatalf("loading scores: %v", err)
	}
	return st
}

func TestSyncSendsTheQueueAndClearsIt(t *testing.T) {
	f := newFakeRegistry(t)
	st := session(t, f)
	st.Record("aviorstudio/tetris", 900, "1.2.0", true)
	st.Record("aviorstudio/asteroid", 10, "", false)

	sent, err := syncActivity(st)
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if sent != 2 {
		t.Errorf("sent %d runs, want 2", sent)
	}
	if got := st.Pending(); len(got) != 0 {
		t.Errorf("queue not cleared: %v", got)
	}
	if len(f.submitted) != 2 {
		t.Fatalf("registry saw %d runs", len(f.submitted))
	}
	if f.submitted[0].Score != 900 || f.submitted[0].Version != "1.2.0" {
		t.Errorf("first run went out wrong: %+v", f.submitted[0])
	}
	if !f.submitted[0].Completed || f.submitted[1].Completed {
		t.Errorf("completion not carried: %+v %+v", f.submitted[0], f.submitted[1])
	}
	// The timestamp is the run's, not the sync's — an offline queue that
	// reported "now" on delivery would rewrite when everything happened.
	if _, err := time.Parse(time.RFC3339, f.submitted[0].PlayedAt); err != nil {
		t.Errorf("played_at %q is not RFC 3339: %v", f.submitted[0].PlayedAt, err)
	}
}

// A run stays queued until it is acknowledged, and the retry carries the same
// id — which is the entire reason retrying is safe.
func TestAFailedRunStaysQueuedUnderTheSameID(t *testing.T) {
	f := newFakeRegistry(t)
	f.fail = http.StatusInternalServerError
	st := session(t, f)
	st.Record("aviorstudio/tetris", 900, "1.2.0", true)
	queued := st.Pending()[0].ID

	if sent, err := syncActivity(st); err == nil || sent != 0 {
		t.Fatalf("a failing sync reported sent=%d err=%v", sent, err)
	}
	pending := st.Pending()
	if len(pending) != 1 || pending[0].ID != queued {
		t.Fatalf("run did not stay queued under its id: %v", pending)
	}

	f.fail = 0
	if sent, err := syncActivity(st); err != nil || sent != 1 {
		t.Fatalf("retry: sent=%d err=%v", sent, err)
	}
	if f.submitted[0].Run != queued {
		t.Errorf("retry used a new run id (%s, was %s) — the registry would count two plays",
			f.submitted[0].Run, queued)
	}
}

// A run for a game the catalog no longer has can never be delivered. Leaving
// it queued would block everything behind it forever.
func TestARunTheRegistryWillNeverAcceptIsDropped(t *testing.T) {
	f := newFakeRegistry(t)
	f.fail = http.StatusNotFound
	st := session(t, f)
	st.Record("aviorstudio/gone", 900, "", true)

	if _, err := syncActivity(st); err != nil {
		t.Fatalf("sync: %v", err)
	}
	if got := st.Pending(); len(got) != 0 {
		t.Errorf("undeliverable run still queued: %v", got)
	}
	// Its score was already local and stays that way.
	if got := st.High("aviorstudio/gone"); got != 900 {
		t.Errorf("high = %d, want 900", got)
	}
}

func TestSyncMergesTheAccountsRecordWithoutLoweringAnything(t *testing.T) {
	f := newFakeRegistry(t)
	f.records = []registry.Activity{
		// Played higher somewhere else.
		{ID: "aviorstudio/asteroid", PersonalBest: 5000,
			LastPlayed: time.Now().UTC().Format(time.RFC3339)},
		// The account is behind this machine.
		{ID: "aviorstudio/tetris", PersonalBest: 10,
			LastPlayed: time.Now().UTC().Add(-72 * time.Hour).Format(time.RFC3339)},
	}
	st := session(t, f)
	st.Record("aviorstudio/tetris", 900, "1.2.0", true)

	if _, err := syncActivity(st); err != nil {
		t.Fatalf("sync: %v", err)
	}
	if got := st.High("aviorstudio/tetris"); got != 900 {
		t.Errorf("local high = %d, want 900 — the account must not lower it", got)
	}
	if got := st.High("aviorstudio/asteroid"); got != 5000 {
		t.Errorf("high = %d, want the 5000 set on another machine", got)
	}
}

// Signed out is the ordinary state of an arcade nobody has made an account
// for. It must not be an error, and the queue must survive it.
func TestSyncSignedOutIsNotAFailure(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	st, _ := scores.Load()
	st.Record("aviorstudio/tetris", 900, "1.2.0", true)

	sent, err := syncActivity(st)
	if err != nil || sent != 0 {
		t.Fatalf("signed-out sync: sent=%d err=%v", sent, err)
	}
	if got := st.Pending(); len(got) != 1 {
		t.Errorf("signing out ate the queue: %v", got)
	}
}

func TestSyncMessageOnlySpeaksWhenThereIsSomethingToSay(t *testing.T) {
	if got := syncMessage(0, nil); got != "" {
		t.Errorf("a quiet sync said %q", got)
	}
	if got := syncMessage(0, registry.ErrUnreachable); got != "" {
		t.Errorf("being offline said %q; the arcade works offline", got)
	}
	if got := syncMessage(1, nil); got != "1 run synced to your account" {
		t.Errorf("one run: %q", got)
	}
	if got := syncMessage(4, nil); got != "4 runs synced to your account" {
		t.Errorf("several runs: %q", got)
	}
	// An expired session is the one failure somebody can do something about.
	if got := syncMessage(0, registry.ErrLoginRequired); !strings.Contains(got, "expired") {
		t.Errorf("expired session: %q", got)
	}
}
