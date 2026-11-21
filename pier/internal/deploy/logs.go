package deploy

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// A build log is kept per deployment as lines of "<ms since start>\t<line>",
// so the dashboard can show when each line happened.

func (d *Deployer) logPath(id string) string { return filepath.Join(d.Logs, id+".log") }

func (d *Deployer) createLog(id string) (*os.File, error) {
	if d.Logs == "" {
		return nil, errors.New("no log folder set")
	}
	if err := os.MkdirAll(d.Logs, 0o755); err != nil {
		return nil, err
	}
	return os.OpenFile(d.logPath(id), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
}

// timedLog writes each complete line with its time since start. Write errors
// are swallowed: a full disk mustn't stop the narration to the pusher.
type timedLog struct {
	mu      sync.Mutex
	w       io.Writer
	start   time.Time
	buf     []byte
	changed func()
}

func (t *timedLog) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, p...)
	for {
		i := bytes.IndexByte(t.buf, '\n')
		if i < 0 {
			break
		}
		t.line(t.buf[:i])
		t.buf = t.buf[i+1:]
	}
	return len(p), nil
}

func (t *timedLog) line(l []byte) {
	l = bytes.TrimRight(l, "\r")
	fmt.Fprintf(t.w, "%d\t%s\n", time.Since(t.start).Milliseconds(), l)
	if t.changed != nil {
		t.changed()
	}
}

func (t *timedLog) Flush() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(bytes.TrimSpace(t.buf)) > 0 {
		t.line(t.buf)
	}
	t.buf = nil
}

// LogLine is one line of a build log.
type LogLine struct {
	MS   int64  `json:"ms"`   // since the build started
	Tag  string `json:"tag"`  // pier, build, deploy, alias
	Text string `json:"text"` // the rest of the line
}

// ErrNoLog means the deployment has no log on disk.
var ErrNoLog = errors.New("no build log")

// ReadLog returns a deployment's log lines from line number from onwards.
func (d *Deployer) ReadLog(id string, from int) ([]LogLine, error) {
	f, err := os.Open(d.logPath(id))
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNoLog
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []LogLine
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	n := 0
	for sc.Scan() {
		raw := sc.Text()
		ms, rest, ok := strings.Cut(raw, "\t")
		if !ok {
			continue // a line still being written
		}
		if n++; n <= from {
			continue
		}
		t, _ := strconv.ParseInt(ms, 10, 64)
		out = append(out, parseLine(t, rest))
	}
	return out, sc.Err()
}

// parseLine splits "build   $ npm ci" into its tag and text.
func parseLine(ms int64, s string) LogLine {
	if len(s) > 8 && s[7] == ' ' {
		tag := strings.TrimSpace(s[:8])
		if tag != "" && !strings.Contains(tag, " ") {
			return LogLine{MS: ms, Tag: tag, Text: s[8:]}
		}
	}
	return LogLine{MS: ms, Text: s}
}

// Register before publishing the deployment, and retire after the final log
// flush. A ready deployment can still be writing its routing and timing lines.
func (d *Deployer) beginLog(id string) (func(), error) {
	d.mu.Lock()
	if d.logChanges == nil {
		d.logChanges = make(map[string]chan struct{})
	}
	if _, exists := d.logChanges[id]; exists {
		d.mu.Unlock()
		return nil, errors.New("deployment ID already in use")
	}
	d.logChanges[id] = nil
	d.mu.Unlock()
	return func() { d.signalLog(id, true) }, nil
}
func (d *Deployer) signalLog(id string, finished bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if ch := d.logChanges[id]; ch != nil {
		close(ch)
	}
	if finished {
		delete(d.logChanges, id)
	} else {
		d.logChanges[id] = nil
	}
}

// FollowLog sends the initial tail and advances by byte offset on subsequent
// writes. A slow network reader never holds up a deployment.
func (d *Deployer) FollowLog(ctx context.Context, id string, tail int, send func([]LogLine) error) error {
	tail = max(1, min(500, tail))
	var offset int64
	first := true
	for {
		d.mu.Lock()
		changed, active := d.logChanges[id]
		if active && changed == nil {
			changed = make(chan struct{})
			d.logChanges[id] = changed
		}
		d.mu.Unlock()
		f, err := os.Open(d.logPath(id))
		var lines []LogLine
		if err == nil {
			lines, offset, err = readLogBatch(ctx, f, offset, tail, first, send)
			f.Close()
		}
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if first || len(lines) > 0 {
			if err := send(lines); err != nil {
				return err
			}
		}
		first = false
		if changed == nil {
			return nil
		}
		select {
		case <-changed:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func readLogBatch(ctx context.Context, f *os.File, offset int64, tail int, first bool, send func([]LogLine) error) ([]LogLine, int64, error) {
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return nil, offset, err
	}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	sc.Split(func(b []byte, atEOF bool) (int, []byte, error) {
		if i := bytes.IndexByte(b, '\n'); i >= 0 {
			return i + 1, b[:i], nil
		}
		return 0, nil, nil // Leave a partial record at the current byte offset.
	})
	var lines []LogLine
	for sc.Scan() {
		if err := ctx.Err(); err != nil {
			return nil, offset, err
		}
		offset += int64(len(sc.Bytes()) + 1)
		ms, body, ok := strings.Cut(sc.Text(), "\t")
		if !ok {
			continue
		}
		n, _ := strconv.ParseInt(ms, 10, 64)
		lines = append(lines, parseLine(n, body))
		if first && len(lines) >= 2*tail {
			copy(lines, lines[len(lines)-tail:])
			lines = lines[:tail]
		}
		if !first && len(lines) == 100 {
			if err := send(lines); err != nil {
				return nil, offset, err
			}
			lines = nil
		}
	}
	if first && len(lines) > tail {
		lines = lines[len(lines)-tail:]
	}
	return lines, offset, sc.Err()
}
