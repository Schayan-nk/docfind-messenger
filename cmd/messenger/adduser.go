package main

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/Schayan-nk/docfind-messenger/internal/auth"
	"golang.org/x/term"
)

// manageUser creates an account, or resets an existing one's password, from
// the command line.
//
// This exists so the very first administrator can be made without a web form:
// on a fresh install there is nobody to sign in as, and the alternative is
// typing a new password into a page served over plain HTTP before the reverse
// proxy is even in front of it. Reading it from the terminal keeps the first
// credential off the network entirely.
func manageUser(usersFile, username, role string) error {
	if strings.TrimSpace(usersFile) == "" {
		return errors.New("-add-user needs -users to say which accounts file to write")
	}
	r := auth.Role(strings.ToLower(strings.TrimSpace(role)))
	if !auth.ValidRole(r) {
		return fmt.Errorf("unknown role %q: use admin, manager or viewer", role)
	}
	if !auth.ValidUsername(username) {
		return errors.New("user name must be 3-64 characters of letters, digits, dot, dash or underscore")
	}

	store, err := auth.OpenStore(usersFile)
	if err != nil {
		return err
	}

	pw, err := readPasswordTwice()
	if err != nil {
		return err
	}
	if len(pw) < 10 {
		return errors.New("password must be at least 10 characters")
	}

	if _, err := store.Get(username); err == nil {
		// Already there: treat this as a password reset rather than failing,
		// which is what someone locked out of their own install actually
		// needs from this command.
		if err := store.SetPassword(username, pw); err != nil {
			return err
		}
		fmt.Printf("password reset for %s in %s\n", username, usersFile)
		return nil
	}

	u, err := store.Create(username, username, pw, r)
	if err != nil {
		return err
	}
	fmt.Printf("created %s (%s) in %s\n", u.Username, u.Role, usersFile)
	return nil
}

// readPasswordTwice prompts without echoing, and asks again to catch typos --
// there is no "forgot password" link behind this.
//
// One reader is shared across both prompts on purpose: a bufio.Reader reads
// ahead, so making a second one would find the buffer already drained and
// report EOF on a perfectly good second line.
func readPasswordTwice() (string, error) {
	in := bufio.NewReader(os.Stdin)
	first, err := readHidden(in, "password: ")
	if err != nil {
		return "", err
	}
	second, err := readHidden(in, "repeat:   ")
	if err != nil {
		return "", err
	}
	if first != second {
		return "", errors.New("the two passwords did not match")
	}
	return first, nil
}

// readHidden reads one line from the terminal with echo turned off, falling
// back to a visible read when there is no console (a pipe, or a service).
func readHidden(in *bufio.Reader, prompt string) (string, error) {
	fmt.Print(prompt)
	defer fmt.Println()

	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		// Not a terminal: read plainly rather than refusing, so scripted
		// setup still works.
		line, err := in.ReadString('\n')
		return strings.TrimRight(line, "\r\n"), err
	}
	b, err := term.ReadPassword(fd)
	return string(b), err
}
