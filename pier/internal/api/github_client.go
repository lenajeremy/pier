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
	"fmt"
	"io"
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
type githubRepo struct {
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

var githubHTTP = &http.Client{Timeout: 2 * time.Minute, CheckRedirect: func(r *http.Request, via []*http.Request) error {
	if len(via) >= 4 || r.URL.Scheme != "https" || (r.URL.Host != "api.github.com" && r.URL.Host != "codeload.github.com") {
		return http.ErrUseLastResponse
	}
	if r.URL.Host != via[0].URL.Host {
		r.Header.Del("Authorization")
	}
	return nil
}}

func (a *API) ghRequest(ctx context.Context, method, path, token string, body any) (*http.Response, error) {
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
	resp, err := githubHTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("GitHub could not be reached")
	}
	if resp.StatusCode/100 != 2 {
		resp.Body.Close()
		return nil, fmt.Errorf("GitHub returned %s", resp.Status)
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
func (a *API) ghRepos(ctx context.Context, app githubApp) ([]githubRepo, error) {
	jwt, err := app.jwt()
	if err != nil {
		return nil, err
	}
	repos := []githubRepo{}
	for page := 1; ; page++ {
		var installs []struct {
			ID        int64
			Suspended *string `json:"suspended_at"`
		}
		if err := a.ghJSON(ctx, "GET", fmt.Sprintf("/app/installations?per_page=100&page=%d", page), jwt, nil, &installs); err != nil {
			return nil, err
		}
		for _, inst := range installs {
			if inst.Suspended != nil {
				continue
			}
			token, err := a.ghToken(ctx, app, inst.ID, 0)
			if err != nil {
				return nil, err
			}
			for p := 1; ; p++ {
				var list struct{ Repositories []githubRepo }
				if err := a.ghJSON(ctx, "GET", fmt.Sprintf("/installation/repositories?per_page=100&page=%d", p), token, nil, &list); err != nil {
					return nil, err
				}
				for _, r := range list.Repositories {
					r.Installation = inst.ID
					r.Project, err = a.Store.GitHubProject(ctx, r.ID)
					if err != nil {
						return nil, err
					}
					repos = append(repos, r)
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
	return repos, nil
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
	return strings.ToLower(ref.Commit.SHA), err
}
