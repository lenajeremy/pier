package storage

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// TestS3AgainstARealServer runs the client against a real S3 server, which checks
// every signature: PIER_TEST_DOCKER=1.
func TestS3AgainstARealServer(t *testing.T) {
	if os.Getenv("PIER_TEST_DOCKER") == "" {
		t.Skip("set PIER_TEST_DOCKER=1 to run an S3 server in Docker")
	}
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	name := fmt.Sprintf("pier-s3-test-%d", port)
	// rclone serve s3 checks every request's signature against its key.
	out, err := exec.Command("docker", "run", "-d", "--rm", "--name", name, "-p", fmt.Sprintf("127.0.0.1:%d:9000", port),
		"rclone/rclone:1.69.1", "serve", "s3", "--addr", ":9000", "--auth-key", "pierkey,piersecretpiersecret", "/data").CombinedOutput()
	if err != nil {
		t.Fatalf("docker run: %v %s", err, out)
	}
	t.Cleanup(func() { exec.Command("docker", "rm", "-f", name).Run() })
	c := &S3{Endpoint: fmt.Sprintf("http://127.0.0.1:%d", port), Region: "us-east-1", KeyID: "pierkey", Secret: "piersecretpiersecret"}
	ctx := context.Background()
	for i := 0; ; i++ {
		resp, err := c.request(ctx, http.MethodPut, "pier-test", "", nil, nil, nil, 0, "")
		if err == nil {
			resp.Body.Close()
			break
		}
		if i > 60 {
			t.Fatal("the S3 server didn't start:", err)
		}
		time.Sleep(500 * time.Millisecond)
	}

	keys := []string{"public/avatars/ada okafor.jpg", "public/avatars/ünïcode+plus.png", "notes/2026/today.txt", "root.txt"}
	for i, k := range keys {
		body := strings.Repeat("x", 100*(i+1))
		if err := c.Put(ctx, "pier-test", k, "text/plain", strings.NewReader(body), int64(len(body))); err != nil {
			t.Fatalf("put %q: %v", k, err)
		}
	}
	top, err := c.List(ctx, "pier-test", "", "/", "", 100)
	if err != nil || len(top.Folders) != 2 || top.Folders[0] != "notes/" || len(top.Objects) != 1 || top.Objects[0].Key != "root.txt" {
		t.Fatalf("top level %+v %v", top, err)
	}
	av, err := c.List(ctx, "pier-test", "public/avatars/", "/", "", 100)
	if err != nil || len(av.Objects) != 2 {
		t.Fatalf("avatars %+v %v", av, err)
	}
	o, typ, err := c.Head(ctx, "pier-test", "public/avatars/ünïcode+plus.png")
	if err != nil || o.Size != 200 || typ != "text/plain" {
		t.Fatalf("head %+v %q %v", o, typ, err)
	}
	resp, err := c.Get(ctx, "pier-test", "public/avatars/ada okafor.jpg")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if len(b) != 100 {
		t.Fatalf("get read %d bytes", len(b))
	}
	link := c.Presign("pier-test", "public/avatars/ada okafor.jpg", time.Minute, url.Values{"response-content-disposition": {`attachment; filename="ada okafor.jpg"`}})
	if got, err := http.Get(link); err != nil || got.StatusCode != 200 {
		t.Fatalf("a presigned link: %v %v", got.Status, err)
	} else {
		n, _ := io.Copy(io.Discard, got.Body)
		got.Body.Close()
		if n != 100 {
			t.Fatalf("a presigned link read %d bytes", n)
		}
	}
	if got, _ := http.Get(strings.Replace(link, "ada", "bob", 1)); got.StatusCode != 403 {
		t.Fatalf("a changed link answered %s", got.Status)
	}
	if _, _, err := c.Head(ctx, "pier-test", "nope"); !NotFound(err) {
		t.Fatalf("a missing object: %v", err)
	}
	page, err := c.List(ctx, "pier-test", "", "", "", 2)
	if err != nil || len(page.Objects) != 2 || page.Next == "" {
		t.Fatalf("paging %+v %v", page, err)
	}
	files, size, more, err := c.Count(ctx, "pier-test", 3)
	if err != nil || files != 4 || size != 1000 || more {
		t.Fatalf("count %d %d %v %v", files, size, more, err)
	}
	if err := c.Delete(ctx, "pier-test", "root.txt"); err != nil {
		t.Fatal(err)
	}
	if err := c.Empty(ctx, "pier-test"); err != nil {
		t.Fatal(err)
	}
	if left, _, _, _ := c.Count(ctx, "pier-test", 10); left != 0 {
		t.Fatalf("%d left after emptying", left)
	}
	bad := *c
	bad.Secret = "wrong"
	if _, err := bad.List(ctx, "pier-test", "", "", "", 1); err == nil {
		t.Fatal("a wrong secret listed the bucket")
	}
}
