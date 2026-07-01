package cmd

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"golang.org/x/term"
)

// promptLine prints a label and reads a single trimmed line from stdin.
func promptLine(label string) (string, error) {
	fmt.Print(label)
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(line), nil
}

// promptSecret reads a secret without echoing it when stdin is a terminal, and
// falls back to a plain read for pipes (e.g. `echo $TOKEN | glute auth`).
func promptSecret(label string) (string, error) {
	fmt.Print(label)
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return promptLine("")
	}
	b, err := term.ReadPassword(fd)
	fmt.Println()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}
