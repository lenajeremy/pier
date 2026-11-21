package api

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"pier/internal/monorepo"
	"pier/internal/store"
)

// A listing is of one commit or one upload, so it never goes stale. The
// cache is only there because the dashboard asks again as the owner types a
// root directory, and its time and size only bound what it holds.
const (
	folderCacheTime = 5 * time.Minute
	folderCacheSize = 32
)

type folderCache struct {
	mu      sync.Mutex
	entries map[string]cachedFolders
}

type cachedFolders struct {
	folders   []monorepo.Folder
	truncated bool
	at        time.Time
}

func (c *folderCache) get(key string) (cachedFolders, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[key]
	if !ok || time.Since(e.at) > folderCacheTime {
		return cachedFolders{}, false
	}
	return e, true
}

func (c *folderCache) put(key string, folders []monorepo.Folder, truncated bool) cachedFolders {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = map[string]cachedFolders{}
	}
	// Make room by forgetting what has expired, or else the oldest.
	if len(c.entries) >= folderCacheSize {
		oldest := ""
		for k, e := range c.entries {
			if time.Since(e.at) > folderCacheTime {
				delete(c.entries, k)
			} else if oldest == "" || e.at.Before(c.entries[oldest].at) {
				oldest = k
			}
		}
		if len(c.entries) >= folderCacheSize {
			delete(c.entries, oldest)
		}
	}
	e := cachedFolders{folders: folders, truncated: truncated, at: time.Now()}
	c.entries[key] = e
	return e
}

// folderListing is what both folder endpoints answer.
type folderListing struct {
	Ref        string            `json:"ref"` // the commit listed; for an upload, the one pier deploy named, if any
	Branch     string            `json:"branch"`
	Folders    []monorepo.Folder `json:"folders"`
	Truncated  bool              `json:"truncated"`            // some folders were left out
	Source     string            `json:"source,omitempty"`     // for a project: github, git or upload
	Deployment string            `json:"deployment,omitempty"` // for an upload: the deployment it came with
}

// listError is why folders couldn't be listed, and the status to answer.
type listError struct {
	status int
	msg    string
}

// repoFolders lists a repository's folders at a branch's head, the
// repository's default branch unless one is asked for, so a root directory
// can be chosen before the repository is imported.
func (a *API) repoFolders(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	installation, _ := strconv.ParseInt(r.URL.Query().Get("installation"), 10, 64)
	if id <= 0 || installation <= 0 {
		githubError(w, 400, "Choose a repository and the installation that reads it.")
		return
	}
	app, err := a.loadApp(r.Context())
	if err != nil {
		a.fail(w, err)
		return
	}
	if app.ID == 0 {
		githubError(w, 409, "Connect GitHub first.")
		return
	}
	listing, lerr := a.githubFolders(r.Context(), app, installation, id, strings.TrimSpace(r.URL.Query().Get("branch")))
	if lerr != nil {
		githubError(w, lerr.status, lerr.msg)
		return
	}
	writeJSON(w, 200, listing)
}

// projectFolders lists the folders of what a project deploys, so its root
// directory can be chosen from them: its GitHub repository at the production
// branch, or the production branch of its own git repository, or else its
// newest deployment's upload.
func (a *API) projectFolders(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("project")
	ps, ok := a.findProject(w, r, name)
	if !ok {
		return
	}
	ctx := r.Context()
	repoID, installation, err := a.Store.GitHubLink(ctx, name)
	if err == nil {
		app, err := a.loadApp(ctx)
		if err != nil {
			a.fail(w, err)
			return
		}
		if app.ID == 0 {
			githubError(w, 409, "Connect GitHub first.")
			return
		}
		listing, lerr := a.githubFolders(ctx, app, installation, repoID, ps.productionBranch)
		if lerr != nil {
			githubError(w, lerr.status, lerr.msg)
			return
		}
		listing.Source = "github"
		writeJSON(w, 200, listing)
		return
	}
	if !errors.Is(err, store.ErrNotFound) {
		a.fail(w, err)
		return
	}
	listing, found, err := a.gitFolders(ctx, name, ps.productionBranch)
	if err == nil && !found {
		listing, found, err = a.uploadFolders(ps.deployments)
	}
	if err != nil {
		a.fail(w, err)
		return
	}
	if !found {
		githubError(w, 404, "Nothing has been deployed to "+name+" yet, so there are no folders to show.")
		return
	}
	writeJSON(w, 200, listing)
}

// githubFolders lists a repository's folders at the head of branch, or of
// its default branch when branch is empty.
func (a *API) githubFolders(ctx context.Context, app githubApp, installation, repoID int64, branch string) (folderListing, *listError) {
	token, err := a.ghToken(ctx, app, installation, repoID)
	if err != nil {
		return folderListing{}, githubListError(err, "Pier can't see that repository through this installation.")
	}
	repo, err := a.ghRepo(ctx, token, repoID)
	if e := (*ghError)(nil); err != nil && !errors.As(err, &e) {
		// GitHub answered for another repository than the one asked for.
		return folderListing{}, &listError{404, "That repository isn't available to Pier any more."}
	}
	if err != nil {
		return folderListing{}, githubListError(err, "That repository isn't available to Pier any more.")
	}
	if branch == "" {
		branch = repo.Branch
	}
	missing := repo.Name + " has no branch called " + branch + "."
	sha, err := a.ghHead(ctx, token, repo.Name, branch, 0)
	if err != nil {
		return folderListing{}, githubListError(err, missing)
	}
	if !commitSHA.MatchString(sha) {
		return folderListing{}, &listError{404, missing}
	}
	key := fmt.Sprintf("github %d %s", repoID, sha)
	e, ok := a.folders.get(key)
	if !ok {
		var tree struct {
			Tree []struct {
				Path string `json:"path"`
				Type string `json:"type"`
			} `json:"tree"`
			Truncated bool `json:"truncated"`
		}
		if err := a.ghJSON(ctx, "GET", "/repos/"+repo.Name+"/git/trees/"+sha+"?recursive=1", token, nil, &tree); err != nil {
			return folderListing{}, githubListError(err, "GitHub has no files for "+sha[:7]+" in "+repo.Name+".")
		}
		var files []string
		for _, t := range tree.Tree {
			// Folders follow from the files in them, and a submodule's are
			// in another repository.
			if t.Type == "blob" {
				files = append(files, t.Path)
			}
		}
		folders, capped := monorepo.Folders(files)
		e = a.folders.put(key, folders, capped || tree.Truncated)
	}
	return folderListing{Ref: sha, Branch: branch, Folders: e.folders, Truncated: e.truncated}, nil
}

// githubListError answers notFound when GitHub says what was asked for
// isn't there or isn't Pier's to see, and passes any other failure on as
// GitHub's.
func githubListError(err error, notFound string) *listError {
	var e *ghError
	if errors.As(err, &e) && (e.Status == http.StatusNotFound || e.Status == http.StatusUnprocessableEntity || e.Status == http.StatusForbidden && !e.Limited) {
		return &listError{404, notFound}
	}
	return &listError{502, err.Error()}
}

// gitFolders lists a project's own git repository at branch. found is false
// when the repository has no commit on branch yet.
func (a *API) gitFolders(ctx context.Context, project, branch string) (listing folderListing, found bool, err error) {
	if a.Deployer.Repos == "" {
		return listing, false, nil
	}
	repo := a.Deployer.RepoPath(project)
	if _, err := os.Stat(repo); err != nil {
		return listing, false, nil
	}
	out, err := exec.CommandContext(ctx, "git", "--git-dir", repo, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch+"^{commit}").Output()
	if err != nil {
		return listing, false, nil
	}
	sha := strings.TrimSpace(string(out))
	key := "git " + sha
	e, ok := a.folders.get(key)
	if !ok {
		out, err := exec.CommandContext(ctx, "git", "--git-dir", repo, "ls-tree", "-r", "-z", "--name-only", sha).Output()
		if err != nil {
			return listing, false, fmt.Errorf("listing %s at %s: %w", project, sha, err)
		}
		folders, truncated := monorepo.Folders(strings.Split(strings.TrimSuffix(string(out), "\x00"), "\x00"))
		e = a.folders.put(key, folders, truncated)
	}
	return folderListing{Ref: sha, Branch: branch, Folders: e.folders, Truncated: e.truncated, Source: "git"}, true, nil
}

// uploadFolders lists the newest deployment's upload that is still kept.
// found is false when none is.
func (a *API) uploadFolders(deployments []Deployment) (listing folderListing, found bool, err error) {
	if a.Deployer.Sources == "" {
		return listing, false, nil
	}
	for _, d := range deployments {
		key := "upload " + d.ID
		e, ok := a.folders.get(key)
		if !ok {
			// The deployer keeps each upload as <id>.tar.gz in Sources.
			f, err := os.Open(filepath.Join(a.Deployer.Sources, d.ID+".tar.gz"))
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			if err != nil {
				return listing, false, err
			}
			repo, err := monorepo.ReadArchive(f, d.Source == store.SourceGitHub)
			f.Close()
			if err != nil {
				a.Log.Warn("a deployment's kept source can't be read", "deployment", d.ID, "err", err)
				continue
			}
			folders, truncated := repo.Folders()
			e = a.folders.put(key, folders, truncated)
		}
		return folderListing{Ref: d.Commit, Branch: d.Branch, Folders: e.folders, Truncated: e.truncated, Source: "upload", Deployment: d.ID}, true, nil
	}
	return listing, false, nil
}
