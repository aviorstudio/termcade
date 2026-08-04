package main

import (
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/aviorstudio/termcade/internal/registry"
	"github.com/aviorstudio/termcade/internal/scores"
)

// Keeping one account's arcade in step across machines.
//
// Playing never waits on any of this. A run is recorded locally and queued;
// the queue drains later, from wherever the arcade happens to be when it next
// has a session and a network. Nothing here can fail in a way that costs a
// score: the local file is authoritative for playing, and the registry is
// authoritative only for what other machines did.
//
// Both directions merge upward. Sending a run cannot lower what the account
// holds, because the registry merges with max(); receiving cannot lower what
// this machine holds, because scores.Merge does the same. So the two can
// disagree, sync in either order, or never sync, and the worst outcome is that
// a high score stays where it was set.

// syncActivity flushes queued runs and folds the account's record into the
// local one. It reports how many runs it delivered, so a caller can say
// something only when there was something to say.
//
// Signed out is not a failure — it is the ordinary state of an arcade nobody
// has made an account for, and the queue simply waits.
func syncActivity(st *scores.Store) (sent int, err error) {
	session, err := registry.LoadSession()
	if err != nil || session == nil {
		return 0, nil
	}
	client := registry.New(registry.URL(session), session.Token)

	sent, err = flushRuns(client, st)
	if err != nil {
		// A run that did not land stays queued. Pulling the account's record
		// anyway would be reporting on a sync that half happened, so this stops
		// here and the next attempt does both.
		return sent, err
	}
	return sent, pullActivity(client, st)
}

// flushRuns delivers the queue oldest first, dropping each run only once the
// registry has acknowledged it.
//
// A rejection the registry will keep repeating — a malformed run, a game no
// longer in the catalog — drops the run rather than blocking everything behind
// it forever. Anything else stops the flush and leaves the rest queued.
func flushRuns(client *registry.Client, st *scores.Store) (int, error) {
	sent := 0
	for _, run := range st.Pending() {
		author, slug, ok := strings.Cut(run.Game, "/")
		if !ok {
			st.Sent(run.ID) // not an id the registry could ever accept
			continue
		}
		_, err := client.SubmitRun(author, slug, registry.Run{
			Run:       run.ID,
			Score:     run.Score,
			PlayedAt:  run.PlayedAt.UTC().Format(time.RFC3339),
			Completed: run.Completed,
			Version:   run.Version,
		})
		switch {
		case err == nil:
			st.Sent(run.ID)
			sent++
		case errors.Is(err, registry.ErrNotFound):
			// The game is gone from the catalog. Its local high score stays;
			// there is simply nowhere to record the run.
			st.Sent(run.ID)
		default:
			return sent, err
		}
	}
	return sent, nil
}

// pullActivity folds the account's record into the local file. Only upward:
// see the package comment.
func pullActivity(client *registry.Client, st *scores.Store) error {
	records, err := client.Activity()
	if err != nil {
		return err
	}
	for _, a := range records {
		st.Merge(a.ID, a.PersonalBest, a.PlayedAt())
	}
	return nil
}

// syncMessage turns a sync into something worth putting on a screen, or "" for
// the ordinary case where nothing happened and nobody needs telling.
//
// An expired session is the one failure a player can act on, so it is named.
// Everything else is the marketplace being unreachable, which the arcade
// already survives by design — the queue is still there and the next start
// tries again.
func syncMessage(sent int, err error) string {
	switch {
	case errors.Is(err, registry.ErrLoginRequired):
		return "your session has expired — press l in the marketplace to sign in again"
	case err != nil:
		return ""
	case sent == 1:
		return "1 run synced to your account"
	case sent > 1:
		return strconv.Itoa(sent) + " runs synced to your account"
	}
	return ""
}
