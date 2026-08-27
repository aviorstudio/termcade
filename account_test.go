package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/aviorstudio/termcade/internal/registry"
)

func TestLoginDisplaysOnlyPairingMaterialAndPersistsIssuedToken(t *testing.T) {
	const (
		device = "tcd_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
		token  = "tcc_BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB"
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/device/start":
			json.NewEncoder(w).Encode(map[string]any{
				"device_code": device, "user_code": "BCDF-GHJK",
				"verification_uri": registry.PairingURI, "expires_in": 30, "interval": 1,
			})
		case "/v1/device/poll":
			json.NewEncoder(w).Encode(map[string]any{
				"status": "approved", "token": token, "credential_id": "credential-id",
				"expires_at": time.Now().Add(30 * 24 * time.Hour).Format(time.RFC3339),
			})
		case "/v1/me":
			json.NewEncoder(w).Encode(map[string]any{"email": "player@example.test", "username": "player", "orgs": []any{}, "handles": []any{}})
		case "/v1/library":
			json.NewEncoder(w).Encode([]any{})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	t.Setenv("TERMCADE_REGISTRY", srv.URL)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	var out bytes.Buffer
	if err := cmdLoginContext(context.Background(), nil, &out); err != nil {
		t.Fatal(err)
	}
	displayed := out.String()
	for _, want := range []string{registry.PairingURI, "BCDF-GHJK", "logged in as player@example.test"} {
		if !strings.Contains(displayed, want) {
			t.Errorf("login output missing %q: %s", want, displayed)
		}
	}
	if strings.Contains(displayed, "tcd_") || strings.Contains(displayed, "tcc_") {
		t.Fatalf("login output exposed credential material: %s", displayed)
	}
	session, err := registry.LoadSession()
	if err != nil || session == nil || session.Token != token {
		t.Fatalf("saved session = %#v, %v", session, err)
	}
}

func TestLoginCancellationIsClean(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := cmdLoginContext(ctx, nil, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "login canceled") || !errors.Is(context.Cause(ctx), context.Canceled) {
		t.Fatalf("cancel error = %v", err)
	}
}
