package storage

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"pier/internal/secrets"
	"pier/internal/store"
)

// TestRealCloudflare runs the whole flow against a real Cloudflare account:
// connect, a private and a public bucket, their keys, files through each
// key, the public address on the base domain, and removing all of it. Set
// PIER_TEST_CLOUDFLARE_ACCOUNT, PIER_TEST_CLOUDFLARE_TOKEN_FILE (a file
// holding an account token) and PIER_TEST_CLOUDFLARE_DOMAIN (a zone in that
// account, or "none" to test a server whose domain isn't on Cloudflare,
// whose public buckets use r2.dev). It leaves nothing behind.
func TestRealCloudflare(t *testing.T) {
	account, file, domain := os.Getenv("PIER_TEST_CLOUDFLARE_ACCOUNT"), os.Getenv("PIER_TEST_CLOUDFLARE_TOKEN_FILE"), os.Getenv("PIER_TEST_CLOUDFLARE_DOMAIN")
	if account == "" || file == "" || domain == "" {
		t.Skip("set PIER_TEST_CLOUDFLARE_ACCOUNT, PIER_TEST_CLOUDFLARE_TOKEN_FILE and PIER_TEST_CLOUDFLARE_DOMAIN to run against Cloudflare")
	}
	raw, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	token := strings.TrimSpace(string(raw))
	ctx := context.Background()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "pier.db"))
	if err != nil {
		t.Fatal(err)
	}
	// Cleanups run last first, so the store closes after the buckets go.
	t.Cleanup(func() { st.Close() })
	box, _ := secrets.Load(filepath.Join(dir, "key"))
	devURL := domain == "none"
	m := &Manager{Store: st, Box: box, Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Domain: domain}
	if devURL {
		m.Domain, domain = "", "localhost"
	}
	project := "r2check"
	if err := st.CreateProject(ctx, store.Project{Name: project}); err != nil {
		t.Fatal(err)
	}

	a, err := m.Connect(ctx, account, token)
	if err != nil {
		t.Fatal("connect:", err)
	}
	cf := m.api(a)
	sweep := func() {
		ctx := context.Background()
		names, err := cf.BucketNames(ctx, "pier-")
		if err != nil {
			t.Log("list buckets:", err)
		}
		for _, name := range names {
			if !strings.Contains(name, "-"+project+"-") {
				continue
			}
			m.admin(a).Empty(ctx, name)
			cf.DetachDomain(ctx, name, "files-"+project+"-"+name[strings.LastIndex(name, "-")+1:]+"."+domain)
			if err := cf.DeleteBucket(ctx, name); err != nil {
				t.Logf("delete %s: %v", name, err)
			}
		}
		keys, err := cf.Keys(ctx, "pier "+project+" ")
		if err != nil {
			t.Log("list keys:", err)
		}
		for id := range keys {
			cf.DeleteKey(ctx, id)
		}
	}
	// Anything an earlier run left behind goes first.
	sweep()
	t.Cleanup(sweep)
	if a.Zone == "" && !devURL {
		t.Fatalf("%s isn't a zone in the account", domain)
	}
	t.Logf("connected; buckets start with %s", a.Prefix)

	files, err := m.Add(ctx, project, Spec{Name: "files", Environments: []string{store.TargetProduction, store.TargetPreview}}, "test")
	if err != nil {
		t.Fatal("add a private bucket:", err)
	}
	t.Cleanup(func() { m.Purge(context.Background(), []store.Bucket{files}) })
	media, err := m.Add(ctx, project, Spec{Name: "media", Public: true, Environments: []string{store.TargetProduction}}, "test")
	if err != nil {
		t.Fatal("add a public bucket:", err)
	}
	t.Cleanup(func() { m.Purge(context.Background(), []store.Bucket{media}) })
	t.Logf("buckets %s and %s, public at %s", files.Bucket, media.Bucket, media.PublicURL)
	if want := "https://files-" + project + "-media." + domain; !devURL && media.PublicURL != want || devURL && !strings.HasSuffix(media.PublicURL, ".r2.dev") {
		t.Fatalf("public url %s", media.PublicURL)
	}

	env, err := m.For(ctx, project, store.TargetProduction)
	if err != nil {
		t.Fatal(err)
	}
	vars := map[string]string{}
	for _, kv := range env {
		k, v, _ := strings.Cut(kv, "=")
		vars[k] = v
	}
	app := &S3{Endpoint: vars["STORAGE_ENDPOINT"], Region: "auto", KeyID: vars["STORAGE_ACCESS_KEY_ID"], Secret: vars["STORAGE_SECRET_ACCESS_KEY"]}
	pub := &S3{Endpoint: vars["MEDIA_ENDPOINT"], Region: "auto", KeyID: vars["MEDIA_ACCESS_KEY_ID"], Secret: vars["MEDIA_SECRET_ACCESS_KEY"]}

	// A new key can take a few seconds to work everywhere.
	retry := func(what string, f func() error) {
		t.Helper()
		var err error
		for i := 0; i < 30; i++ {
			if err = f(); err == nil {
				return
			}
			time.Sleep(2 * time.Second)
		}
		t.Fatalf("%s: %v", what, err)
	}
	retry("the app's key writes its bucket", func() error {
		return app.Put(ctx, files.Bucket, "notes/hello world.txt", "text/plain", strings.NewReader("hello from pier"), 15)
	})
	if err := app.Put(ctx, media.Bucket, "x.txt", "text/plain", strings.NewReader("x"), 1); err == nil {
		t.Fatal("a bucket's key wrote to another bucket")
	}
	retry("the public bucket's key writes it", func() error {
		return pub.Put(ctx, media.Bucket, "avatars/ada.txt", "text/plain", strings.NewReader("public hello"), 12)
	})

	// The dashboard's viewer, with the owner's token.
	_, s3, err := m.Objects(ctx, project, "files")
	if err != nil {
		t.Fatal(err)
	}
	list, err := s3.List(ctx, files.Bucket, "", "/", "", 100)
	if err != nil || len(list.Folders) != 1 || list.Folders[0] != "notes/" {
		t.Fatalf("listing %+v %v", list, err)
	}
	link := s3.Presign(files.Bucket, "notes/hello world.txt", time.Minute, nil)
	resp, err := http.Get(link)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || string(body) != "hello from pier" {
		t.Fatalf("a presigned link answered %d %q", resp.StatusCode, body)
	}
	if n, size, _, err := m.Count(ctx, files); err != nil || n != 1 || size != 15 {
		t.Fatalf("count %d %d %v", n, size, err)
	}

	// The public address, once Cloudflare has its record and certificate.
	// It's looked up at the zone's own nameservers, as a cached answer from
	// before the record existed would point at the base domain's wildcard.
	deadline := time.Now().Add(4 * time.Minute)
	for !m.AddressReady(ctx, media) {
		if time.Now().After(deadline) {
			t.Fatal("the public address never became ready")
		}
		time.Sleep(5 * time.Second)
	}
	client := &http.Client{Timeout: 20 * time.Second}
	if !devURL {
		ns, err := net.LookupNS(domain)
		if err != nil || len(ns) == 0 {
			t.Fatal("the zone's nameservers:", err)
		}
		direct := &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, net.JoinHostPort(strings.TrimSuffix(ns[0].Host, "."), "53"))
		}}
		client.Transport = &http.Transport{DialContext: (&net.Dialer{Resolver: direct}).DialContext}
	}
	url := FileURL(media, "avatars/ada.txt")
	for {
		resp, err := client.Get(url)
		if err == nil {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if resp.StatusCode == 200 && string(body) == "public hello" {
				t.Logf("served at %s, via %s", url, resp.Header.Get("Server"))
				break
			}
			err = &S3Error{Status: resp.StatusCode, Message: strings.TrimSpace(string(body))}
		}
		if time.Now().After(deadline) {
			t.Fatalf("the public address never served the file: %v", err)
		}
		time.Sleep(10 * time.Second)
	}

	// Removing empties and deletes both, and their keys and address.
	if err := m.Remove(ctx, project, "media", "test"); err != nil {
		t.Fatal("remove the public bucket:", err)
	}
	if err := m.Remove(ctx, project, "files", "test"); err != nil {
		t.Fatal("remove the private bucket:", err)
	}
	if _, err := m.admin(a).List(ctx, files.Bucket, "", "", "", 1); !NotFound(err) {
		t.Fatalf("the bucket is still there: %v", err)
	}
	if err := m.Disconnect(ctx); err != nil {
		t.Fatal(err)
	}
}
