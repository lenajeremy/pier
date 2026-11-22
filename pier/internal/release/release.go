// Package release finds Pier's releases on GitHub, downloads a release's
// bundle, checks it against the SHA256SUMS inside it, and puts a binary in
// place with no moment where it's missing or half written. pier upgrade and
// pierd upgrade-apply both use it.
package release

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Repo is where Pier's releases are published.
const Repo = "lenajeremy/pier"

// These are fixed: nothing a request, a file or an API answer says changes
// where a release is downloaded from.
const (
	// DownloadBase holds every release's bundles, under its tag.
	DownloadBase = "https://github.com/" + Repo + "/releases/download"
	// LatestAPI says which release is the newest.
	LatestAPI = "https://api.github.com/repos/" + Repo + "/releases/latest"
	// NotesBase is a release's page, with its notes, under its tag.
	NotesBase = "https://github.com/" + Repo + "/releases/tag/"
)

// Bundles can't be larger than this, packed or unpacked.
const maxBundle = 512 << 20

var tag = regexp.MustCompile(`^v(0|[1-9][0-9]{0,5})\.(0|[1-9][0-9]{0,5})\.(0|[1-9][0-9]{0,5})$`)

// Valid reports whether v names a release exactly, like v1.2.3: no
// suffixes, no leading zeros, nothing else.
func Valid(v string) bool { return tag.MatchString(v) }

// Newer reports whether release a is later than release b. Both must be
// valid; if either isn't, it says no.
func Newer(a, b string) bool {
	if !Valid(a) || !Valid(b) {
		return false
	}
	pa, pb := numbers(a), numbers(b)
	for i := range 3 {
		if pa[i] != pb[i] {
			return pa[i] > pb[i]
		}
	}
	return false
}

func numbers(v string) [3]int {
	var out [3]int
	for i, p := range strings.SplitN(strings.TrimPrefix(v, "v"), ".", 3) {
		out[i], _ = strconv.Atoi(p)
	}
	return out
}

// Notes is a release's page on GitHub.
func Notes(v string) string { return NotesBase + v }

// Client is an HTTP client for releases: it gives up after a while, and
// won't follow a redirect from HTTPS to plain HTTP, as GitHub's downloads
// redirect to its file storage.
func Client(timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout: timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return errors.New("too many redirects")
			}
			if via[0].URL.Scheme == "https" && req.URL.Scheme != "https" {
				return errors.New("a release download can't move from HTTPS to plain HTTP")
			}
			return nil
		},
	}
}

// Latest asks GitHub which release is the newest. api is LatestAPI except
// in tests.
func Latest(ctx context.Context, c *http.Client, api string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, api, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "pier")
	resp, err := c.Do(req)
	if err != nil {
		return "", fmt.Errorf("Couldn't ask GitHub for the latest release: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GitHub answered %s when asked for the latest release", resp.Status)
	}
	var r struct {
		Tag string `json:"tag_name"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&r); err != nil {
		return "", fmt.Errorf("GitHub's answer about the latest release couldn't be read: %w", err)
	}
	if !Valid(r.Tag) {
		return "", fmt.Errorf("The latest release on GitHub isn't named like v1.2.3")
	}
	return r.Tag, nil
}

// Fetch downloads a bundle and unpacks it into a new folder under work. A
// bundle is a tar.gz holding one folder, top, of plain files; it returns
// that folder's path. Anything else in the archive, like a link, a deeper
// path or a name that climbs out, is refused.
func Fetch(ctx context.Context, c *http.Client, url, top, work string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "pier")
	resp, err := c.Do(req)
	if err != nil {
		return "", fmt.Errorf("Couldn't download the release: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("Downloading the release answered %s", resp.Status)
	}
	dir, err := os.MkdirTemp(work, "bundle-")
	if err != nil {
		return "", err
	}
	zr, err := gzip.NewReader(io.LimitReader(resp.Body, maxBundle))
	if err != nil {
		return "", fmt.Errorf("The download isn't a gzipped bundle: %w", err)
	}
	tr := tar.NewReader(zr)
	var total int64
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", fmt.Errorf("The bundle couldn't be unpacked: %w", err)
		}
		name := strings.TrimSuffix(strings.TrimPrefix(h.Name, "./"), "/")
		if h.Typeflag == tar.TypeDir && name == top {
			continue
		}
		file, ok := strings.CutPrefix(name, top+"/")
		if h.Typeflag != tar.TypeReg || !ok || file == "" || file == "." || file == ".." || strings.ContainsAny(file, `/\`) {
			return "", fmt.Errorf("The bundle holds %q, which isn't a plain file in %s", h.Name, top)
		}
		if total += h.Size; total > maxBundle {
			return "", errors.New("The bundle is larger than any Pier release")
		}
		// Owner-only, in a folder of its own, and runnable, so the new binary
		// can be tried before it replaces the old one.
		f, err := os.OpenFile(filepath.Join(dir, file), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o700)
		if err != nil {
			return "", err
		}
		_, err = io.Copy(f, io.LimitReader(tr, h.Size))
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return "", fmt.Errorf("The bundle couldn't be unpacked: %w", err)
		}
	}
	return dir, nil
}

// Verify checks the files a bundle's SHA256SUMS lists against it, and that
// it lists each of the required ones. It returns the names it lists, which
// are the only files of the bundle that may be used.
//
// The sums come in the bundle they check, so they catch a download or an
// unpacking that went wrong, and nothing else: whoever could change the
// bundle could change them too. What says the bundle is Pier's is HTTPS to
// GitHub, at an address that's fixed.
func Verify(dir string, required ...string) (map[string]bool, error) {
	f, err := os.Open(filepath.Join(dir, "SHA256SUMS"))
	if err != nil {
		return nil, errors.New("The bundle has no SHA256SUMS to check it with")
	}
	defer f.Close()
	listed := map[string]bool{}
	sc := bufio.NewScanner(io.LimitReader(f, 64<<10))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		sum, name, ok := strings.Cut(line, " ")
		name = strings.TrimPrefix(strings.TrimSpace(name), "*")
		if !ok || len(sum) != 64 || name == "" || strings.ContainsAny(name, `/\`) || name == "." || name == ".." {
			return nil, fmt.Errorf("SHA256SUMS has a line that isn't a sum and a file name: %q", line)
		}
		got, err := fileSum(filepath.Join(dir, name))
		if err != nil {
			return nil, fmt.Errorf("SHA256SUMS lists %s, which isn't in the bundle", name)
		}
		if !strings.EqualFold(got, sum) {
			return nil, fmt.Errorf("The bundle's %s doesn't match its checksum, so the download is damaged", name)
		}
		listed[name] = true
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	for _, name := range required {
		if !listed[name] {
			return nil, fmt.Errorf("SHA256SUMS doesn't list %s", name)
		}
	}
	return listed, nil
}

func fileSum(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// ReadValue reads a one-line file of a bundle, like VERSION or ARCH.
func ReadValue(dir, name string) string {
	b, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// Stage copies src into a new temp file beside dst, with mode, flushed to
// disk, ready to be renamed over dst. The rename is the only step that
// changes dst, and it changes it whole.
func Stage(src, dst string, mode os.FileMode) (string, error) {
	in, err := os.Open(src)
	if err != nil {
		return "", err
	}
	defer in.Close()
	tmp, err := os.CreateTemp(filepath.Dir(dst), "."+filepath.Base(dst)+".")
	if err != nil {
		return "", err
	}
	_, err = io.Copy(tmp, in)
	if err == nil {
		err = tmp.Chmod(mode)
	}
	if err == nil {
		err = tmp.Sync()
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(tmp.Name())
		return "", err
	}
	return tmp.Name(), nil
}

// Replace puts src at dst whole: copied beside it, then renamed over it.
func Replace(src, dst string, mode os.FileMode) error {
	tmp, err := Stage(src, dst, mode)
	if err != nil {
		return err
	}
	if err := os.Rename(tmp, dst); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// Same reports whether two files hold the same bytes.
func Same(a, b string) bool {
	x, err1 := os.ReadFile(a)
	y, err2 := os.ReadFile(b)
	return err1 == nil && err2 == nil && string(x) == string(y)
}

// Checker asks GitHub for the latest release now and then, and keeps the
// answer. A failed check keeps the last answer and is only logged.
type Checker struct {
	API    string        // LatestAPI unless testing
	Every  time.Duration // how often to ask; 6 hours if zero
	Client *http.Client
	Log    *slog.Logger

	mu     sync.Mutex
	latest string
}

// Run checks soon after it starts, then every Every, until ctx ends.
func (c *Checker) Run(ctx context.Context) {
	every := c.Every
	if every == 0 {
		every = 6 * time.Hour
	}
	wait := 10 * time.Second
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		c.Check(ctx)
		wait = every
	}
}

// Check asks once.
func (c *Checker) Check(ctx context.Context) {
	api, client := c.API, c.Client
	if api == "" {
		api = LatestAPI
	}
	if client == nil {
		client = Client(30 * time.Second)
	}
	v, err := Latest(ctx, client, api)
	if err != nil {
		if c.Log != nil {
			c.Log.Debug("check for a new release", "err", err)
		}
		return
	}
	c.mu.Lock()
	c.latest = v
	c.mu.Unlock()
}

// Latest is the newest release the last good check found, or "".
func (c *Checker) Latest() string {
	if c == nil {
		return ""
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.latest
}
