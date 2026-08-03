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

	// Signing in on a new machine is the moment your library should arrive.
	// Nobody should have to know a second command exists to get the games
	// they already own, so this runs here rather than waiting to be asked.
	//
	// Best-effort: the login itself succeeded, and reporting it as a failure
	// because a restore did not finish would be a lie about what happened.
	if err := cmdSync(); err != nil {
		fmt.Fprintf(os.Stderr, "note: could not restore your library: %v\n", err)
	}
	return nil
}

// promptUsername collects the handle a new account claims. It is not
// optional: a handle is the author segment of every game published from this
// account, and an account without one cannot publish at all.
func promptUsername(reader *bufio.Reader) (string, error) {
	fmt.Print("username (this is your publishing handle, e.g. nicodes): ")
	line, err := reader.ReadString('\n')
	if err != nil {
		return "", err
	}
	name := strings.TrimSpace(line)
	if name == "" {
		return "", fmt.Errorf("a username is required")
	}
	return name, nil
}

func cmdSignup(args []string) error {
	if len(args) > 1 {
		return fmt.Errorf("usage: termcade signup [email]")
	}
	username, err := promptUsername(bufio.NewReader(os.Stdin))
	if err != nil {
		return err
	}
	email, password, err := promptCredentials(args, true)
	if err != nil {
		return err
	}
	client := registry.New(registry.URL(nil), "")
	session, err := client.Signup(email, password, username)
	if err != nil {
		return err
	}
	if err := registry.SaveSession(session); err != nil {
		return err
	}
	// The handle is the useful half of the greeting: it is what a game id
	// starts with, so it is what an author needs to know they have.
	if session.Username != "" {
		fmt.Printf("welcome to termcade, %s — publish as %s/<game>\n", session.Email, session.Username)
	} else {
		fmt.Printf("welcome to termcade, %s\n", session.Email)
	}
	if session.Notice != "" {
		fmt.Fprintln(os.Stderr, "note:", session.Notice)
	}
	return nil
}

func cmdLogout() error {
	if err := registry.ClearSession(); err != nil {
		return err
	}
	fmt.Println("logged out")
	return nil
}
