package jobs

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"pier/internal/alerts"
)

// MaxLog is how much of a run's output is kept. A run that prints more has
// the rest cut, and its log says so.
const MaxLog = 1 << 20

// Line is one line of a run's log: when, since the run started, which
// stream (stdout, stderr, or pier for Pier's own words), and the text.
type Line struct {
	MS     int64  `json:"ms"`
	Stream string `json:"stream"`
	Text   string `json:"text"`
}

// runLog keeps a run's output in its file as "ms\tstream\ttext" lines, up
// to MaxLog, and its last lines for an alert.
type runLog struct {
	mu      sync.Mutex
	f       *os.File
	start   time.Time
	written int
	cut     bool
	tail    []string
}

func logPath(dir, project string, run int64) string {
	return filepath.Join(dir, project, strconv.FormatInt(run, 10)+".log")
}

func openLog(dir, project string, run int64, start time.Time) (*runLog, error) {
	l := &runLog{start: start}
	if dir == "" {
		return l, nil
	}
	if err := os.MkdirAll(filepath.Join(dir, project), 0o755); err != nil {
		return l, err
	}
	f, err := os.OpenFile(logPath(dir, project, run), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	l.f = f
	return l, err
}

func (l *runLog) line(stream, text string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	text = strings.TrimRight(text, "\r\n")
	l.tail = append(l.tail, fmt.Sprintf("%-7s %s", stream, text))
	if len(l.tail) > alerts.MaxLines {
		l.tail = l.tail[1:]
	}
	if l.f == nil || l.cut {
		return
	}
	rec := fmt.Sprintf("%d\t%s\t%s\n", time.Since(l.start).Milliseconds(), stream, strings.ReplaceAll(text, "\n", " "))
	if l.written+len(rec) > MaxLog {
		l.cut = true
		rec = fmt.Sprintf("%d\tpier\tThe output went past 1 MB, so the rest isn't kept.\n", time.Since(l.start).Milliseconds())
	}
	n, _ := l.f.WriteString(rec)
	l.written += n
}

func (l *runLog) say(format string, args ...any) { l.line("pier", fmt.Sprintf(format, args...)) }

func (l *runLog) last() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.tail...)
}

func (l *runLog) close() {
	if l.f != nil {
		l.f.Close()
	}
}

// ReadLog returns a run's log from line from onwards.
func ReadLog(dir, project string, run int64, from int) ([]Line, error) {
	f, err := os.Open(logPath(dir, project, run))
	if errors.Is(err, os.ErrNotExist) {
		return []Line{}, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	out := []Line{}
	r := bufio.NewReader(f)
	n := 0
	for {
		raw, err := r.ReadString('\n')
		if err == io.EOF {
			break // a line still being written waits for the next read
		}
		if err != nil {
			return nil, err
		}
		if n++; n <= from {
			continue
		}
		parts := strings.SplitN(strings.TrimSuffix(raw, "\n"), "\t", 3)
		if len(parts) != 3 {
			continue
		}
		ms, _ := strconv.ParseInt(parts[0], 10, 64)
		out = append(out, Line{MS: ms, Stream: parts[1], Text: parts[2]})
	}
	return out, nil
}
