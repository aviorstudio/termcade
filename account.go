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

// cmdKeys manages publish keys: the credential a release workflow holds so
// publishing does not need a password on a machine nobody is sitting at.
func cmdKeys(args []string) error {
	session, err := registry.LoadSession()
	if err != nil {
		return err
	}
	if session == nil {
		return fmt.Errorf("managing keys requires an account — run `termcade login`")
	}
	client := registry.New(registry.URL(session), session.Token)

	switch {
	case len(args) == 0 || args[0] == "list":
		keys, err := client.Keys()
		if err != nil {
			return err
		}
		if len(keys) == 0 {
			fmt.Println("no publish keys — create one with `termcade keys new <name> <username>`")
			return nil
		}
		for _, k := range keys {
			used := "never used"
			if k.LastUsed != "" {
				used = "last used " + k.LastUsed
			}
			fmt.Printf("%-24s %-20s %s\n  %s\n", k.Name, k.Username, used, k.ID)
		}
		return nil

	case args[0] == "new":
		if len(args) != 3 {
			return fmt.Errorf("usage: termcade keys new <name> <username>")
		}
		key, err := client.CreateKey(args[1], args[2])
		if err != nil {
			return err
		}
		// Printed once because it exists once. The registry stores a hash and
		// cannot produce this again, so anything that loses it needs a new key.
		fmt.Printf("created %q, publishing as %s\n\n  %s\n\n", key.Name, key.Username, key.Token)
		fmt.Fprintln(os.Stderr,
			"that token is shown once and cannot be recovered — put it somewhere safe now")
		return nil

	case args[0] == "revoke":
		if len(args) != 2 {
			return fmt.Errorf("usage: termcade keys revoke <id>")
		}
		if err := client.DeleteKey(args[1]); err != nil {
			return err
		}
		fmt.Println("revoked")
		return nil
	}
	return fmt.Errorf("unknown keys subcommand; try: list · new <name> <username> · revoke <id>")
}

// cmdOrg manages studios: a handle more than one person publishes under.
//
// An org exists so `aviorstudio/tetris` can outlive any one account, and so a
// second person can release it. Membership is enough to publish; admin governs
// the studio itself.
func cmdOrg(args []string) error {
	session, err := registry.LoadSession()
	if err != nil {
		return err
	}
	if session == nil {
		return fmt.Errorf("managing studios requires an account — run `termcade login`")
	}
	client := registry.New(registry.URL(session), session.Token)

	switch {
	case len(args) == 0 || args[0] == "list":
		me, err := client.Me()
		if err != nil {
			return err
		}
		if me.Username != "" {
			fmt.Printf("%-24s you\n", me.Username)
		}
		for _, org := range me.Orgs {
			role := "member"
			if org.Admin {
				role = "admin"
			}
			fmt.Printf("%-24s studio (%s)\n", org.Username, role)
		}
		if len(me.Orgs) == 0 {
			fmt.Println("\nno studios — create one with `termcade org new <username>`")
		}
		return nil

	case args[0] == "new":
		if len(args) < 2 || len(args) > 3 {
			return fmt.Errorf("usage: termcade org new <username> [bio]")
		}
		bio := ""
		if len(args) == 3 {
			bio = args[2]
		}
		org, err := client.CreateOrg(args[1], bio, "")
		if err != nil {
			return err
		}
		fmt.Printf("created %s — you are its admin\n", org.Username)
		fmt.Printf("publish as %s/<game>, and add people with `termcade org add %s <email>`\n",
			org.Username, org.Username)
		return nil

	case args[0] == "show":
		if len(args) != 2 {
			return fmt.Errorf("usage: termcade org show <username>")
		}
		org, err := client.Org(args[1])
		if err != nil {
			return err
		}
		fmt.Printf("%s\n", org.Username)
		if org.Bio != "" {
			fmt.Printf("  %s\n", org.Bio)
		}
		if len(org.Games) == 0 {
			fmt.Println("  no published games")
		}
		for _, game := range org.Games {
			fmt.Printf("  %s\n", game)
		}
		return nil

	case args[0] == "add":
		// One call for adding and for promoting: they are the same intent at
		// different times, and a separate `promote` would only work in one
		// order.
		if len(args) < 3 || len(args) > 4 {
			return fmt.Errorf("usage: termcade org add <org> <email> [admin]")
		}
		admin := len(args) == 4 && args[3] == "admin"
		if err := client.AddMember(args[1], args[2], admin); err != nil {
			return err
		}
		role := "a member"
		if admin {
			role = "an admin"
		}
		fmt.Printf("%s is now %s of %s\n", args[2], role, args[1])
		return nil

	case args[0] == "edit":
		if len(args) != 3 {
			return fmt.Errorf("usage: termcade org edit <org> <bio>")
		}
		bio := args[2]
		org, err := client.UpdateOrg(args[1], &bio, nil)
		if err != nil {
			return err
		}
		fmt.Printf("updated %s\n", org.Username)
		return nil

	case args[0] == "delete":
		// Confirmed by typing the name. Dissolving a studio releases its
		// handle for anyone to claim, and cannot be undone.
		if len(args) != 3 || args[2] != args[1] {
			return fmt.Errorf(
				"this dissolves the studio and releases its handle for anyone to claim.\n"+
					"it cannot be undone. to confirm:\n\n  termcade org delete %s %s",
				valueOr(args, 1), valueOr(args, 1))
		}
		if err := client.DeleteOrg(args[1]); err != nil {
			return err
		}
		fmt.Printf("dissolved %s\n", args[1])
		return nil

	case args[0] == "remove":
		if len(args) != 3 {
			return fmt.Errorf("usage: termcade org remove <org> <email>")
		}
		if err := client.RemoveMember(args[1], args[2]); err != nil {
			return err
		}
		fmt.Printf("removed %s from %s\n", args[2], args[1])
		return nil
	}
	return fmt.Errorf(
		"unknown org subcommand; try: list · new <username> [bio] · show <username> · " +
			"edit <org> <bio> · add <org> <email> [admin] · remove <org> <email> · " +
			"delete <org> <org>")
}

// valueOr keeps a usage message readable when the argument it wants to quote
// was the thing that was missing.
func valueOr(args []string, i int) string {
	if i < len(args) {
		return args[i]
	}
	return "<org>"
}

// cmdWhoami reports the account, its handle, and every studio it can publish
// under. The first thing to reach for when a publish is refused and the reason
// is "you are not a member of" something.
func cmdWhoami() error {
	session, err := registry.LoadSession()
	if err != nil {
		return err
	}
	if session == nil {
		return fmt.Errorf("not signed in — run `termcade login`")
	}
	me, err := registry.New(registry.URL(session), session.Token).Me()
	if err != nil {
		return err
	}

	fmt.Printf("%s (%s)\n", me.Email, session.Registry)
	if me.Username == "" {
		// A real state: an account whose signup lost a race for its handle.
		fmt.Println("\nno username yet — claim one with `termcade username <name>`")
	} else {
		fmt.Printf("\npublish as:\n  %-24s you\n", me.Username)
	}
	for _, org := range me.Orgs {
		role := "member"
		if org.Admin {
			role = "admin"
		}
		fmt.Printf("  %-24s studio (%s)\n", org.Username, role)
	}
	return nil
}

// cmdUsername claims or renames this account's handle, and with no argument
// reports whether one is free.
func cmdUsername(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: termcade username <name>")
	}
	session, err := registry.LoadSession()
	if err != nil {
		return err
	}

	// Checking availability needs no account, so it works before signing up —
	// which is when somebody is choosing a name.
	client := registry.New(registry.URL(session), "")
	owner, taken, err := client.HandleTaken(args[0])
	if err != nil {
		return err
	}
	if session == nil {
		if taken {
			kind := "an account"
			if owner.IsOrg {
				kind = "a studio"
			}
			return fmt.Errorf("%s is taken (by %s)", args[0], kind)
		}
		fmt.Printf("%s is available — claim it with `termcade signup`\n", args[0])
		return nil
	}

	claimed, err := registry.New(registry.URL(session), session.Token).SetUsername(args[0])
	if err != nil {
		return err
	}
	// The stored session carries the handle, so it has to follow the rename or
	// every later command reports the old one.
	session.Username = claimed.Name
	if err := registry.SaveSession(*session); err != nil {
		return err
	}
	fmt.Printf("you are %s — publish as %s/<game>\n", claimed.Name, claimed.Name)
	return nil
}

// cmdAccountDelete removes the account, after saying what that means and
// waiting for the word.
func cmdAccountDelete(args []string) error {
	session, err := registry.LoadSession()
	if err != nil {
		return err
	}
	if session == nil {
		return fmt.Errorf("not signed in — run `termcade login`")
	}

	// Confirmed by typing the handle, not by pressing y. This releases a name
	// somebody else can then claim, and it cannot be undone.
	if len(args) != 1 || args[0] != session.Username {
		return fmt.Errorf(
			"this deletes %s and releases the handle %q for anyone to claim.\n"+
				"it cannot be undone. to confirm:\n\n  termcade account delete %s",
			session.Email, session.Username, session.Username)
	}

	if err := registry.New(registry.URL(session), session.Token).DeleteAccount(); err != nil {
		return err
	}
	if err := registry.ClearSession(); err != nil {
		return err
	}
	fmt.Println("account deleted")
	return nil
}

func cmdAccount(args []string) error {
	if len(args) >= 1 && args[0] == "delete" {
		return cmdAccountDelete(args[1:])
	}
	return fmt.Errorf("unknown account subcommand; try: termcade account delete <username>")
}
