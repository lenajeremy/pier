package main

import (
	"fmt"
	"os"
	"sync"
	"time"

	terminal "golang.org/x/term"
)

// spinDelay is how long a wait goes before the spinner shows, so a quick
// answer doesn't flash one.
const spinDelay = 250 * time.Millisecond

// spin shows label beside a spinner on stderr while the caller waits on
// pierd, and returns the function that clears it. It shows only on a
// terminal: a pipe or a log gets nothing, and a full-screen view owns the
// screen, so it isn't used while one runs.
func spin(label string) func() {
	if !terminal.IsTerminal(int(os.Stderr.Fd())) || os.Getenv("TERM") == "dumb" {
		return func() {}
	}
	c := os.Getenv("NO_COLOR") == ""
	done := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		select {
		case <-done:
			return
		case <-time.After(spinDelay):
		}
		t := time.NewTicker(80 * time.Millisecond)
		defer t.Stop()
		for i := 0; ; i++ {
			fmt.Fprint(os.Stderr, "\r\x1b[2K  "+ink(spinnerFrames[i%len(spinnerFrames)], lavender, c)+" "+ink(label, fgMuted, c))
			select {
			case <-done:
				fmt.Fprint(os.Stderr, "\r\x1b[2K")
				return
			case <-t.C:
			}
		}
	}()
	var once sync.Once
	return func() {
		once.Do(func() {
			close(done)
			wg.Wait()
		})
	}
}

// callWith is call with a spinner that says what it's waiting for.
func callWith(label string, c config, method, path string, body, into any) error {
	stop := spin(label)
	defer stop()
	return call(c, method, path, body, into)
}
