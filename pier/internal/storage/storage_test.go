package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"pier/internal/secrets"
	"pier/internal/store"
)

// fakeCloudflare answers the Cloudflare calls Pier makes, and makes and
// removes buckets on a real S3 server, so the manager's whole flow runs.
type fakeCloudflare struct {
	t       *testing.T
	s3      *S3
	keys    [][2]string // id and value of each key it hands out, in order
	mu      sync.Mutex
	made    int
	tokens  map[string][]string // key id -> its buckets
	domains map[string]string   // bucket -> custom domain
	dev     map[string]bool
	buckets map[string]bool // the account's buckets
}

func (f *fakeCloudflare) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	ok := func(result any) {
		json.NewEncoder(w).Encode(map[string]any{"success": true, "errors": []any{}, "result": result})
	}
	if r.Header.Get("Authorization") != "Bearer admin-token" {
		w.WriteHeader(403)
		json.NewEncoder(w).Encode(map[string]any{"success": false, "errors": []any{map[string]any{"code": 9109, "message": "Invalid access token"}}})
		return
	}
	p := r.URL.Path
	switch {
	case p == "/accounts/0123456789abcdef0123456789abcdef/tokens/verify":
		ok(map[string]string{"id": "adminkey", "status": "active"})
	case p == "/accounts/0123456789abcdef0123456789abcdef/tokens/permission_groups":
		ok([]map[string]string{{"id": "g1", "name": ObjectWrite}})
	case p == "/zones":
		ok([]map[string]string{{"id": "zone1", "name": r.URL.Query().Get("name"), "status": "active"}})
	case p == "/accounts/0123456789abcdef0123456789abcdef/r2/buckets" && r.Method == http.MethodPost:
		var b struct{ Name string }
		json.NewDecoder(r.Body).Decode(&b)
		if f.buckets[b.Name] {
			w.WriteHeader(409)
			json.NewEncoder(w).Encode(map[string]any{"success": false, "errors": []any{map[string]any{"code": 10004, "message": "The bucket you tried to create already exists, and you own it."}}})
			return
		}
		f.buckets[b.Name] = true
		resp, err := f.s3.request(r.Context(), http.MethodPut, b.Name, "", nil, nil, nil, 0, "")
		if err != nil {
			f.t.Errorf("make %s: %v", b.Name, err)
		} else {
			resp.Body.Close()
		}
		ok(map[string]string{"name": b.Name})
	case strings.HasPrefix(p, "/accounts/0123456789abcdef0123456789abcdef/r2/buckets/") && strings.HasSuffix(p, "/domains/custom") && r.Method == http.MethodPost:
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		if body["zoneId"] != "zone1" {
			f.t.Errorf("domain attached in zone %v", body["zoneId"])
		}
		domain, _ := body["domain"].(string)
		f.domains[strings.Split(strings.TrimPrefix(p, "/accounts/0123456789abcdef0123456789abcdef/r2/buckets/"), "/")[0]] = domain
		ok(body)
	case strings.Contains(p, "/domains/custom/") && r.Method == http.MethodDelete:
		delete(f.domains, strings.Split(strings.TrimPrefix(p, "/accounts/0123456789abcdef0123456789abcdef/r2/buckets/"), "/")[0])
		ok(map[string]string{})
	case strings.HasSuffix(p, "/domains/managed"):
		bucket := strings.Split(strings.TrimPrefix(p, "/accounts/0123456789abcdef0123456789abcdef/r2/buckets/"), "/")[0]
		f.dev[bucket] = true
		ok(map[string]any{"domain": "pub-" + bucket + ".r2.dev", "enabled": true})
	case strings.HasPrefix(p, "/accounts/0123456789abcdef0123456789abcdef/r2/buckets/") && r.Method == http.MethodDelete:
		name := strings.TrimPrefix(p, "/accounts/0123456789abcdef0123456789abcdef/r2/buckets/")
		page, _ := f.s3.List(r.Context(), name, "", "", "", 1)
		if len(page.Objects) > 0 {
			w.WriteHeader(409)
			json.NewEncoder(w).Encode(map[string]any{"success": false, "errors": []any{map[string]any{"code": 10008, "message": "The bucket you tried to delete is not empty"}}})
			return
		}
		resp, err := f.s3.request(r.Context(), http.MethodDelete, name, "", nil, nil, nil, 0, "")
		if err == nil {
			resp.Body.Close()
		}
		delete(f.buckets, name)
		ok(map[string]string{})
	case p == "/accounts/0123456789abcdef0123456789abcdef/tokens" && r.Method == http.MethodPost:
		var body struct {
			Policies []struct {
				Resources map[string]string `json:"resources"`
			} `json:"policies"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		k := f.keys[f.made]
		f.made++
		var buckets []string
		for res := range body.Policies[0].Resources {
			buckets = append(buckets, strings.TrimPrefix(res, "com.cloudflare.edge.r2.bucket.0123456789abcdef0123456789abcdef_default_"))
		}
		f.tokens[k[0]] = buckets
		ok(map[string]string{"id": k[0], "value": k[1]})
	case strings.HasPrefix(p, "/accounts/0123456789abcdef0123456789abcdef/tokens/") && r.Method == http.MethodDelete:
		delete(f.tokens, strings.TrimPrefix(p, "/accounts/0123456789abcdef0123456789abcdef/tokens/"))
		ok(map[string]string{})
	default:
		f.t.Errorf("unexpected %s %s", r.Method, p)
		w.WriteHeader(404)
	}
}

func TestStorageLifecycle(t *testing.T) {
	if os.Getenv("PIER_TEST_DOCKER") == "" {
		t.Skip("set PIER_TEST_DOCKER=1 to run an S3 server in Docker")
	}
	keys := [][2]string{{"key1", "value-one"}, {"key2", "value-two"}, {"key3", "value-three"}, {"key4", "value-four"}, {"key5", "value-five"}, {"key6", "value-six"}}
	auth := []string{"--auth-key", "adminkey," + sha256Hex("admin-token")}
	for _, k := range keys {
		auth = append(auth, "--auth-key", k[0]+","+sha256Hex(k[1]))
	}
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	name := fmt.Sprintf("pier-storage-test-%d", port)
	args := append([]string{"run", "-d", "--rm", "--name", name, "-p", fmt.Sprintf("127.0.0.1:%d:9000", port), "rclone/rclone:1.69.1", "serve", "s3", "--addr", ":9000"}, auth...)
	if out, err := exec.Command("docker", append(args, "/data")...).CombinedOutput(); err != nil {
		t.Fatalf("docker run: %v %s", err, out)
	}
	t.Cleanup(func() { exec.Command("docker", "rm", "-f", name).Run() })
	endpoint := fmt.Sprintf("http://127.0.0.1:%d", port)
	admin := &S3{Endpoint: endpoint, KeyID: "adminkey", Secret: sha256Hex("admin-token")}
	ctx := context.Background()
	for i := 0; ; i++ {
		if _, err := admin.List(ctx, "nope", "", "", "", 1); err != nil && NotFound(err) {
			break
		}
		if i > 60 {
			t.Fatal("the S3 server didn't start")
		}
		time.Sleep(500 * time.Millisecond)
	}
	fake := &fakeCloudflare{t: t, s3: admin, keys: keys, tokens: map[string][]string{}, domains: map[string]string{}, dev: map[string]bool{}, buckets: map[string]bool{}}
	api := httptest.NewServer(fake)
	defer api.Close()

	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "pier.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	box, _ := secrets.Load(filepath.Join(dir, "key"))
	m := &Manager{Store: st, Box: box, Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Domain: "thesunset.live", Base: api.URL, Endpoint: endpoint}
	if err := st.CreateProject(ctx, store.Project{Name: "shop"}); err != nil {
		t.Fatal(err)
	}

	both := []string{store.TargetProduction, store.TargetPreview}
	if _, err := m.Add(ctx, "shop", Spec{Name: "files", Environments: both}, "Ada"); err != ErrNotConnected {
		t.Fatalf("added a bucket before connecting: %v", err)
	}
	if _, err := m.Connect(ctx, "0123456789abcdef0123456789abcdef", "wrong"); err == nil {
		t.Fatal("a wrong token connected")
	}
	a, err := m.Connect(ctx, "0123456789abcdef0123456789abcdef", "admin-token")
	if err != nil || a.Zone != "zone1" || !strings.HasPrefix(a.Prefix, "pier-") {
		t.Fatalf("connect %+v %v", a, err)
	}

	// The first bucket serves production and previews, as STORAGE_.
	files, err := m.Add(ctx, "shop", Spec{Name: "files", Environments: both}, "Ada")
	if err != nil || files.Prefix != "STORAGE" || files.PublicURL != "" || files.Bucket != a.Prefix+"-shop-files" {
		t.Fatalf("add %+v %v", files, err)
	}
	if _, err := m.Add(ctx, "shop", Spec{Name: "files"}, "Ada"); err == nil {
		t.Fatal("added a second bucket with the same name")
	}
	if _, err := m.Add(ctx, "shop", Spec{Name: "Bad Name!"}, "Ada"); err == nil {
		t.Fatal("added a bucket with a bad name")
	}
	if _, err := m.Add(ctx, "shop", Spec{Name: "other", Environments: both, Prefix: "STORAGE"}, "Ada"); err == nil {
		t.Fatal("two buckets gave production the same variables")
	}
	// A public bucket, for production only, served from the base domain.
	avatars, err := m.Add(ctx, "shop", Spec{Name: "avatars", Public: true, Environments: []string{store.TargetProduction}}, "Ada")
	if err != nil || avatars.Prefix != "AVATARS" || avatars.PublicURL != "https://files-shop-avatars.thesunset.live" || fake.domains[avatars.Bucket] != "files-shop-avatars.thesunset.live" {
		t.Fatalf("add public %+v %v %v", avatars, err, fake.domains)
	}

	vars := func(target string) map[string]string {
		env, err := m.For(ctx, "shop", target)
		if err != nil {
			t.Fatal(err)
		}
		out := map[string]string{}
		for _, kv := range env {
			k, v, _ := strings.Cut(kv, "=")
			out[k] = v
		}
		return out
	}
	prod := vars(store.TargetProduction)
	if prod["STORAGE_BUCKET"] != files.Bucket || prod["AWS_ACCESS_KEY_ID"] != "key1" || prod["AWS_REGION"] != "auto" ||
		prod["AVATARS_BUCKET"] != avatars.Bucket || prod["AVATARS_PUBLIC_URL"] != avatars.PublicURL || prod["AVATARS_ACCESS_KEY_ID"] != "key2" {
		t.Fatalf("production's variables %v", prod)
	}
	if prev := vars(store.TargetPreview); prev["STORAGE_BUCKET"] != files.Bucket || prev["AVATARS_BUCKET"] != "" {
		t.Fatalf("previews' variables %v", prev)
	}

	// Each key writes to its own bucket only.
	app := &S3{Endpoint: prod["STORAGE_ENDPOINT"], KeyID: prod["STORAGE_ACCESS_KEY_ID"], Secret: prod["STORAGE_SECRET_ACCESS_KEY"]}
	if err := app.Put(ctx, files.Bucket, "invoices/1.pdf", "application/pdf", strings.NewReader("pdf!"), 4); err != nil {
		t.Fatal("the app's key couldn't write:", err)
	}
	pub := &S3{Endpoint: endpoint, KeyID: prod["AVATARS_ACCESS_KEY_ID"], Secret: prod["AVATARS_SECRET_ACCESS_KEY"]}
	if err := pub.Put(ctx, avatars.Bucket, "ada okafor.png", "image/png", strings.NewReader("png"), 3); err != nil {
		t.Fatal(err)
	}
	if fake.tokens["key1"][0] != files.Bucket || len(fake.tokens["key1"]) != 1 {
		t.Fatalf("the first key reaches %v", fake.tokens["key1"])
	}

	// The viewer reads them.
	b, s3, err := m.Objects(ctx, "shop", "files")
	if err != nil {
		t.Fatal(err)
	}
	list, err := s3.List(ctx, b.Bucket, "", "/", "", 100)
	if err != nil || len(list.Folders) != 1 || list.Folders[0] != "invoices/" {
		t.Fatalf("viewer %+v %v", list, err)
	}
	if _, _, err := m.Objects(ctx, "shop", "someone-elses"); err == nil {
		t.Fatal("the viewer opened a bucket that isn't the project's")
	}
	if u := FileURL(avatars, "ada okafor.png"); u != "https://files-shop-avatars.thesunset.live/ada%20okafor.png" {
		t.Fatalf("file url %q", u)
	}
	if n, size, _, err := m.Count(ctx, files); err != nil || n != 1 || size != 4 {
		t.Fatalf("count %d %d %v", n, size, err)
	}

	// Previews move to a bucket of their own.
	test, err := m.Add(ctx, "shop", Spec{Name: "test"}, "Ada")
	if err != nil || len(test.Environments) != 0 || test.Prefix != "TEST" {
		t.Fatalf("add test %+v %v", test, err)
	}
	if _, err := m.Link(ctx, "shop", "test", []string{store.TargetPreview}, "STORAGE", "Ada"); err == nil {
		t.Fatal("linked two STORAGE_ buckets to previews")
	}
	if _, err := m.Link(ctx, "shop", "files", []string{store.TargetProduction}, "", "Ada"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Link(ctx, "shop", "test", []string{store.TargetPreview}, "STORAGE", "Ada"); err != nil {
		t.Fatal(err)
	}
	if prev := vars(store.TargetPreview); prev["STORAGE_BUCKET"] != test.Bucket || prev["AWS_ACCESS_KEY_ID"] != "key3" {
		t.Fatalf("previews' variables after the move %v", prev)
	}
	if err := m.Disconnect(ctx); err == nil {
		t.Fatal("disconnected while projects have buckets")
	}

	// shop's old-files and shop-old's files would get the same name and
	// address; the second gets its own.
	if err := st.CreateProject(ctx, store.Project{Name: "shop-old"}); err != nil {
		t.Fatal(err)
	}
	oldFiles, err := m.Add(ctx, "shop", Spec{Name: "old-files", Public: true}, "Ada")
	if err != nil || oldFiles.Bucket != a.Prefix+"-shop-old-files" {
		t.Fatalf("add old-files %+v %v", oldFiles, err)
	}
	clash, err := m.Add(ctx, "shop-old", Spec{Name: "files", Public: true}, "Ada")
	if err != nil || clash.Bucket == oldFiles.Bucket || clash.PublicURL == oldFiles.PublicURL ||
		!strings.HasPrefix(clash.Bucket, a.Prefix+"-shop-old-files-") || !strings.HasPrefix(clash.PublicURL, "https://files-shop-old-files-") {
		t.Fatalf("clashing names %+v %+v %v", oldFiles, clash, err)
	}
	// A bucket left on the account under the name it would get.
	fake.mu.Lock()
	fake.buckets[a.Prefix+"-shop-leftover"] = true
	fake.mu.Unlock()
	leftover, err := m.Add(ctx, "shop", Spec{Name: "leftover"}, "Ada")
	if err != nil || !strings.HasPrefix(leftover.Bucket, a.Prefix+"-shop-leftover-") {
		t.Fatalf("add over a leftover %+v %v", leftover, err)
	}
	for _, b := range []store.Bucket{oldFiles, clash, leftover} {
		if err := m.Remove(ctx, b.Project, b.Name, "Ada"); err != nil {
			t.Fatal(err)
		}
	}

	// Deleting a bucket empties it and removes its key and address.
	if err := m.Remove(ctx, "shop", "avatars", "Ada"); err != nil {
		t.Fatal(err)
	}
	if _, err := admin.List(ctx, avatars.Bucket, "", "", "", 1); !NotFound(err) {
		t.Fatalf("the bucket is still there: %v", err)
	}
	if _, ok := fake.tokens["key2"]; ok || len(fake.domains) != 0 {
		t.Fatalf("left behind: keys %v domains %v", fake.tokens, fake.domains)
	}
	// Deleting the project removes the rest.
	rest, _ := st.Buckets(ctx, "shop")
	if err := st.DeleteProject(ctx, "shop"); err != nil {
		t.Fatal(err)
	}
	if err := m.Purge(ctx, rest); err != nil {
		t.Fatal(err)
	}
	if left, _ := st.Buckets(ctx, "shop"); len(left) != 0 || len(fake.tokens) != 0 {
		t.Fatalf("left behind after purge: %v %v", left, fake.tokens)
	}
	if err := m.Disconnect(ctx); err != nil {
		t.Fatal(err)
	}
}
