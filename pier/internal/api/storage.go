package api

import (
	"encoding/json"
	"errors"
	"mime"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"pier/internal/storage"
	"pier/internal/store"
)

// cloudflareView is the Cloudflare connection, as Integrations shows it.
// The token never leaves pierd.
type cloudflareView struct {
	Connected bool   `json:"connected"`
	Account   string `json:"account,omitempty"`
	// Domain is the base domain when it's on Cloudflare in this account,
	// so public buckets are served from it; empty when they use r2.dev.
	Domain string `json:"domain,omitempty"`
	Prefix string `json:"prefix,omitempty"`
	// Buckets is every project's buckets, for Integrations.
	Buckets []bucketView `json:"buckets,omitempty"`
}

func (a *API) cloudflareView(r *http.Request) (cloudflareView, error) {
	acct, err := a.Storage.Connected(r.Context())
	if errors.Is(err, storage.ErrNotConnected) {
		return cloudflareView{}, nil
	}
	if err != nil {
		return cloudflareView{}, err
	}
	v := cloudflareView{Connected: true, Account: acct.ID, Prefix: acct.Prefix}
	if acct.Zone != "" {
		v.Domain = a.Storage.Domain
	}
	return v, nil
}

func (a *API) getCloudflare(w http.ResponseWriter, r *http.Request) {
	if a.Storage == nil {
		writeJSON(w, http.StatusOK, cloudflareView{})
		return
	}
	v, err := a.cloudflareView(r)
	if err != nil {
		a.fail(w, err)
		return
	}
	if v.Connected {
		all, err := a.Store.Buckets(r.Context(), "")
		if err != nil {
			a.fail(w, err)
			return
		}
		v.Buckets = a.bucketViews(r, all, true)
	}
	writeJSON(w, http.StatusOK, v)
}

// bucketViews adds each bucket's count and variable names.
func (a *API) bucketViews(r *http.Request, all []store.Bucket, count bool) []bucketView {
	out := []bucketView{}
	for _, b := range all {
		bv := bucketView{Bucket: b, Variables: []string{}}
		if vars, err := a.Storage.Variables(r.Context(), b); err == nil {
			for _, x := range vars {
				bv.Variables = append(bv.Variables, x.Name)
			}
		}
		if count {
			var err error
			if bv.Files, bv.Bytes, bv.More, err = a.Storage.Count(r.Context(), b); err != nil {
				bv.CountError = sentenceOf(err)
			}
			bv.AddressReady = !b.Public || a.Storage.AddressReady(r.Context(), b)
		}
		out = append(out, bv)
	}
	return out
}

func (a *API) connectCloudflare(w http.ResponseWriter, r *http.Request) {
	if a.Storage == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "Storage isn't available on this server."})
		return
	}
	var req struct {
		Account string `json:"account"`
		Token   string `json:"token"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req) != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "that isn't JSON"})
		return
	}
	if strings.TrimSpace(req.Token) == "" {
		fieldError(w, "token", "Paste the API token.")
		return
	}
	if _, err := a.Storage.Connect(r.Context(), req.Account, req.Token); err != nil {
		field := "token"
		if strings.Contains(err.Error(), "account ID") {
			field = "account"
		}
		fieldError(w, field, sentenceOf(err))
		return
	}
	a.getCloudflare(w, r)
}

func (a *API) disconnectCloudflare(w http.ResponseWriter, r *http.Request) {
	if a.Storage == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "Storage isn't available on this server."})
		return
	}
	if err := a.Storage.Disconnect(r.Context()); err != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": sentenceOf(err)})
		return
	}
	a.getCloudflare(w, r)
}

// bucketView is one bucket on the Storage tab: what it serves, how much
// it holds, and the names of the variables it gives.
type bucketView struct {
	store.Bucket
	Files      int      `json:"files"`
	Bytes      int64    `json:"bytes"`
	More       bool     `json:"more"` // more files than Pier counts
	CountError string   `json:"count_error,omitempty"`
	Variables  []string `json:"variables"`
	// AddressReady says a public bucket's address serves; a new one on the
	// base domain takes a minute.
	AddressReady bool `json:"address_ready"`
}

type storageView struct {
	Cloudflare cloudflareView `json:"cloudflare"`
	Buckets    []bucketView   `json:"buckets"`
	CountUpTo  int            `json:"count_up_to"`
}

func (a *API) storageProject(w http.ResponseWriter, r *http.Request) (string, bool) {
	name := r.PathValue("project")
	if a.Storage == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "Storage isn't available on this server."})
		return "", false
	}
	if _, err := a.Store.Project(r.Context(), name); errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "there's no project called " + name})
		return "", false
	} else if err != nil {
		a.fail(w, err)
		return "", false
	}
	return name, true
}

// storageBucket finds the bucket in the path, for the routes that use one.
func (a *API) storageBucket(w http.ResponseWriter, r *http.Request) (store.Bucket, *storage.S3, bool) {
	project, ok := a.storageProject(w, r)
	if !ok {
		return store.Bucket{}, nil, false
	}
	b, s3, err := a.Storage.Objects(r.Context(), project, r.PathValue("bucket"))
	if err != nil {
		a.storageError(w, err)
		return b, nil, false
	}
	return b, s3, true
}

func (a *API) storageError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound), errors.Is(err, storage.ErrNone):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "There's no bucket by that name."})
	case errors.Is(err, storage.ErrNotConnected):
		writeJSON(w, http.StatusConflict, map[string]string{"error": "Connect Cloudflare in Integrations first."})
	case storage.NotFound(err):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "That file isn't there."})
	default:
		a.Log.Warn("storage", "err", err)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": sentenceOf(err)})
	}
}

func (a *API) getStorage(w http.ResponseWriter, r *http.Request) {
	project, ok := a.storageProject(w, r)
	if !ok {
		return
	}
	cf, err := a.cloudflareView(r)
	if err != nil {
		a.fail(w, err)
		return
	}
	all, err := a.Store.Buckets(r.Context(), project)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, storageView{Cloudflare: cf, Buckets: a.bucketViews(r, all, cf.Connected), CountUpTo: storage.CountUpTo})
}

func (a *API) addBucket(w http.ResponseWriter, r *http.Request) {
	project, ok := a.storageProject(w, r)
	if !ok {
		return
	}
	var spec storage.Spec
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&spec) != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "that isn't JSON"})
		return
	}
	ctx, cancel := work(r)
	defer cancel()
	if _, err := a.Storage.Add(ctx, project, spec, userFrom(r.Context()).DisplayName()); err != nil {
		if errors.Is(err, storage.ErrNotConnected) {
			a.storageError(w, err)
			return
		}
		field := "name"
		if strings.Contains(err.Error(), "prefix") {
			field = "prefix"
		}
		if !strings.Contains(err.Error(), "Cloudflare") && !strings.Contains(err.Error(), "couldn't") {
			fieldError(w, field, sentenceOf(err))
			return
		}
		a.storageError(w, err)
		return
	}
	a.getStorage(w, r)
}

func (a *API) changeBucket(w http.ResponseWriter, r *http.Request) {
	project, ok := a.storageProject(w, r)
	if !ok {
		return
	}
	var req struct {
		Environments []string `json:"environments"`
		Prefix       string   `json:"prefix"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req) != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "that isn't JSON"})
		return
	}
	if _, err := a.Storage.Link(r.Context(), project, r.PathValue("bucket"), req.Environments, req.Prefix, userFrom(r.Context()).DisplayName()); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			a.storageError(w, err)
			return
		}
		fieldError(w, "prefix", sentenceOf(err))
		return
	}
	a.getStorage(w, r)
}

func (a *API) deleteBucket(w http.ResponseWriter, r *http.Request) {
	project, ok := a.storageProject(w, r)
	if !ok {
		return
	}
	ctx, cancel := work(r)
	defer cancel()
	if err := a.Storage.Remove(ctx, project, r.PathValue("bucket"), userFrom(r.Context()).DisplayName()); err != nil {
		a.storageError(w, err)
		return
	}
	a.getStorage(w, r)
}

// bucketVariables shows a bucket's variables with their values, its key's
// secret among them, to members and the owner.
func (a *API) bucketVariables(w http.ResponseWriter, r *http.Request) {
	b, _, ok := a.storageBucket(w, r)
	if !ok {
		return
	}
	vars, err := a.Storage.Variables(r.Context(), b)
	if err != nil {
		a.storageError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"variables": vars})
}

// fileView is one file, with links that read it straight from R2 for a
// while, and its public address when the bucket is public.
type fileView struct {
	storage.Object
	Type     string `json:"type"`
	View     string `json:"view"`     // opens it in the browser
	Download string `json:"download"` // saves it
	URL      string `json:"url,omitempty"`
}

// linkFor is how long the dashboard's links to a file work.
const linkFor = 15 * time.Minute

func (a *API) fileLinks(b store.Bucket, s3 *storage.S3, o storage.Object, typ string) fileView {
	name := path.Base(o.Key)
	disp := func(kind string) url.Values {
		return url.Values{"response-content-disposition": {mime.FormatMediaType(kind, map[string]string{"filename": name})}}
	}
	return fileView{Object: o, Type: typ,
		View:     s3.Presign(b.Bucket, o.Key, linkFor, disp("inline")),
		Download: s3.Presign(b.Bucket, o.Key, linkFor, disp("attachment")),
		URL:      storage.FileURL(b, o.Key)}
}

// listFiles is a page of a folder: the folders in it and its files, each
// with links, so the viewer can show images without asking again.
func (a *API) listFiles(w http.ResponseWriter, r *http.Request) {
	b, s3, ok := a.storageBucket(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	page, err := s3.List(r.Context(), b.Bucket, q.Get("prefix"), "/", q.Get("after"), limit)
	if err != nil {
		a.storageError(w, err)
		return
	}
	files := make([]fileView, 0, len(page.Objects))
	for _, o := range page.Objects {
		files = append(files, a.fileLinks(b, s3, o, mime.TypeByExtension(path.Ext(o.Key))))
	}
	writeJSON(w, http.StatusOK, map[string]any{"folders": page.Folders, "files": files, "next": page.Next})
}

// fileKey is the file a request names, which must be a real key.
func fileKey(w http.ResponseWriter, r *http.Request) (string, bool) {
	key := r.URL.Query().Get("key")
	if key == "" || len(key) > 1024 || strings.HasPrefix(key, "/") {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Name a file."})
		return "", false
	}
	return key, true
}

func (a *API) getFile(w http.ResponseWriter, r *http.Request) {
	b, s3, ok := a.storageBucket(w, r)
	if !ok {
		return
	}
	key, ok := fileKey(w, r)
	if !ok {
		return
	}
	o, typ, err := s3.Head(r.Context(), b.Bucket, key)
	if err != nil {
		a.storageError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, a.fileLinks(b, s3, o, typ))
}

// MaxFile is the largest file the dashboard uploads in one piece.
const MaxFile = 5 << 30

// putFile uploads a file from the dashboard: the body, as it comes.
func (a *API) putFile(w http.ResponseWriter, r *http.Request) {
	b, s3, ok := a.storageBucket(w, r)
	if !ok {
		return
	}
	key, ok := fileKey(w, r)
	if !ok {
		return
	}
	if r.ContentLength < 0 {
		writeJSON(w, http.StatusLengthRequired, map[string]string{"error": "Send the file's size."})
		return
	}
	if r.ContentLength > MaxFile {
		writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "Files over 5 GB are uploaded with an S3 client."})
		return
	}
	typ := r.Header.Get("Content-Type")
	if typ == "" {
		typ = mime.TypeByExtension(path.Ext(key))
	}
	ctx, cancel := work(r)
	defer cancel()
	if err := s3.Put(ctx, b.Bucket, key, typ, http.MaxBytesReader(w, r.Body, r.ContentLength), r.ContentLength); err != nil {
		a.storageError(w, err)
		return
	}
	a.Storage.Changed(b)
	o, typ, err := s3.Head(r.Context(), b.Bucket, key)
	if err != nil {
		a.storageError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, a.fileLinks(b, s3, o, typ))
}

// deleteFiles deletes one file (?key=) or a folder and everything in it
// (?prefix=, ending in /).
func (a *API) deleteFiles(w http.ResponseWriter, r *http.Request) {
	b, s3, ok := a.storageBucket(w, r)
	if !ok {
		return
	}
	defer a.Storage.Changed(b)
	q := r.URL.Query()
	if prefix := q.Get("prefix"); prefix != "" {
		if !strings.HasSuffix(prefix, "/") {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "A folder ends with /."})
			return
		}
		ctx, cancel := work(r)
		defer cancel()
		deleted := 0
		for {
			page, err := s3.List(ctx, b.Bucket, prefix, "", "", 1000)
			if err != nil {
				a.storageError(w, err)
				return
			}
			if len(page.Objects) == 0 {
				break
			}
			keys := make([]string, len(page.Objects))
			for i, o := range page.Objects {
				keys[i] = o.Key
			}
			if err := s3.DeleteMany(ctx, b.Bucket, keys); err != nil {
				a.storageError(w, err)
				return
			}
			deleted += len(keys)
		}
		writeJSON(w, http.StatusOK, map[string]int{"deleted": deleted})
		return
	}
	key, ok := fileKey(w, r)
	if !ok {
		return
	}
	if err := s3.Delete(r.Context(), b.Bucket, key); err != nil {
		a.storageError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"deleted": 1})
}
