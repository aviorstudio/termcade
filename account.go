package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"golang.org/x/term"

	"github.com/aviorstudio/termcade/internal/registry"
)

// promptCredentials collects email (arg or prompt) and a hidden password.
func promptCredentials(args []string, confirm bool) (email, password string, err error) {
	reader := bufio.NewReader(os.Stdin)
	if len(args) >= 1 {
		email = args[0]
	} else {
		fmt.Print("email: ")
		line, err := reader.ReadString('\n')
		if err != nil {
			return "", "", err
		}
		email = strings.TrimSpace(line)
	}
	if email == "" {
		return "", "", fmt.Errorf("an email is required")
	}

	fmt.Print("password: ")
	raw, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Println()
	if err != nil {
		return "", "", fmt.Errorf("reading password: %w", err)
	}
	password = string(raw)
	if confirm {
		fmt.Print("confirm password: ")
		again, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Println()
		if err != nil {
			return "", "", fmt.Errorf("reading password: %w", err)
		}
		if string(again) != password {
			return "", "", fmt.Errorf("passwords do not match")
		}
	}
	return email, password, nil
}

func cmdLogin(args []string) error {
	if len(args) > 1 {
		return fmt.Errorf("usage: termcade login [email]")
	}
	email, password, err := promptCredentials(args, false)
	if err != nil {
		return err
	}
	client := registry.New(registry.URL(nil), "")
	session, err := client.Login(email, password)
	if err != nil {
		return err
	}
	if err := registry.SaveSession(session); err != nil {
		return err
	}
	fmt.Printf("logged in as %s (%s)\n", session.Email, session.Registry)
	return nil
}

func cmdSignup(args []string) error {
	if len(args) > 1 {
		return fmt.Errorf("usage: termcade signup [email]")
	}
	email, password, err := promptCredentials(args, true)
	if err != nil {
		return err
	}
	client := registry.New(registry.URL(nil), "")
	session, err := client.Signup(email, password)
	if err != nil {
		return err
	}
	if err := registry.SaveSession(session); err != nil {
		return err
	}
	fmt.Printf("welcome to termcade, %s\n", session.Email)
	return nil
}

func cmdLogout() error {
	if err := registry.ClearSession(); err != nil {
		return err
	}
	fmt.Println("logged out")
	return nil
}
