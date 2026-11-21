package deploy

import (
	"context"
	"testing"
	"time"
)

func TestFollowBuildThroughFinalFlush(t *testing.T) {
	d := Deployer{Logs: t.TempDir()}
	finish, err := d.beginLog("abc")
	if err != nil {
		t.Fatal(err)
	}
	f, err := d.createLog("abc")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	log := timedLog{w: f, start: time.Now(), changed: func() { d.signalLog("abc", false) }}
	log.Write([]byte("pier    old\nbuild   initial\n"))
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	out := make(chan string, 3)
	done := make(chan error, 1)
	go func() {
		done <- d.FollowLog(ctx, "abc", 1, func(lines []LogLine) error {
			for _, l := range lines {
				out <- l.Tag + " " + l.Text
			}
			return nil
		})
	}()
	read := func(want string) {
		t.Helper()
		select {
		case got := <-out:
			if got != want {
				t.Fatalf("got %q, want %q", got, want)
			}
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	read("build initial")
	log.Write([]byte("alias   served\npier    finished"))
	read("alias served")
	log.Flush()
	finish()
	read("pier finished")
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
