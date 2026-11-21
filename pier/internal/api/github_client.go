package api

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type githubApp struct {
	ID     int64  `json:"id"`
	Slug   string `json:"slug"`
	Key    string `json:"pem"`
	Secret string `json:"webhook_secret"`
	Hook   string `json:"hook"`
}
type githubInstallation struct {
	ID      int64 `json:"id"`
	Account struct {
		Login string `json:"login"`
		Type  string `json:"type"`
	} `json:"account"`
	URL       string  `json:"html_url"`
	Suspended *string `json:"suspended_at,omitempty"`
}

type githubCatalog struct {
	Repositories  []githubRepo         `json:"repositories"`
	Installations []githubInstallation `json:"installations"`
}

type githubRepo struct {
	Language     string `json:"language"`
	PushedAt     string `json:"pushed_at"`
	ID           int64  `json:"id"`
	Name         string `json:"full_name"`
	Branch       string `json:"default_branch"`
	Private      bool   `json:"private"`
	Installation int64  `json:"installation"`
	Project      string `json:"project,omitempty"`
}

var repoName = regexp.MustCompile("^[a-zA-Z0-9_.-]+/[a-zA-Z0-9_.-]+$")
var commitSHA = regexp.MustCompile("^[a-f0-9]{40}$")

func (app githubApp) jwt() (string, error) {
	block, _ := pem.Decode([]byte(app.Key))
	if block == nil {
		return "", fmt.Errorf("invalid GitHub App key")
	}
	key, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		return "", err
	}
	enc := base64.RawURLEncoding.EncodeToString
	now := time.Now()
	claims, _ := json.Marshal(map[string]any{"iss": strconv.FormatInt(app.ID, 10), "iat": now.Add(-time.Minute).Unix(), "exp": now.Add(9 * time.Minute).Unix()})
	data := enc([]byte(`{"alg":"RS256","typ":"JWT"}`)) + "." + enc(claims)
	digest := sha256.Sum256([]byte(data))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	return data + "." + enc(sig), err
}

// githubRedirect follows GitHub's own redirects only, and keeps the token
// from the host it wasn't issued for.
func githubRedirect(r *http.Request, via []*http.Request) error {
	if len(via) >= 4 || r.URL.Scheme != "https" || (r.URL.Host != "api.github.com" && r.URL.Host != "codeload.github.com") {
		return http.ErrUseLastResponse
	}
	if r.URL.Host != via[0].URL.Host {
		r.Header.Del("Authorization")
	}
	return nil
}

var githubHTTP = &http.Client{Timeout: 2 * time.Minute, CheckRedirect: githubRedirect}

// githubArchive downloads source archives. A client's Timeout also covers
// reading the body, which would cap how large a repository a slow link can
// fetch, so this one bounds each step up to the response instead. After
// that, downloadGitHub ends a download that stops arriving.
var githubArchive = &http.Client{CheckRedirect: githubRedirect, Transport: &http.Transport{
	Proxy:                 http.ProxyFromEnvironment,
	DialContext:           (&net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
	ForceAttemptHTTP2:     true,
	TLSHandshakeTimeout:   10 * time.Second,
	ResponseHeaderTimeout: time.Minute,
	IdleConnTimeout:       90 * time.Second,
}}

// ghError is a GitHub API call that failed. Status is the HTTP status, or 0
// when no answer arrived.
type ghError struct {
	Status  int
	Limited bool // GitHub refused the call for its rate limit
	msg     string
}

func (e *ghError) Error() string { return e.msg }

// temporary reports whether err is a failure that trying again may get past:
// GitHub couldn't be reached, had an error of its own, or is limiting calls.
func temporary(err error) bool {
	var e *ghError
	return errors.As(err, &e) && (e.Status == 0 || e.Status >= 500 || e.Limited)
}

func (a *API) ghRequest(ctx context.Context, method, path, token string, body any) (*http.Response, error) {
	return a.ghSend(ctx, githubHTTP, method, path, token, body)
}

func (a *API) ghSend(ctx context.Context, client *http.Client, method, path, token string, body any) (*http.Response, error) {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reader = bytes.NewReader(raw)
	}
	root := a.githubEndpoint
	if root == "" {
		root = "https://api.github.com"
	}
	req, err := http.NewRequestWithContext(ctx, method, root+path, reader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "Pier-GitHub-App")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, &ghError{msg: "GitHub could not be reached"}
	}
	if resp.StatusCode/100 != 2 {
		resp.Body.Close()
		// GitHub answers 429, or 403 with one of these headers, when it is
		// limiting calls rather than refusing this one.
		limited := resp.StatusCode == http.StatusTooManyRequests ||
			resp.StatusCode == http.StatusForbidden && (resp.Header.Get("Retry-After") != "" || resp.Header.Get("X-RateLimit-Remaining") == "0")
		return nil, &ghError{Status: resp.StatusCode, Limited: limited, msg: "GitHub returned " + resp.Status}
	}
	return resp, nil
}
func (a *API) ghJSON(ctx context.Context, method, path, token string, body, out any) error {
	r, err := a.ghRequest(ctx, method, path, token, body)
	if err != nil {
		return err
	}
	defer r.Body.Close()
	if out == nil {
		return nil
	}
	return json.NewDecoder(io.LimitReader(r.Body, 16<<20)).Decode(out)
}
func (a *API) ghToken(ctx context.Context, app githubApp, install, repo int64) (string, error) {
	jwt, err := app.jwt()
	if err != nil {
		return "", err
	}
	var out struct{ Token string }
	var scope any
	if repo > 0 {
		scope = map[string]any{"repository_ids": []int64{repo}}
	}
	err = a.ghJSON(ctx, "POST", fmt.Sprintf("/app/installations/%d/access_tokens", install), jwt, scope, &out)
	if err == nil && out.Token == "" {
		err = fmt.Errorf("GitHub did not return an installation token")
	}
	return out.Token, err
}
func (a *API) ghRepos(ctx context.Context, app githubApp) (githubCatalog, error) {
	catalog := githubCatalog{Repositories: []githubRepo{}, Installations: []githubInstallation{}}
	jwt, err := app.jwt()
	if err != nil {
		return catalog, err
	}

	for page := 1; ; page++ {
		var installs []githubInstallation
		if err := a.ghJSON(ctx, "GET", fmt.Sprintf("/app/installations?per_page=100&page=%d", page), jwt, nil, &installs); err != nil {
			return catalog, err
		}
		for _, inst := range installs {
			if inst.Suspended != nil {
				continue
			}
			catalog.Installations = append(catalog.Installations, inst)
			token, err := a.ghToken(ctx, app, inst.ID, 0)
			if err != nil {
				return catalog, err
			}
			for p := 1; ; p++ {
				var list struct{ Repositories []githubRepo }
				if err := a.ghJSON(ctx, "GET", fmt.Sprintf("/installation/repositories?per_page=100&page=%d", p), token, nil, &list); err != nil {
					return catalog, err
				}
				for _, r := range list.Repositories {
					r.Installation = inst.ID
					r.Project, err = a.Store.GitHubProject(ctx, r.ID)
					if err != nil {
						return catalog, err
					}
					catalog.Repositories = append(catalog.Repositories, r)
				}
				if len(list.Repositories) < 100 {
					break
				}
			}
		}
		if len(installs) < 100 {
			break
		}
	}
	return catalog, nil
}

func (a *API) ghRepo(ctx context.Context, token string, id int64) (githubRepo, error) {
	var r githubRepo
	err := a.ghJSON(ctx, "GET", fmt.Sprintf("/repositories/%d", id), token, nil, &r)
	if err == nil && (r.ID != id || !repoName.MatchString(r.Name)) {
		err = fmt.Errorf("the installed repository is no longer available")
	}
	return r, err
}
func (a *API) ghHead(ctx context.Context, token, repo, branch string, pr int) (string, error) {
	if pr > 0 {
		var pull struct {
			State string
			Head  struct{ SHA string }
		}
		err := a.ghJSON(ctx, "GET", fmt.Sprintf("/repos/%s/pulls/%d", repo, pr), token, nil, &pull)
		if pull.State != "open" && err == nil {
			return "", nil
		}
		return pull.Head.SHA, err
	}
	var ref struct{ Commit struct{ SHA string } }
	err := a.ghJSON(ctx, "GET", "/repos/"+repo+"/branches/"+url.PathEscape(branch), token, nil, &ref)
	// A branch deleted since its push has no head, like a closed pull request.
	if e := (*ghError)(nil); errors.As(err, &e) && e.Status == http.StatusNotFound {
		return "", nil
	}
	return strings.ToLower(ref.Commit.SHA), err
}
