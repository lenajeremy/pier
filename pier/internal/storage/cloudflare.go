package storage

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// CloudflareAPI is Cloudflare's REST API.
const CloudflareAPI = "https://api.cloudflare.com/client/v4"

// Cloudflare is a client for the few Cloudflare calls Pier makes with the
// owner's account token: buckets, the keys that reach one project's
// buckets, and the addresses public buckets are served at.
type Cloudflare struct {
	Account string
	Token   string
	Base    string // CloudflareAPI unless testing
	Client  *http.Client
}

// APIError is what Cloudflare said went wrong.
type APIError struct {
	Status int
	Errors []struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	}
}

func (e *APIError) Error() string {
	var msgs []string
	for _, x := range e.Errors {
		msgs = append(msgs, x.Message)
	}
	if len(msgs) == 0 {
		return fmt.Sprintf("Cloudflare answered %d", e.Status)
	}
	return "Cloudflare said: " + strings.Join(msgs, "; ")
}

// call sends one request and decodes its result.
func (c *Cloudflare) call(ctx context.Context, method, path string, body, into any) error {
	base := c.Base
	if base == "" {
		base = CloudflareAPI
	}
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, base+path, r)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	client := c.Client
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("couldn't reach Cloudflare: %w", err)
	}
	defer resp.Body.Close()
	var env struct {
		Success    bool            `json:"success"`
		Errors     json.RawMessage `json:"errors"`
		Result     json.RawMessage `json:"result"`
		ResultInfo struct {
			Page       int `json:"page"`
			TotalPages int `json:"total_pages"`
		} `json:"result_info"`
	}
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err := json.Unmarshal(raw, &env); err != nil {
		return fmt.Errorf("Cloudflare answered %d with something Pier couldn't read", resp.StatusCode)
	}
	if !env.Success || resp.StatusCode >= 300 {
		e := &APIError{Status: resp.StatusCode}
		json.Unmarshal(env.Errors, &e.Errors)
		return e
	}
	if into != nil && len(env.Result) > 0 {
		return json.Unmarshal(env.Result, into)
	}
	return nil
}

func (c *Cloudflare) account() string { return "/accounts/" + url.PathEscape(c.Account) }

// Verify checks the token works and returns its ID, which is also the
// access key ID of the S3 credentials made from it.
func (c *Cloudflare) Verify(ctx context.Context) (string, error) {
	var r struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	if err := c.call(ctx, http.MethodGet, c.account()+"/tokens/verify", nil, &r); err != nil {
		return "", err
	}
	if r.Status != "active" {
		return "", fmt.Errorf("the token is %s, not active", r.Status)
	}
	return r.ID, nil
}

// Zone finds the zone for a domain in this account, or "" if it isn't on
// Cloudflare here.
func (c *Cloudflare) Zone(ctx context.Context, name string) (string, error) {
	var zones []struct {
		ID     string `json:"id"`
		Name   string `json:"name"`
		Status string `json:"status"`
	}
	q := url.Values{"name": {name}, "account.id": {c.Account}}
	if err := c.call(ctx, http.MethodGet, "/zones?"+q.Encode(), nil, &zones); err != nil {
		return "", err
	}
	for _, z := range zones {
		if strings.EqualFold(z.Name, name) {
			return z.ID, nil
		}
	}
	return "", nil
}

// PermissionGroup finds an account token permission group's ID by its name.
func (c *Cloudflare) PermissionGroup(ctx context.Context, name string) (string, error) {
	var groups []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	if err := c.call(ctx, http.MethodGet, c.account()+"/tokens/permission_groups?"+url.Values{"name": {name}}.Encode(), nil, &groups); err != nil {
		return "", err
	}
	for _, g := range groups {
		if g.Name == name {
			return g.ID, nil
		}
	}
	return "", fmt.Errorf("Cloudflare has no permission called %q for account tokens", name)
}

// CreateBucket makes an R2 bucket.
func (c *Cloudflare) CreateBucket(ctx context.Context, name string) error {
	return c.call(ctx, http.MethodPost, c.account()+"/r2/buckets", map[string]string{"name": name}, nil)
}

// DeleteBucket removes an empty bucket. One that's gone already counts.
func (c *Cloudflare) DeleteBucket(ctx context.Context, name string) error {
	err := c.call(ctx, http.MethodDelete, c.account()+"/r2/buckets/"+url.PathEscape(name), nil, nil)
	var e *APIError
	if errors.As(err, &e) && e.Status == http.StatusNotFound {
		return nil
	}
	return err
}

// ObjectWrite is the account token permission that reads, writes and
// lists objects in the buckets a token names.
const ObjectWrite = "Workers R2 Storage Bucket Item Write"

// CreateKey makes an account token that can read and write objects in
// these buckets only, and returns its ID and value. The S3 secret is the
// SHA-256 of the value.
func (c *Cloudflare) CreateKey(ctx context.Context, name, group string, buckets []string) (string, string, error) {
	resources := map[string]string{}
	for _, b := range buckets {
		resources["com.cloudflare.edge.r2.bucket."+c.Account+"_default_"+b] = "*"
	}
	body := map[string]any{
		"name": name,
		"policies": []any{map[string]any{
			"effect":            "allow",
			"resources":         resources,
			"permission_groups": []any{map[string]string{"id": group}},
		}},
	}
	var r struct {
		ID    string `json:"id"`
		Value string `json:"value"`
	}
	if err := c.call(ctx, http.MethodPost, c.account()+"/tokens", body, &r); err != nil {
		return "", "", err
	}
	if r.ID == "" || r.Value == "" {
		return "", "", errors.New("Cloudflare made the key but didn't send it back")
	}
	return r.ID, r.Value, nil
}

// DeleteKey removes an account token. One that's gone already counts.
func (c *Cloudflare) DeleteKey(ctx context.Context, id string) error {
	err := c.call(ctx, http.MethodDelete, c.account()+"/tokens/"+url.PathEscape(id), nil, nil)
	var e *APIError
	if errors.As(err, &e) && e.Status == http.StatusNotFound {
		return nil
	}
	return err
}

// AttachDomain serves a bucket at an address in one of the account's zones,
// through Cloudflare's network and cache.
func (c *Cloudflare) AttachDomain(ctx context.Context, bucket, domain, zone string) error {
	return c.call(ctx, http.MethodPost, c.account()+"/r2/buckets/"+url.PathEscape(bucket)+"/domains/custom",
		map[string]any{"domain": domain, "zoneId": zone, "enabled": true, "minTLS": "1.2"}, nil)
}

// DetachDomain stops serving a bucket at an address. One that's gone
// already counts.
func (c *Cloudflare) DetachDomain(ctx context.Context, bucket, domain string) error {
	err := c.call(ctx, http.MethodDelete, c.account()+"/r2/buckets/"+url.PathEscape(bucket)+"/domains/custom/"+url.PathEscape(domain), nil, nil)
	var e *APIError
	if errors.As(err, &e) && e.Status == http.StatusNotFound {
		return nil
	}
	return err
}

// DomainReady reports whether a bucket's address on a zone serves yet:
// Cloudflare has its record and its certificate.
func (c *Cloudflare) DomainReady(ctx context.Context, bucket, domain string) (bool, error) {
	var r struct {
		Status struct {
			Ownership string `json:"ownership"`
			SSL       string `json:"ssl"`
		} `json:"status"`
	}
	if err := c.call(ctx, http.MethodGet, c.account()+"/r2/buckets/"+url.PathEscape(bucket)+"/domains/custom/"+url.PathEscape(domain), nil, &r); err != nil {
		return false, err
	}
	return r.Status.Ownership == "active" && r.Status.SSL == "active", nil
}

// BucketNames lists the account's buckets whose names start with prefix.
func (c *Cloudflare) BucketNames(ctx context.Context, prefix string) ([]string, error) {
	var r struct {
		Buckets []struct {
			Name string `json:"name"`
		} `json:"buckets"`
	}
	if err := c.call(ctx, http.MethodGet, c.account()+"/r2/buckets?"+url.Values{"name_contains": {prefix}, "per_page": {"1000"}}.Encode(), nil, &r); err != nil {
		return nil, err
	}
	var out []string
	for _, b := range r.Buckets {
		if strings.HasPrefix(b.Name, prefix) {
			out = append(out, b.Name)
		}
	}
	return out, nil
}

// Keys lists the account's tokens whose names start with prefix, by ID.
func (c *Cloudflare) Keys(ctx context.Context, prefix string) (map[string]string, error) {
	var r []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	if err := c.call(ctx, http.MethodGet, c.account()+"/tokens?per_page=50", nil, &r); err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, k := range r {
		if strings.HasPrefix(k.Name, prefix) {
			out[k.ID] = k.Name
		}
	}
	return out, nil
}

// EnableDevURL serves a bucket at its r2.dev address, which Cloudflare
// rate-limits and doesn't cache: for previews only. It returns the host.
func (c *Cloudflare) EnableDevURL(ctx context.Context, bucket string) (string, error) {
	var r struct {
		Domain string `json:"domain"`
	}
	if err := c.call(ctx, http.MethodPut, c.account()+"/r2/buckets/"+url.PathEscape(bucket)+"/domains/managed", map[string]bool{"enabled": true}, &r); err != nil {
		return "", err
	}
	return r.Domain, nil
}

// S3Endpoint is the account's S3 endpoint.
func (c *Cloudflare) S3Endpoint() string { return "https://" + c.Account + ".r2.cloudflarestorage.com" }
