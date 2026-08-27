package registry

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const (
	testDevice = "tcd_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	testToken  = "tcc_BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB"
)

func startResponse(overrides map[string]any) map[string]any {
	out := map[string]any{
		"device_code": testDevice, "user_code": "BCDF-GHJK",
		"verification_uri": PairingURI, "expires_in": 30, "interval": 1,
	}
	for key, value := range overrides {
		out[key] = value
	}
	return out
}

func fakePolicy() devicePolicy {
	now := time.Now()
	return devicePolicy{
		now: func() time.Time { return now },
		sleep: func(ctx context.Context, d time.Duration) error {
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
				now = now.Add(d)
				return nil
			}
		},
	}
}

func TestDeviceRoundPendingThenApproved(t *testing.T) {
	polls := 0
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.RequestURI())
		switch r.URL.Path {
		case "/v1/device/start":
			var body map[string]string
			json.NewDecoder(r.Body).Decode(&body)
			if body["client_id"] != clientID || body["device_name"] != "Termcade CLI" {
				t.Errorf("start body = %#v", body)
			}
			json.NewEncoder(w).Encode(startResponse(nil))
		case "/v1/device/poll":
			polls++
			var body map[string]string
			json.NewDecoder(r.Body).Decode(&body)
			if body["device_code"] != testDevice {
				t.Errorf("poll body omitted device code")
			}
			if polls == 1 {
				json.NewEncoder(w).Encode(DevicePoll{Status: "pending", Interval: 2})
				return
			}
			json.NewEncoder(w).Encode(DevicePoll{Status: "approved", Token: testToken,
				CredentialID: "credential-id", ExpiresAt: time.Now().Add(30 * 24 * time.Hour).Format(time.RFC3339)})
		}
	}))
	defer srv.Close()

	client := New(srv.URL, "")
	round, err := client.StartDevice(context.Background(), "Termcade CLI")
	if err != nil {
		t.Fatal(err)
	}
	approved, err := client.pollRound(context.Background(), round, fakePolicy())
	if err != nil || approved.Token != testToken || polls != 2 {
		t.Fatalf("approved = %#v, polls=%d, err=%v", approved, polls, err)
	}
	for _, path := range paths {
		if strings.Contains(path, "tcd_") || strings.Contains(path, "tcc_") {
			t.Fatalf("credential material appeared in URL %q", path)
		}
	}
}

func TestExpiredRoundRestartsWithBackoff(t *testing.T) {
	starts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/device/start":
			starts++
			response := startResponse(nil)
			response["device_code"] = "tcd_" + strings.Repeat(string(rune('A'+starts-1)), 43)
			json.NewEncoder(w).Encode(response)
		case "/v1/device/poll":
			if starts == 1 {
				json.NewEncoder(w).Encode(DevicePoll{Status: "expired"})
			} else {
				json.NewEncoder(w).Encode(DevicePoll{Status: "approved", Token: testToken,
					CredentialID: "credential-id", ExpiresAt: time.Now().Add(30 * 24 * time.Hour).Format(time.RFC3339)})
			}
		case "/v1/me":
			if r.Header.Get("Authorization") != "Bearer "+testToken {
				t.Errorf("authorization = %q", r.Header.Get("Authorization"))
			}
			json.NewEncoder(w).Encode(Me{Email: "player@example.test", Username: "player"})
		}
	}))
	defer srv.Close()

	displays := 0
	session, err := New(srv.URL, "").deviceLogin(context.Background(), "Termcade CLI", func(uri, code string) {
		displays++
		if uri != PairingURI || !userCodeRE.MatchString(code) {
			t.Errorf("unsafe display %q %q", uri, code)
		}
	}, fakePolicy())
	if err != nil || starts != 2 || displays != 2 || session.Token != testToken {
		t.Fatalf("session=%#v starts=%d displays=%d err=%v", session, starts, displays, err)
	}
}

func TestPollingRetriesTransientFailureUntilApproval(t *testing.T) {
	polls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		polls++
		if polls == 1 {
			http.Error(w, `{"message":"temporary\u001b[31m failure"}`, http.StatusServiceUnavailable)
			return
		}
		json.NewEncoder(w).Encode(DevicePoll{Status: "approved", Token: testToken,
			CredentialID: "credential-id", ExpiresAt: time.Now().Add(30 * 24 * time.Hour).Format(time.RFC3339)})
	}))
	defer srv.Close()

	round := DeviceRound{DeviceCode: testDevice, ExpiresIn: 30 * time.Second, Interval: time.Second}
	if _, err := New(srv.URL, "").pollRound(context.Background(), round, fakePolicy()); err != nil || polls != 2 {
		t.Fatalf("polls=%d err=%v", polls, err)
	}
}

func TestServerErrorTextIsTerminalSafe(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"message": "bad\x1b[2J\nrequest"})
	}))
	defer srv.Close()
	_, err := New(srv.URL, "").StartDevice(context.Background(), "Termcade CLI")
	if err == nil || strings.ContainsAny(err.Error(), "\x1b\n\r") || err.Error() != "bad[2Jrequest" {
		t.Fatalf("unsafe server error = %q", err)
	}
}

func TestOversizedDeviceResponseIsRejected(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"device_code":"` + strings.Repeat("A", maxJSONResponse) + `"}`))
	}))
	defer srv.Close()
	_, err := New(srv.URL, "").StartDevice(context.Background(), "Termcade CLI")
	if err == nil || err.Error() != "the marketplace returned malformed data" {
		t.Fatalf("oversized response error = %v", err)
	}
}

func TestHostileDeviceStartFieldsAreRejected(t *testing.T) {
	tests := []struct {
		name  string
		field string
		value any
		want  string
	}{
		{"code", "user_code", "BCDF-\x1b[2J", "malformed pairing code"},
		{"uri", "verification_uri", "https://evil.example/pair", "untrusted pairing address"},
		{"short expiry", "expires_in", 1, "unsafe pairing expiry"},
		{"long expiry", "expires_in", 901, "unsafe pairing expiry"},
		{"zero interval", "interval", 0, "unsafe polling interval"},
		{"long interval", "interval", 31, "unsafe polling interval"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				json.NewEncoder(w).Encode(startResponse(map[string]any{test.field: test.value}))
			}))
			defer srv.Close()
			_, err := New(srv.URL, "").StartDevice(context.Background(), "Termcade CLI")
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error=%v, want %q", err, test.want)
			}
		})
	}
}

func TestPollMalformedExpiredAndThirtyDayCredential(t *testing.T) {
	responses := []DevicePoll{
		{Status: "pending", Interval: 31},
		{Status: "expired", Token: testToken},
		{Status: "approved", Token: "tcc_bad", CredentialID: "id", ExpiresAt: time.Now().Add(30 * 24 * time.Hour).Format(time.RFC3339)},
		{Status: "approved", Token: testToken, CredentialID: "id\n", ExpiresAt: time.Now().Add(30 * 24 * time.Hour).Format(time.RFC3339)},
		{Status: "approved", Token: testToken, CredentialID: "id", ExpiresAt: time.Now().Add(32 * 24 * time.Hour).Format(time.RFC3339)},
		{Status: "approved", Token: testToken, CredentialID: "id", ExpiresAt: time.Now().Add(30 * 24 * time.Hour).Format(time.RFC3339)},
	}
	index := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		json.NewEncoder(w).Encode(responses[index])
		index++
	}))
	defer srv.Close()
	client := New(srv.URL, "")
	for i := range len(responses) - 1 {
		if _, err := client.PollDevice(context.Background(), testDevice); err == nil {
			t.Fatalf("hostile poll response %d accepted", i)
		}
	}
	if result, err := client.PollDevice(context.Background(), testDevice); err != nil || result.Token != testToken {
		t.Fatalf("30-day credential rejected: %#v %v", result, err)
	}
}

func TestPollingCancellationAndExpiryAreReachable(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	round := DeviceRound{DeviceCode: testDevice, ExpiresIn: 30 * time.Second, Interval: time.Second}
	if _, err := New("https://unused.example", "").pollRound(ctx, round, productionDevicePolicy); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel error = %v", err)
	}

	policy := fakePolicy()
	round.ExpiresIn = time.Second
	if _, err := New("https://unused.example", "").pollRound(context.Background(), round, policy); !errors.Is(err, ErrDeviceExpired) {
		t.Fatalf("expiry error = %v", err)
	}
}

func TestRevokedCredentialIsNotReturnedAsSession(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/device/start":
			json.NewEncoder(w).Encode(startResponse(nil))
		case "/v1/device/poll":
			json.NewEncoder(w).Encode(DevicePoll{Status: "approved", Token: testToken,
				CredentialID: "id", ExpiresAt: time.Now().Add(30 * 24 * time.Hour).Format(time.RFC3339)})
		case "/v1/me":
			w.WriteHeader(http.StatusUnauthorized)
		}
	}))
	defer srv.Close()
	_, err := New(srv.URL, "").deviceLogin(context.Background(), "Termcade CLI", func(string, string) {}, fakePolicy())
	if !errors.Is(err, ErrLoginRequired) {
		t.Fatalf("revocation error = %v, want ErrLoginRequired", err)
	}
}
