package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Liwyd/bull-Tunnel/internal/panel/auth"
	"golang.org/x/term"
)

// writePassword changes the admin password of the panel.
//
// The file of the login is written directly, the way the panel's own command
// line does it: the secret the sessions are signed with is rotated, so
// every session is invalid afterwards.
func writePassword(path string, username string, password string) error {
	if len(password) < 6 {
		return panelError("the password must be at least 6 characters")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return panelError("the data directory cannot be made: %v", err)
	}
	login := auth.New(path)
	if err := login.SetPassword(username, password); err != nil {
		return panelError("the password was not changed: %v", err)
	}
	return nil
}

// askSecret asks for something that must not be seen: the terminal is not
// echoed while it is typed.
func askSecret(prompt string) (string, error) {
	fmt.Print(prompt + ": ")
	secret, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Println()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(secret)), nil
}
