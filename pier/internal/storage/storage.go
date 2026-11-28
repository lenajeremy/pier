// Package storage gives projects buckets on the owner's Cloudflare R2. A
// project has any number of named buckets, private or public, each linked
// to the environments it serves (production, preview); the first serves
// both. Each bucket has a key that reaches it alone, and its deployments
// get the key, endpoint and bucket as variables named from the bucket's
// prefix, STORAGE_ by default; the STORAGE_ bucket also sets the standard
// AWS variables, so any S3 client works with no setup. Apps talk to R2
// directly; pierd only makes and removes buckets and keys, and reads files
// for the dashboard's viewer.
//
// A public bucket is served at files-<project>-<name>.<base domain>
// through Cloudflare's network when the base domain is on Cloudflare in
// the same account, and at its r2.dev address otherwise, which Cloudflare
// rate-limits and means for development.
package storage

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"pier/internal/secrets"
	"pier/internal/store"
)

// ErrNotConnected means Cloudflare isn't connected in Integrations.
var ErrNotConnected = errors.New("connect Cloudflare in Integrations first")

// ErrNone means the project has no bucket by that name.
var ErrNone = errors.New("there's no bucket by that name")

// Manager runs every project's storage.
type Manager struct {
	Store  *store.Store
	Box    *secrets.Box
	Log    *slog.Logger
	Domain string // the base domain, where files-<project> addresses go
	// Base and Endpoint replace Cloudflare's API and R2's S3 endpoint in
	// tests.
	Base     string
	Endpoint string
	Client   *http.Client

	mu     sync.Mutex
	locks  map[string]*sync.Mutex
	counts map[string]count
	ready  map[string]time.Time // public addresses known to serve, or when last found not to
	live   map[string]bool
}

type count struct {
	files int
	bytes int64
	more  bool
	at    time.Time
}

// Account is the connected Cloudflare account, as pierd keeps it.
type Account struct {
	ID      string
	Token   string
	TokenID string
	Zone    string // the base domain's zone, or "" when it isn't on Cloudflare here
	Prefix  string // what this server's buckets start with
}

func (m *Manager) lock(key string) func() {
	m.mu.Lock()
	if m.locks == nil {
		m.locks = map[string]*sync.Mutex{}
	}
	l := m.locks[key]
	if l == nil {
		l = &sync.Mutex{}
		m.locks[key] = l
	}
	m.mu.Unlock()
	l.Lock()
	return l.Unlock
}

const tokenBinding = "cloudflare\x00token"

func secretBinding(bucket string) string { return "storage\x00" + bucket }

// Connected reads the connected account, or ErrNotConnected.
func (m *Manager) Connected(ctx context.Context) (Account, error) {
	var a Account
	var sealed string
	for k, v := range map[string]*string{
		store.SettingCloudflareAccount: &a.ID, store.SettingCloudflareToken: &sealed, store.SettingCloudflareTokenID: &a.TokenID,
		store.SettingCloudflareZone: &a.Zone, store.SettingCloudflarePrefix: &a.Prefix,
	} {
		val, err := m.Store.Setting(ctx, k)
		if err != nil {
			return a, err
		}
		*v = val
	}
	if a.ID == "" || sealed == "" {
		return a, ErrNotConnected
	}
	tok, err := m.Box.Open(sealed, tokenBinding)
	if err != nil {
		return a, err
	}
	a.Token = tok
	return a, nil
}

func (m *Manager) api(a Account) *Cloudflare {
	return &Cloudflare{Account: a.ID, Token: a.Token, Base: m.Base, Client: m.Client}
}

// admin is an S3 client with the owner's token, which reaches every
// bucket: for the viewer and for emptying buckets.
func (m *Manager) admin(a Account) *S3 {
	return &S3{Endpoint: m.endpoint(a), Region: "auto", KeyID: a.TokenID, Secret: sha256Hex(a.Token), Client: m.Client}
}

func (m *Manager) endpoint(a Account) string {
	if m.Endpoint != "" {
		return m.Endpoint
	}
	return m.api(a).S3Endpoint()
}

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// Connect checks a Cloudflare account ID and token and keeps them. The
// token must be able to make API tokens, which Pier needs for each
// project's key; finding the base domain's zone is optional.
func (m *Manager) Connect(ctx context.Context, account, token string) (Account, error) {
	account, token = strings.TrimSpace(account), strings.TrimSpace(token)
	if !regexp.MustCompile(`^[0-9a-f]{32}$`).MatchString(account) {
		return Account{}, errors.New("an account ID is 32 letters and digits, from the account's overview page")
	}
	// Buckets made on one account can't be emptied or deleted with
	// another's token, so the account only changes once there are none.
	if current, err := m.Store.Setting(ctx, store.SettingCloudflareAccount); err != nil {
		return Account{}, err
	} else if current != "" && current != account {
		all, err := m.Store.Buckets(ctx, "")
		if err != nil {
			return Account{}, err
		}
		if len(all) > 0 {
			return Account{}, fmt.Errorf("projects still have %d buckets on the connected account; delete them from their Storage tabs before connecting another", len(all))
		}
	}
	a := Account{ID: account, Token: token}
	cf := m.api(a)
	id, err := cf.Verify(ctx)
	if err != nil {
		return a, fmt.Errorf("that token doesn't work for this account: %w", err)
	}
	a.TokenID = id
	if _, err := cf.PermissionGroup(ctx, ObjectWrite); err != nil {
		return a, fmt.Errorf("the token can't make keys for projects (it needs Account API Tokens: Edit): %w", err)
	}
	if m.Domain != "" && !strings.HasSuffix(m.Domain, "localhost") {
		if zone, err := cf.Zone(ctx, rootDomain(m.Domain)); err == nil {
			a.Zone = zone
		}
	}
	if prefix, _ := m.Store.Setting(ctx, store.SettingCloudflarePrefix); prefix != "" {
		a.Prefix = prefix
	} else {
		a.Prefix = "pier-" + strings.ToLower(rand.Text())[:4]
	}
	sealed, err := m.Box.Seal(token, tokenBinding)
	if err != nil {
		return a, err
	}
	for k, v := range map[string]string{
		store.SettingCloudflareAccount: a.ID, store.SettingCloudflareToken: sealed, store.SettingCloudflareTokenID: a.TokenID,
		store.SettingCloudflareZone: a.Zone, store.SettingCloudflarePrefix: a.Prefix,
	} {
		if err := m.Store.SetSetting(ctx, k, v); err != nil {
			return a, err
		}
	}
	return a, nil
}

// Disconnect forgets the account. It refuses while projects have storage,
// which would be left without anyone to clean it up.
func (m *Manager) Disconnect(ctx context.Context) error {
	all, err := m.Store.Buckets(ctx, "")
	if err != nil {
		return err
	}
	if len(all) > 0 {
		return fmt.Errorf("projects still have %d buckets; delete them from their Storage tabs first", len(all))
	}
	for _, k := range []string{store.SettingCloudflareAccount, store.SettingCloudflareToken, store.SettingCloudflareTokenID, store.SettingCloudflareZone} {
		if err := m.Store.SetSetting(ctx, k, ""); err != nil {
			return err
		}
	}
	return nil
}

// rootDomain is the registrable part of a host, as far as a zone goes:
// the last two labels.
func rootDomain(host string) string {
	parts := strings.Split(strings.TrimSuffix(host, "."), ".")
	if len(parts) <= 2 {
		return host
	}
	return strings.Join(parts[len(parts)-2:], ".")
}

var (
	nameRule   = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,22}[a-z0-9])?$`)
	prefixRule = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,30}$`)
	bucketSafe = regexp.MustCompile(`[^a-z0-9-]+`)
)

// Spec is what a new bucket, or a change to one, asks for.
type Spec struct {
	Name         string   `json:"name"`
	Public       bool     `json:"public"`
	Environments []string `json:"environments"`
	Prefix       string   `json:"prefix"`
}

// DefaultPrefix is the prefix of a project's first bucket's variables.
const DefaultPrefix = "STORAGE"

// targets puts environments in order and drops anything else.
func targets(envs []string) []string {
	out := []string{}
	for _, t := range []string{store.TargetProduction, store.TargetPreview} {
		for _, e := range envs {
			if e == t {
				out = append(out, t)
				break
			}
		}
	}
	return out
}

// check says what's wrong with a bucket's name, prefix or links, given the
// project's other buckets: two buckets can't give one environment the same
// variables.
func check(others []store.Bucket, name, prefix string, envs []string) error {
	if !nameRule.MatchString(name) {
		return errors.New("a name is lowercase letters, digits and dashes, up to 24")
	}
	if !prefixRule.MatchString(prefix) {
		return errors.New("a prefix is capital letters, digits and underscores, starting with a letter")
	}
	if prefix == "AWS" {
		// AWS_ACCESS_KEY_ID and the rest come from the STORAGE_ bucket.
		return errors.New("AWS_ is for the STORAGE_ bucket's standard variables; pick another prefix")
	}
	for _, o := range others {
		if o.Name == name {
			continue
		}
		if o.Prefix != prefix {
			continue
		}
		for _, e := range envs {
			if o.Serves(e) {
				return fmt.Errorf("%s gets its %s_ variables from %s already; pick another prefix", e, prefix, o.Name)
			}
		}
	}
	return nil
}

// bucketName is the R2 bucket's name: <server prefix>-<project>-<name>, and
// -<suffix> when that name is taken, the project shortened to keep it in R2's
// 63 characters.
func bucketName(prefix, project, name, suffix string) string {
	p := strings.Trim(bucketSafe.ReplaceAllString(strings.ToLower(project), "-"), "-")
	if suffix != "" {
		name += "-" + suffix
	}
	if room := min(28, 63-len(prefix)-len(name)-2); len(p) > room {
		p = strings.TrimRight(p[:room], "-")
	}
	return prefix + "-" + p + "-" + name
}

// publicHost is a public bucket's address: files-<label>-<name>, and
// -<suffix> when that's taken, the label shortened so the first part stays
// in DNS's 63 characters.
func publicHost(label, name, suffix, domain string) string {
	if suffix != "" {
		name += "-" + suffix
	}
	if room := 63 - len("files-") - len("-") - len(name); len(label) > room {
		label = strings.TrimRight(label[:room], "-")
	}
	return "files-" + label + "-" + name + "." + domain
}

// nameSuffix tells apart two buckets whose names would come out the same:
// project a's b-c and project a-b's c.
func nameSuffix() string { return strings.ToLower(rand.Text()[:4]) }

// taken is every R2 bucket name and public address this server's buckets
// use, across projects.
func (m *Manager) taken(ctx context.Context) (map[string]bool, error) {
	all, err := m.Store.Buckets(ctx, "")
	if err != nil {
		return nil, err
	}
	out := map[string]bool{}
	for _, b := range all {
		out[b.Bucket] = true
		if host := strings.TrimPrefix(b.PublicURL, "https://"); host != "" {
			out[host] = true
		}
	}
	return out, nil
}

// Add makes one of a project's buckets: the R2 bucket, its key, and for a
// public one its address. Anything made before a step that fails is
// removed again.
func (m *Manager) Add(ctx context.Context, project string, spec Spec, by string) (store.Bucket, error) {
	unlock := m.lock(project)
	defer unlock()
	spec.Name = strings.TrimSpace(strings.ToLower(spec.Name))
	spec.Prefix = strings.TrimSpace(strings.ToUpper(spec.Prefix))
	spec.Environments = targets(spec.Environments)
	others, err := m.Store.Buckets(ctx, project)
	if err != nil {
		return store.Bucket{}, err
	}
	for _, o := range others {
		if o.Name == spec.Name {
			return store.Bucket{}, fmt.Errorf("%s already has a bucket called %s", project, spec.Name)
		}
	}
	if spec.Prefix == "" {
		spec.Prefix = DefaultPrefix
		if len(others) > 0 {
			spec.Prefix = strings.ToUpper(strings.ReplaceAll(spec.Name, "-", "_"))
			if spec.Prefix[0] >= '0' && spec.Prefix[0] <= '9' {
				spec.Prefix = "B_" + spec.Prefix
			}
		}
	}
	if err := check(others, spec.Name, spec.Prefix, spec.Environments); err != nil {
		return store.Bucket{}, err
	}
	a, err := m.Connected(ctx)
	if err != nil {
		return store.Bucket{}, err
	}
	cf := m.api(a)
	taken, err := m.taken(ctx)
	if err != nil {
		return store.Bucket{}, err
	}
	name := bucketName(a.Prefix, project, spec.Name, "")
	if taken[name] {
		name = bucketName(a.Prefix, project, spec.Name, nameSuffix())
	}
	var undo []func()
	fail := func(err error) (store.Bucket, error) {
		for i := len(undo) - 1; i >= 0; i-- {
			undo[i]()
		}
		m.Log.Warn("storage: couldn't add a bucket", "project", project, "bucket", name, "err", err)
		return store.Bucket{}, err
	}
	err = cf.CreateBucket(ctx, name)
	if exists(err) {
		// Left on the account by something else, or made a moment ago for
		// another project: this one takes a name of its own.
		name = bucketName(a.Prefix, project, spec.Name, nameSuffix())
		err = cf.CreateBucket(ctx, name)
	}
	if err != nil {
		return fail(fmt.Errorf("couldn't make the bucket: %w", err))
	}
	undo = append(undo, func() { cf.DeleteBucket(context.Background(), name) })
	group, err := cf.PermissionGroup(ctx, ObjectWrite)
	if err != nil {
		return fail(err)
	}
	id, value, err := cf.CreateKey(ctx, "pier "+project+" "+spec.Name, group, []string{name})
	if err != nil {
		return fail(fmt.Errorf("couldn't make the bucket's key: %w", err))
	}
	undo = append(undo, func() { cf.DeleteKey(context.Background(), id) })
	b := store.Bucket{Project: project, Name: spec.Name, Bucket: name, Public: spec.Public, Environments: spec.Environments,
		Prefix: spec.Prefix, KeyID: id, CreatedAt: time.Now(), CreatedBy: by}
	if spec.Public {
		if a.Zone != "" && m.Domain != "" {
			host := publicHost(m.label(ctx, project), spec.Name, "", m.Domain)
			if taken[host] {
				host = publicHost(m.label(ctx, project), spec.Name, nameSuffix(), m.Domain)
			}
			if err := cf.AttachDomain(ctx, name, host, a.Zone); err != nil {
				return fail(fmt.Errorf("couldn't serve the bucket at %s: %w", host, err))
			}
			undo = append(undo, func() { cf.DetachDomain(context.Background(), name, host) })
			b.PublicURL = "https://" + host
		} else {
			host, err := cf.EnableDevURL(ctx, name)
			if err != nil {
				return fail(fmt.Errorf("couldn't turn on the bucket's public address: %w", err))
			}
			b.PublicURL = "https://" + host
		}
	}
	if b.Secret, err = m.Box.Seal(sha256Hex(value), secretBinding(name)); err != nil {
		return fail(err)
	}
	if b.ID, err = m.Store.AddBucket(ctx, b); err != nil {
		return fail(err)
	}
	m.Store.RecordEvent(context.WithoutCancel(ctx), project, "Added the bucket "+spec.Name, linkedTo(b), "", by)
	return b, nil
}

// linkedTo says, for the activity feed, what a bucket serves.
func linkedTo(b store.Bucket) string {
	switch len(b.Environments) {
	case 0:
		return "Linked to no environment."
	case 2:
		return "Linked to production and previews, as " + b.Prefix + "_."
	}
	if b.Environments[0] == store.TargetProduction {
		return "Linked to production, as " + b.Prefix + "_."
	}
	return "Linked to previews, as " + b.Prefix + "_."
}

// Link changes the environments a bucket serves and its prefix. Running
// deployments keep what they started with until they're redeployed.
func (m *Manager) Link(ctx context.Context, project, name string, envs []string, prefix, by string) (store.Bucket, error) {
	unlock := m.lock(project)
	defer unlock()
	b, err := m.Store.BucketNamed(ctx, project, name)
	if err != nil {
		return b, err
	}
	envs = targets(envs)
	prefix = strings.TrimSpace(strings.ToUpper(prefix))
	if prefix == "" {
		prefix = b.Prefix
	}
	others, err := m.Store.Buckets(ctx, project)
	if err != nil {
		return b, err
	}
	if err := check(others, name, prefix, envs); err != nil {
		return b, err
	}
	if err := m.Store.LinkBucket(ctx, project, name, envs, prefix); err != nil {
		return b, err
	}
	b.Environments, b.Prefix = envs, prefix
	m.Store.RecordEvent(context.WithoutCancel(ctx), project, "Changed the bucket "+name, linkedTo(b), "", by)
	return b, nil
}

// label is the name a project's addresses use.
func (m *Manager) label(ctx context.Context, project string) string {
	if p, err := m.Store.Project(ctx, project); err == nil && p.Label() != "" {
		return p.Label()
	}
	return project
}

// Remove empties and deletes one of a project's buckets, and its key.
func (m *Manager) Remove(ctx context.Context, project, name, by string) error {
	unlock := m.lock(project)
	defer unlock()
	b, err := m.Store.BucketNamed(ctx, project, name)
	if errors.Is(err, store.ErrNotFound) {
		return ErrNone
	}
	if err != nil {
		return err
	}
	if err := m.remove(ctx, b); err != nil {
		return err
	}
	m.Store.RecordEvent(context.WithoutCancel(ctx), project, "Deleted the bucket "+name, "Every file in it was deleted.", "", by)
	return nil
}

// Purge removes a deleted project's buckets, read before the project was
// deleted, which forgot them. It carries on past one that fails.
func (m *Manager) Purge(ctx context.Context, buckets []store.Bucket) error {
	var errs []error
	for _, b := range buckets {
		unlock := m.lock(b.Project)
		if err := m.remove(ctx, b); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", b.Name, err))
		}
		unlock()
	}
	return errors.Join(errs...)
}

func (m *Manager) remove(ctx context.Context, b store.Bucket) error {
	a, err := m.Connected(ctx)
	if err != nil {
		return err
	}
	cf, s3 := m.api(a), m.admin(a)
	if err := s3.Empty(ctx, b.Bucket); err != nil {
		return fmt.Errorf("couldn't empty %s: %w", b.Name, err)
	}
	if host, ok := strings.CutPrefix(b.PublicURL, "https://"); ok && !strings.HasSuffix(host, ".r2.dev") {
		if err := cf.DetachDomain(ctx, b.Bucket, host); err != nil {
			return err
		}
	}
	if err := cf.DeleteBucket(ctx, b.Bucket); err != nil {
		return fmt.Errorf("couldn't delete %s: %w", b.Name, err)
	}
	if err := cf.DeleteKey(ctx, b.KeyID); err != nil {
		return err
	}
	m.forget(b.Bucket)
	return m.Store.DeleteBucket(ctx, b.Project, b.Name)
}

// Variable is one variable a bucket gives its deployments.
type Variable struct {
	Name   string `json:"name"`
	Value  string `json:"value,omitempty"`
	Secret bool   `json:"secret,omitempty"`
}

// Variables are what a bucket gives a deployment: <PREFIX>_BUCKET,
// _ENDPOINT, _ACCESS_KEY_ID, _SECRET_ACCESS_KEY, _REGION and, for a public
// bucket, _PUBLIC_URL; and for the STORAGE_ bucket the standard AWS
// variables S3 clients read.
func (m *Manager) Variables(ctx context.Context, b store.Bucket) ([]Variable, error) {
	a, err := m.Connected(ctx)
	if err != nil {
		return nil, err
	}
	secret, err := m.Box.Open(b.Secret, secretBinding(b.Bucket))
	if err != nil {
		return nil, err
	}
	endpoint := m.endpoint(a)
	p := b.Prefix + "_"
	out := []Variable{
		{Name: p + "BUCKET", Value: b.Bucket},
		{Name: p + "ENDPOINT", Value: endpoint},
		{Name: p + "ACCESS_KEY_ID", Value: b.KeyID},
		{Name: p + "SECRET_ACCESS_KEY", Value: secret, Secret: true},
		{Name: p + "REGION", Value: "auto"},
	}
	if b.PublicURL != "" {
		out = append(out, Variable{Name: p + "PUBLIC_URL", Value: b.PublicURL})
	}
	if b.Prefix == DefaultPrefix {
		out = append(out,
			Variable{Name: "AWS_ACCESS_KEY_ID", Value: b.KeyID},
			Variable{Name: "AWS_SECRET_ACCESS_KEY", Value: secret, Secret: true},
			Variable{Name: "AWS_ENDPOINT_URL_S3", Value: endpoint},
			Variable{Name: "AWS_REGION", Value: "auto"},
		)
	}
	return out, nil
}

// For is a deployment's storage variables: those of every bucket linked
// to its target. A project without buckets gets none.
func (m *Manager) For(ctx context.Context, project, target string) ([]string, error) {
	all, err := m.Store.Buckets(ctx, project)
	if err != nil || len(all) == 0 {
		return nil, err
	}
	var env []string
	for _, b := range all {
		if !b.Serves(target) {
			continue
		}
		vars, err := m.Variables(ctx, b)
		if err != nil {
			return nil, err
		}
		for _, v := range vars {
			env = append(env, v.Name+"="+v.Value)
		}
	}
	return env, nil
}

// Secrets are a project's bucket secrets, which a viewer must never see.
func (m *Manager) Secrets(ctx context.Context, project string) ([]string, error) {
	all, err := m.Store.Buckets(ctx, project)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, b := range all {
		s, err := m.Box.Open(b.Secret, secretBinding(b.Bucket))
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, nil
}

// CountUpTo is the most objects Pier counts in a bucket.
const CountUpTo = 10_000

// Count counts a bucket's files and their size, up to CountUpTo, keeping
// the answer for two minutes.
func (m *Manager) Count(ctx context.Context, b store.Bucket) (int, int64, bool, error) {
	m.mu.Lock()
	c, ok := m.counts[b.Bucket]
	m.mu.Unlock()
	if ok && time.Since(c.at) < 2*time.Minute {
		return c.files, c.bytes, c.more, nil
	}
	a, err := m.Connected(ctx)
	if err != nil {
		return 0, 0, false, err
	}
	files, bytes, more, err := m.admin(a).Count(ctx, b.Bucket, CountUpTo)
	if err != nil {
		return 0, 0, false, err
	}
	m.mu.Lock()
	if m.counts == nil {
		m.counts = map[string]count{}
	}
	m.counts[b.Bucket] = count{files, bytes, more, time.Now()}
	m.mu.Unlock()
	return files, bytes, more, nil
}

// forget drops a bucket's count, after a change.
func (m *Manager) forget(bucket string) {
	m.mu.Lock()
	delete(m.counts, bucket)
	m.mu.Unlock()
}

// Objects is one of a project's buckets and the S3 client the viewer
// reads and writes its files with.
func (m *Manager) Objects(ctx context.Context, project, name string) (store.Bucket, *S3, error) {
	b, err := m.Store.BucketNamed(ctx, project, name)
	if err != nil {
		return b, nil, err
	}
	a, err := m.Connected(ctx)
	if err != nil {
		return b, nil, err
	}
	return b, m.admin(a), nil
}

// Changed drops a bucket's cached count after the viewer changes a file.
func (m *Manager) Changed(b store.Bucket) { m.forget(b.Bucket) }

// AddressReady reports whether a public bucket's address serves yet. An
// r2.dev address always does; one on the base domain does once Cloudflare
// has its record and certificate, which takes a minute or so. Once it
// serves, Pier stops asking; until then it asks at most every 15 seconds.
func (m *Manager) AddressReady(ctx context.Context, b store.Bucket) bool {
	host, ok := strings.CutPrefix(b.PublicURL, "https://")
	if !ok || strings.HasSuffix(host, ".r2.dev") {
		return true
	}
	m.mu.Lock()
	if m.live[b.Bucket] {
		m.mu.Unlock()
		return true
	}
	if at, ok := m.ready[b.Bucket]; ok && time.Since(at) < 15*time.Second {
		m.mu.Unlock()
		return false
	}
	m.mu.Unlock()
	a, err := m.Connected(ctx)
	if err != nil {
		return false
	}
	live, err := m.api(a).DomainReady(ctx, b.Bucket, host)
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.ready == nil {
		m.ready, m.live = map[string]time.Time{}, map[string]bool{}
	}
	m.ready[b.Bucket] = time.Now()
	if err == nil && live {
		m.live[b.Bucket] = true
	}
	return err == nil && live
}

// FileURL is where a public bucket serves a file, or "" for a private one.
func FileURL(b store.Bucket, key string) string {
	if b.PublicURL == "" {
		return ""
	}
	return b.PublicURL + "/" + encodePath(key)
}
