package main

import (
	"os"
	"strings"
	"testing"
)

func TestTerminalAuthContainsNoLegacySecretHandling(t *testing.T) {
	for _, path := range []string{"account.go", "cli.go", "internal/registry/client.go", "internal/shell/market.go"} {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, forbidden := range []string{"auth/login", "auth/signup", "ReadPassword", "promptCredentials"} {
			if strings.Contains(string(raw), forbidden) {
				t.Errorf("%s still contains legacy terminal secret handling %q", path, forbidden)
			}
		}
	}
}
