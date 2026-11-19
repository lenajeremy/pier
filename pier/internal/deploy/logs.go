package deploy

import (
	"bufio"
	"bytes"
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
	mu    sync.Mutex
	w     io.Writer
	start time.Time
	buf   []byte
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
