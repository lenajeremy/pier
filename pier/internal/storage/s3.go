package storage

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/md5"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

// S3 is a small client for R2's S3 API: list, read, write and delete, signed
// with AWS Signature Version 4. Only what Pier's viewer and clean-up need.
type S3 struct {
	Endpoint string // https://<account>.r2.cloudflarestorage.com
	Region   string // auto
	KeyID    string
	Secret   string
	Client   *http.Client
	now      func() time.Time
}

// Object is one file in a bucket.
type Object struct {
	Key      string    `json:"key"`
	Size     int64     `json:"size"`
	Modified time.Time `json:"modified"`
	ETag     string    `json:"etag,omitempty"`
}

// Listing is a page of a bucket's objects under a prefix, with the
// "folders" below it when listed with a delimiter.
type Listing struct {
	Folders []string `json:"folders"`
	Objects []Object `json:"objects"`
	Next    string   `json:"next,omitempty"` // continuation token; empty on the last page
}

// S3Error is an error answer from the S3 API.
type S3Error struct {
	Status  int
	Code    string `xml:"Code"`
	Message string `xml:"Message"`
}

func (e *S3Error) Error() string {
	if e.Message != "" {
		return fmt.Sprintf("%s: %s", e.Code, e.Message)
	}
	if e.Code != "" {
		return e.Code
	}
	return fmt.Sprintf("storage answered %d", e.Status)
}

// NotFound reports whether err says the object or bucket isn't there.
func NotFound(err error) bool {
	var e *S3Error
	return errors.As(err, &e) && (e.Status == http.StatusNotFound || e.Code == "NoSuchKey" || e.Code == "NoSuchBucket")
}

const unsigned = "UNSIGNED-PAYLOAD"

// request signs and sends one request. body may be nil; payload is its
// SHA-256 in hex, or UNSIGNED-PAYLOAD for a stream.
func (c *S3) request(ctx context.Context, method, bucket, key string, query url.Values, header http.Header, body io.Reader, size int64, payload string) (*http.Response, error) {
	u, err := url.Parse(c.Endpoint)
	if err != nil {
		return nil, err
	}
	path := "/" + bucket
	if key != "" {
		path += "/" + key
	}
	u.Path = path
	u.RawPath = encodePath(path)
	u.RawQuery = canonicalQuery(query)
	req, err := http.NewRequestWithContext(ctx, method, u.String(), body)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.ContentLength = size
	}
	for k, v := range header {
		req.Header[k] = v
	}
	if payload == "" {
		payload = hashHex(nil)
	}
	c.sign(req, payload)
	client := c.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 300 {
		defer resp.Body.Close()
		e := &S3Error{Status: resp.StatusCode}
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		xml.Unmarshal(raw, e)
		return nil, e
	}
	return resp, nil
}

// sign adds SigV4 headers to req for the s3 service.
func (c *S3) sign(req *http.Request, payload string) {
	now := time.Now().UTC()
	if c.now != nil {
		now = c.now().UTC()
	}
	stamp := now.Format("20060102T150405Z")
	day := now.Format("20060102")
	region := c.Region
	if region == "" {
		region = "auto"
	}
	req.Header.Set("X-Amz-Date", stamp)
	req.Header.Set("X-Amz-Content-Sha256", payload)
	req.Header.Set("Host", req.URL.Host)

	names := []string{"host"}
	for k := range req.Header {
		lk := strings.ToLower(k)
		if lk == "host" {
			continue
		}
		if strings.HasPrefix(lk, "x-amz-") || lk == "content-type" || lk == "content-md5" || lk == "range" {
			names = append(names, lk)
		}
	}
	sort.Strings(names)
	var canonHeaders strings.Builder
	for _, n := range names {
		v := req.URL.Host
		if n != "host" {
			v = strings.Join(req.Header.Values(n), ",")
		}
		canonHeaders.WriteString(n + ":" + strings.TrimSpace(v) + "\n")
	}
	signed := strings.Join(names, ";")
	canonical := strings.Join([]string{
		req.Method, req.URL.EscapedPath(), req.URL.RawQuery, canonHeaders.String(), signed, payload,
	}, "\n")
	scope := day + "/" + region + "/s3/aws4_request"
	sig := c.signature(day, region, stamp, scope, canonical)
	req.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential="+c.KeyID+"/"+scope+", SignedHeaders="+signed+", Signature="+sig)
}

func (c *S3) signature(day, region, stamp, scope, canonical string) string {
	toSign := "AWS4-HMAC-SHA256\n" + stamp + "\n" + scope + "\n" + hashHex([]byte(canonical))
	k := hmacSHA256([]byte("AWS4"+c.Secret), day)
	k = hmacSHA256(k, region)
	k = hmacSHA256(k, "s3")
	k = hmacSHA256(k, "aws4_request")
	return hex.EncodeToString(hmacSHA256(k, toSign))
}

// Presign makes a link that reads an object for a while, with no other
// credentials: the dashboard's previews and downloads come straight from
// R2. extra holds response-content-disposition and the like.
func (c *S3) Presign(bucket, key string, expires time.Duration, extra url.Values) string {
	now := time.Now().UTC()
	if c.now != nil {
		now = c.now().UTC()
	}
	stamp, day := now.Format("20060102T150405Z"), now.Format("20060102")
	region := c.Region
	if region == "" {
		region = "auto"
	}
	u, _ := url.Parse(c.Endpoint)
	path := "/" + bucket + "/" + key
	scope := day + "/" + region + "/s3/aws4_request"
	q := url.Values{}
	for k, v := range extra {
		q[k] = v
	}
	q.Set("X-Amz-Algorithm", "AWS4-HMAC-SHA256")
	q.Set("X-Amz-Credential", c.KeyID+"/"+scope)
	q.Set("X-Amz-Date", stamp)
	q.Set("X-Amz-Expires", strconv.Itoa(int(expires.Seconds())))
	q.Set("X-Amz-SignedHeaders", "host")
	query := canonicalQuery(q)
	canonical := strings.Join([]string{http.MethodGet, encodePath(path), query, "host:" + u.Host + "\n", "host", unsigned}, "\n")
	return u.Scheme + "://" + u.Host + encodePath(path) + "?" + query + "&X-Amz-Signature=" + c.signature(day, region, stamp, scope, canonical)
}

func hmacSHA256(key []byte, s string) []byte {
	h := hmac.New(sha256.New, key)
	h.Write([]byte(s))
	return h.Sum(nil)
}

func hashHex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// encodePath escapes a path the way SigV4 wants: every byte but the
// unreserved ones and "/".
func encodePath(p string) string {
	var b strings.Builder
	for i := 0; i < len(p); i++ {
		ch := p[i]
		if ch == '/' || unreserved(ch) {
			b.WriteByte(ch)
		} else {
			fmt.Fprintf(&b, "%%%02X", ch)
		}
	}
	return b.String()
}

func encodeQuery(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		ch := s[i]
		if unreserved(ch) {
			b.WriteByte(ch)
		} else {
			fmt.Fprintf(&b, "%%%02X", ch)
		}
	}
	return b.String()
}

func unreserved(ch byte) bool {
	return 'A' <= ch && ch <= 'Z' || 'a' <= ch && ch <= 'z' || '0' <= ch && ch <= '9' || ch == '-' || ch == '_' || ch == '.' || ch == '~'
}

func canonicalQuery(q url.Values) string {
	keys := make([]string, 0, len(q))
	for k := range q {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var parts []string
	for _, k := range keys {
		vs := append([]string{}, q[k]...)
		sort.Strings(vs)
		for _, v := range vs {
			parts = append(parts, encodeQuery(k)+"="+encodeQuery(v))
		}
	}
	return strings.Join(parts, "&")
}

// List lists a page of objects under prefix. With a delimiter of "/",
// what's below the next "/" comes back as folders instead.
func (c *S3) List(ctx context.Context, bucket, prefix, delimiter, after string, max int) (Listing, error) {
	q := url.Values{"list-type": {"2"}, "max-keys": {strconv.Itoa(max)}}
	if prefix != "" {
		q.Set("prefix", prefix)
	}
	if delimiter != "" {
		q.Set("delimiter", delimiter)
	}
	if after != "" {
		q.Set("continuation-token", after)
	}
	resp, err := c.request(ctx, http.MethodGet, bucket, "", q, nil, nil, 0, "")
	if err != nil {
		return Listing{}, err
	}
	defer resp.Body.Close()
	var r struct {
		Contents []struct {
			Key          string `xml:"Key"`
			Size         int64  `xml:"Size"`
			LastModified string `xml:"LastModified"`
			ETag         string `xml:"ETag"`
		} `xml:"Contents"`
		CommonPrefixes []struct {
			Prefix string `xml:"Prefix"`
		} `xml:"CommonPrefixes"`
		IsTruncated           bool   `xml:"IsTruncated"`
		NextContinuationToken string `xml:"NextContinuationToken"`
	}
	if err := xml.NewDecoder(io.LimitReader(resp.Body, 32<<20)).Decode(&r); err != nil {
		return Listing{}, fmt.Errorf("couldn't read the listing: %w", err)
	}
	out := Listing{Folders: []string{}, Objects: []Object{}}
	for _, p := range r.CommonPrefixes {
		// S3 ends a folder with the delimiter; some servers leave it off.
		if delimiter != "" && !strings.HasSuffix(p.Prefix, delimiter) {
			p.Prefix += delimiter
		}
		out.Folders = append(out.Folders, p.Prefix)
	}
	for _, o := range r.Contents {
		t, _ := time.Parse(time.RFC3339, o.LastModified)
		out.Objects = append(out.Objects, Object{Key: o.Key, Size: o.Size, Modified: t, ETag: strings.Trim(o.ETag, `"`)})
	}
	if r.IsTruncated {
		out.Next = r.NextContinuationToken
	}
	return out, nil
}

// Head reads an object's size, type and when it changed.
func (c *S3) Head(ctx context.Context, bucket, key string) (Object, string, error) {
	resp, err := c.request(ctx, http.MethodHead, bucket, key, nil, nil, nil, 0, "")
	if err != nil {
		return Object{}, "", err
	}
	resp.Body.Close()
	t, _ := http.ParseTime(resp.Header.Get("Last-Modified"))
	return Object{Key: key, Size: resp.ContentLength, Modified: t, ETag: strings.Trim(resp.Header.Get("ETag"), `"`)}, resp.Header.Get("Content-Type"), nil
}

// Get opens an object for reading. The caller closes the body.
func (c *S3) Get(ctx context.Context, bucket, key string) (*http.Response, error) {
	return c.request(ctx, http.MethodGet, bucket, key, nil, nil, nil, 0, "")
}

// Put writes an object from a stream of a known size.
func (c *S3) Put(ctx context.Context, bucket, key, contentType string, body io.Reader, size int64) error {
	h := http.Header{}
	if contentType != "" {
		h.Set("Content-Type", contentType)
	}
	resp, err := c.request(ctx, http.MethodPut, bucket, key, nil, h, body, size, unsigned)
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

// Delete removes one object.
func (c *S3) Delete(ctx context.Context, bucket, key string) error {
	resp, err := c.request(ctx, http.MethodDelete, bucket, key, nil, nil, nil, 0, "")
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

// DeleteMany removes up to 1,000 objects in one request.
func (c *S3) DeleteMany(ctx context.Context, bucket string, keys []string) error {
	if len(keys) == 0 {
		return nil
	}
	type obj struct {
		Key string `xml:"Key"`
	}
	body, _ := xml.Marshal(struct {
		XMLName xml.Name `xml:"Delete"`
		Quiet   bool     `xml:"Quiet"`
		Objects []obj    `xml:"Object"`
	}{Quiet: true, Objects: func() []obj {
		out := make([]obj, len(keys))
		for i, k := range keys {
			out[i] = obj{Key: k}
		}
		return out
	}()})
	sum := md5.Sum(body)
	h := http.Header{}
	h.Set("Content-MD5", base64.StdEncoding.EncodeToString(sum[:]))
	h.Set("Content-Type", "application/xml")
	resp, err := c.request(ctx, http.MethodPost, bucket, "", url.Values{"delete": {""}}, h, bytes.NewReader(body), int64(len(body)), hashHex(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	// A quiet delete answers 200 and lists only the keys it couldn't
	// delete; one left behind would have Empty list it again forever.
	var r struct {
		Errors []struct {
			Key     string `xml:"Key"`
			Code    string `xml:"Code"`
			Message string `xml:"Message"`
		} `xml:"Error"`
	}
	if err := xml.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&r); err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("couldn't read what the delete did: %w", err)
	}
	if len(r.Errors) > 0 {
		e := r.Errors[0]
		return &S3Error{Status: http.StatusOK, Code: e.Code, Message: fmt.Sprintf("%d files weren't deleted, %s among them: %s", len(r.Errors), e.Key, e.Message)}
	}
	return nil
}

// Empty deletes every object in a bucket, a thousand at a time.
func (c *S3) Empty(ctx context.Context, bucket string) error {
	for {
		page, err := c.List(ctx, bucket, "", "", "", 1000)
		if err != nil {
			if NotFound(err) {
				return nil
			}
			return err
		}
		if len(page.Objects) == 0 {
			return nil
		}
		keys := make([]string, len(page.Objects))
		for i, o := range page.Objects {
			keys[i] = o.Key
		}
		if err := c.DeleteMany(ctx, bucket, keys); err != nil {
			return err
		}
	}
}

// Count counts the objects in a bucket and their size, up to limit; more
// says there were more than that.
func (c *S3) Count(ctx context.Context, bucket string, limit int) (files int, bytes int64, more bool, err error) {
	after := ""
	for {
		page, err := c.List(ctx, bucket, "", "", after, 1000)
		if err != nil {
			return files, bytes, false, err
		}
		for _, o := range page.Objects {
			files++
			bytes += o.Size
		}
		if page.Next == "" {
			return files, bytes, false, nil
		}
		if files >= limit {
			return files, bytes, true, nil
		}
		after = page.Next
	}
}
