package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"pier/internal/bundle"
)

// link is .pier/project.json: which project a folder deploys to.
type link struct {
	Project string `json:"project"`
	Server  string `json:"server"`
}

func readLink(dir string) (link, error) {
	var l link
	b, err := os.ReadFile(filepath.Join(dir, ".pier", "project.json"))
	if err != nil {
		return l, err
	}
	err = json.Unmarshal(b, &l)
	if err == nil && l.Project == "" {
		err = errors.New("no project in .pier/project.json")
	}
	return l, err
}

func writeLink(dir string, l link) error {
	if err := os.MkdirAll(filepath.Join(dir, ".pier"), 0o755); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(l, "", "  ")
	return os.WriteFile(filepath.Join(dir, ".pier", "project.json"), append(b, '\n'), 0o644)
}

var projectName = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,37}[a-z0-9])?$`)

// deploy uploads a folder and narrates its build: a preview, or production
// with --prod. The first time, it asks which project the folder is.
func deploy(args []string) error {
	fs := flag.NewFlagSet("deploy", flag.ExitOnError)
	prod := fs.Bool("prod", false, "deploy to production instead of a preview")
	projectFlag := fs.String("project", "", "the project to deploy to (remembered in .pier/project.json)")
	fs.Parse(reorder(args, map[string]bool{"project": true}))
	c, err := signedIn()
	if err != nil {
		return err
	}
	dir := "."
	if fs.NArg() > 0 {
		dir = fs.Arg(0)
	}
	dir, err = filepath.Abs(dir)
	if err != nil {
		return err
	}
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		return fmt.Errorf("%s isn't a folder", dir)
	}

	// Read git before chooseProject, which may add .pier to .gitignore and
	// so make a clean checkout look changed.
	g := gitInfo(dir)
	project, err := chooseProject(c, dir, *projectFlag)
	if err != nil {
		return err
	}

	files, err := bundle.Files(dir)
	if err != nil {
		return err
	}
	if len(files) == 0 {
		return fmt.Errorf("%s has nothing to upload", dir)
	}
	tmp, err := os.CreateTemp("", "pier-upload-*.tar.gz")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	size, err := bundle.Pack(tmp, dir, files)
	if err != nil {
		tmp.Close()
		return fmt.Errorf("packing %s: %w", dir, err)
	}
	packed, _ := tmp.Seek(0, 1)
	if _, err := tmp.Seek(0, 0); err != nil {
		return err
	}
	defer tmp.Close()

	target := "preview"
	if *prod {
		target = "production"
	}
	q := url.Values{
		"target": {target}, "files": {strconv.Itoa(len(files))},
		"branch": {g.branch}, "commit": {g.commit}, "message": {g.message},
	}
	fmt.Printf("Uploading %s (%s, %s packed) to %s, for %s\n",
		plural(len(files), "file"), human(size), human(packed), project, target)

	req, err := http.NewRequest(http.MethodPost, c.Server+"/api/projects/"+url.PathEscape(project)+"/deployments?"+q.Encode(), tmp)
	if err != nil {
		return err
	}
	req.ContentLength = packed
	req.Header.Set("Content-Type", "application/gzip")
	req.Header.Set("Authorization", "Bearer "+c.Token)
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("can't reach Pier at %s: %w", c.Server, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		var e struct{ Error string }
		json.NewDecoder(resp.Body).Decode(&e)
		if e.Error == "" {
			e.Error = "Pier answered " + resp.Status
		}
		return errors.New(e.Error)
	}
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		fmt.Println(colourLine(sc.Text()))
	}
	if err := sc.Err(); err != nil {
		return fmt.Errorf("lost the connection while it built; see the dashboard for how it ended: %w", err)
	}
	if resp.Trailer.Get("Pier-Result") != "ok" {
		return errors.New("the build failed; nothing changed")
	}
	return nil
}

// colourLine paints the narration's tag the way the dashboard's log does.
func colourLine(s string) string {
	if len(s) < 8 {
		return s
	}
	rgb := map[string]string{"pier": "188;174;248", "build": "98;216;198", "deploy": "244;156;203", "alias": "244;156;203"}[strings.TrimSpace(s[:8])]
	if rgb == "" {
		return s
	}
	return paint(rgb, s[:8]) + s[8:]
}

// chooseProject finds the project this folder deploys to: the flag, then
// .pier/project.json, then it asks and remembers the answer.
func chooseProject(c config, dir, flagged string) (string, error) {
	if flagged != "" {
		if err := ensureProject(c, flagged, false); err != nil {
			return "", err
		}
		return flagged, remember(c, dir, flagged)
	}
	if l, err := readLink(dir); err == nil {
		return l.Project, nil
	}
	if !term() {
		return "", errors.New("say which project this folder is: pier deploy --project NAME")
	}
	var list struct {
		Projects []struct{ Name string }
	}
	if err := call(c, http.MethodGet, "/api/projects", nil, &list); err != nil {
		return "", err
	}
	suggest := strings.Trim(regexp.MustCompile(`[^a-z0-9-]+`).ReplaceAllString(strings.ToLower(filepath.Base(dir)), "-"), "-")
	fmt.Printf("Which project is %s?\n", filepath.Base(dir))
	for i, p := range list.Projects {
		fmt.Printf("  %d  %s\n", i+1, p.Name)
	}
	answer := ask(fmt.Sprintf("Type a number, or a new name to create one [%s]: ", suggest))
	name := answer
	if answer == "" {
		name = suggest
	} else if n, err := strconv.Atoi(answer); err == nil && n >= 1 && n <= len(list.Projects) {
		name = list.Projects[n-1].Name
	}
	exists := false
	for _, p := range list.Projects {
		exists = exists || p.Name == name
	}
	if !exists {
		if err := ensureProject(c, name, true); err != nil {
			return "", err
		}
	}
	return name, remember(c, dir, name)
}

// ensureProject checks the project exists, creating it if asked to.
func ensureProject(c config, name string, create bool) error {
	if !projectName.MatchString(name) || strings.Contains(name, "--") {
		return fmt.Errorf("%q can't be a project name: use lowercase letters, numbers and single dashes", name)
	}
	err := call(c, http.MethodGet, "/api/projects/"+url.PathEscape(name)+"/settings", nil, nil)
	if err == nil {
		return nil
	}
	if !create {
		return fmt.Errorf("there's no project called %s; create it in the dashboard, or run pier deploy without --project", name)
	}
	var made struct{ Address string }
	if err := call(c, http.MethodPost, "/api/projects", map[string]string{"name": name}, &made); err != nil {
		return err
	}
	fmt.Printf("%s Created %s. Production will be at %s.\n", tick(), name, made.Address)
	return nil
}

// remember writes .pier/project.json, and keeps .pier out of git.
func remember(c config, dir, name string) error {
	if l, err := readLink(dir); err == nil && l.Project == name {
		return nil
	}
	if err := writeLink(dir, link{Project: name, Server: c.Server}); err != nil {
		return err
	}
	if exec.Command("git", "-C", dir, "rev-parse", "--is-inside-work-tree").Run() == nil {
		ignore := filepath.Join(dir, ".gitignore")
		b, _ := os.ReadFile(ignore)
		if !regexp.MustCompile(`(?m)^/?\.pier/?$`).Match(b) {
			f, err := os.OpenFile(ignore, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
			if err == nil {
				if len(b) > 0 && b[len(b)-1] != '\n' {
					f.WriteString("\n")
				}
				f.WriteString(".pier\n")
				f.Close()
			}
		}
	}
	fmt.Printf("This folder deploys to %s from now on (.pier/project.json).\n", name)
	return nil
}

type git struct{ branch, commit, message string }

// gitInfo says which branch and commit a folder is on, if it's a checkout,
// so the dashboard can show where a deployment came from.
func gitInfo(dir string) git {
	run := func(args ...string) string {
		out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).Output()
		if err != nil {
			return ""
		}
		return strings.TrimSpace(string(out))
	}
	g := git{branch: run("rev-parse", "--abbrev-ref", "HEAD"), commit: run("rev-parse", "HEAD"), message: run("log", "-1", "--format=%s")}
	if g.branch == "HEAD" {
		g.branch = ""
	}
	if g.commit != "" && run("status", "--porcelain") != "" {
		g.message += " (with uncommitted changes)"
	}
	return g
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return fmt.Sprintf("%d %ss", n, word)
}

func human(n int64) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1f MB", float64(n)/1_000_000)
	case n >= 1_000:
		return fmt.Sprintf("%.0f KB", float64(n)/1_000)
	default:
		return fmt.Sprintf("%d bytes", n)
	}
}
