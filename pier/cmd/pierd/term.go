package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"unicode/utf8"

	"golang.org/x/term"
)

// tty is pierd's conversation with the owner in the terminal: marks in the
// design system's colours, questions, and hidden passwords.
type tty struct {
	in          *bufio.Reader
	out         io.Writer
	interactive bool // stdin is a terminal: ask, and hide passwords
	color       bool
}

func newTTY() *tty {
	fd := int(os.Stdin.Fd())
	return &tty{
		in:          bufio.NewReader(os.Stdin),
		out:         os.Stdout,
		interactive: term.IsTerminal(fd),
		color:       os.Getenv("NO_COLOR") == "" && term.IsTerminal(int(os.Stdout.Fd())),
	}
}

func (t *tty) paint(rgb, s string) string {
	if !t.color {
		return s
	}
	return "\x1b[38;2;" + rgb + "m" + s + "\x1b[0m"
}

func (t *tty) ok() string      { return t.paint("98;216;198", "✓") }
func (t *tty) warn() string    { return t.paint("240;184;92", "!") }
func (t *tty) waiting() string { return t.paint("188;174;248", "●") }
func (t *tty) bad() string     { return t.paint("255;180;180", "✗") }
func (t *tty) dim(s string) string {
	return t.paint("138;146;168", s)
}

func (t *tty) println(a ...any)                 { fmt.Fprintln(t.out, a...) }
func (t *tty) printf(f string, a ...any)        { fmt.Fprintf(t.out, f, a...) }
func (t *tty) heading(s string)                 { t.printf("\n%s\n", s) }
func (t *tty) line(mark, s string)              { t.printf("  %s %s\n", mark, s) }
func (t *tty) note(s string)                    { t.printf("    %s\n", t.dim(s)) }
func (t *tty) field(label string, w int) string { return "    " + pad(label, w) }

// ask prints a label and reads one line.
func (t *tty) ask(label string) (string, error) {
	t.printf("%s", label)
	s, err := t.in.ReadString('\n')
	if err != nil && (s == "" || !errors.Is(err, io.EOF)) {
		return "", err
	}
	return strings.TrimSpace(s), nil
}

// yes asks a [Y/n] question; Enter means yes.
func (t *tty) yes(question string) bool {
	if !t.interactive {
		return false
	}
	ans, err := t.ask("    " + question + " [Y/n] ")
	if err != nil {
		return false
	}
	ans = strings.ToLower(ans)
	return ans == "" || ans == "y" || ans == "yes"
}

// secret reads a password without showing it, then shows a dot per character
// and an optional hint after it, like the rest of the form.
func (t *tty) secret(label, hint string) (string, error) {
	t.printf("%s", label)
	if !t.interactive {
		s, err := t.in.ReadString('\n')
		if err != nil && s == "" {
			return "", err
		}
		s = strings.TrimRight(s, "\r\n")
		t.printf("%s\n", strings.Repeat("•", utf8.RuneCountInString(s)))
		return s, nil
	}
	b, err := term.ReadPassword(int(os.Stdin.Fd()))
	if err != nil {
		t.println()
		return "", err
	}
	dots := strings.Repeat("•", utf8.RuneCountInString(string(b)))
	if hint != "" {
		dots += "  " + t.dim(hint)
	}
	t.printf("%s\n", dots)
	return string(b), nil
}

func pad(s string, w int) string {
	if n := utf8.RuneCountInString(s); n < w {
		return s + strings.Repeat(" ", w-n)
	}
	return s + " "
}
