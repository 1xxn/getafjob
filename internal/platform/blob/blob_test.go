package blob_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/1xxn/getafjob/internal/platform/blob"
	"github.com/1xxn/getafjob/internal/platform/config"
	"github.com/1xxn/getafjob/internal/platform/crypto"
	"github.com/1xxn/getafjob/internal/platform/db"
	"github.com/1xxn/getafjob/internal/platform/db/dbtest"
)

func TestValidKey(t *testing.T) {
	for key, want := range map[string]bool{
		"cv.pdf": true, "documents/42/cv-max.pdf": true, "a/b_c/d.e-f": true,
		"": false, "../x": false, "a/../b": false, "/abs": false, "a//b": false, "Upper.pdf": false,
		".hidden": false, "a/.tmp": false, `a\b`: false, "a b": false, "a/": false,
		strings.Repeat("a/", 12) + "z": false,
	} {
		if blob.ValidKey(key) != want {
			t.Errorf("ValidKey(%q) = %v", key, !want)
		}
	}
}

// exercise runs the Store contract against s.
func exercise(t *testing.T, s blob.Store) {
	t.Helper()
	ctx := context.Background()
	if _, err := s.Get(ctx, "missing/x"); !errors.Is(err, blob.ErrNotFound) {
		t.Errorf("missing: %v", err)
	}
	if err := s.Put(ctx, "documents/1/cv.pdf", []byte("v1")); err != nil {
		t.Fatal(err)
	}
	if err := s.Put(ctx, "documents/1/cv.pdf", []byte("v2")); err != nil {
		t.Fatal(err)
	}
	if b, err := s.Get(ctx, "documents/1/cv.pdf"); err != nil || string(b) != "v2" {
		t.Fatalf("get = %q, %v", b, err)
	}
	if err := s.Delete(ctx, "documents/1/cv.pdf"); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(ctx, "documents/1/cv.pdf"); err != nil {
		t.Errorf("second delete: %v", err)
	}
	if _, err := s.Get(ctx, "documents/1/cv.pdf"); !errors.Is(err, blob.ErrNotFound) {
		t.Errorf("after delete: %v", err)
	}
	for _, bad := range []string{"../escape", "a/../../b", "/etc/passwd"} {
		if err := s.Put(ctx, bad, []byte("x")); !errors.Is(err, blob.ErrBadKey) {
			t.Errorf("put %q: %v", bad, err)
		}
	}
	if err := s.Put(ctx, "big", make([]byte, blob.MaxObject+1)); !errors.Is(err, blob.ErrTooLarge) {
		t.Errorf("too large: %v", err)
	}
}

func TestDir(t *testing.T) {
	d, err := blob.NewDir(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	exercise(t, d)
}

// fakeS3 keeps objects in memory and checks that every request is signed.
type fakeS3 struct {
	t    *testing.T
	mu   sync.Mutex
	objs map[string][]byte
}

func (f *fakeS3) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !strings.HasPrefix(r.Header.Get("Authorization"), "AWS4-HMAC-SHA256 Credential=AKMAX/") {
		f.t.Errorf("unsigned request %s %s", r.Method, r.URL.Path)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	switch r.Method {
	case http.MethodPut:
		body, err := readBody(r)
		if err != nil {
			f.t.Error(err)
		}
		f.objs[r.URL.Path] = body
		w.Header().Set("ETag", `"x"`)
	case http.MethodGet, http.MethodHead:
		b, ok := f.objs[r.URL.Path]
		if !ok {
			w.Header().Set("Content-Type", "application/xml")
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `<Error><Code>NoSuchKey</Code><Message>missing</Message></Error>`)
			return
		}
		w.Header().Set("Content-Length", strconv.Itoa(len(b)))
		w.Header().Set("Last-Modified", "Wed, 04 Nov 2026 10:00:00 GMT")
		w.Header().Set("ETag", `"x"`)
		if r.Method == http.MethodGet {
			_, _ = w.Write(b)
		}
	case http.MethodDelete:
		delete(f.objs, r.URL.Path)
		w.WriteHeader(http.StatusNoContent)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

// readBody decodes aws-chunked uploads (signed streaming over plain http).
func readBody(r *http.Request) ([]byte, error) {
	if !strings.HasPrefix(r.Header.Get("X-Amz-Content-Sha256"), "STREAMING-") {
		return io.ReadAll(r.Body)
	}
	var out bytes.Buffer
	br := bufio.NewReader(r.Body)
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			return nil, err
		}
		size, _, _ := strings.Cut(strings.TrimSpace(line), ";")
		n, err := strconv.ParseInt(size, 16, 64)
		if err != nil {
			return nil, err
		}
		if n == 0 {
			return out.Bytes(), nil
		}
		if _, err := io.CopyN(&out, br, n); err != nil {
			return nil, err
		}
		if _, err := br.Discard(2); err != nil {
			return nil, err
		}
	}
}

func TestS3(t *testing.T) {
	fake := &fakeS3{t: t, objs: map[string][]byte{}}
	srv := httptest.NewServer(fake)
	defer srv.Close()
	s, err := blob.NewS3ForTest(srv.URL, "bucket", "getafjob", blob.S3Credentials{AccessKey: "AKMAX", SecretKey: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	exercise(t, s)
	if err := s.Put(context.Background(), "a/b", []byte("x")); err != nil {
		t.Fatal(err)
	}
	if _, ok := fake.objs["/bucket/getafjob/a/b"]; !ok {
		t.Errorf("objects = %v (bucket and prefix)", fake.objs)
	}
}

func TestOpen(t *testing.T) {
	creds := blob.S3Credentials{AccessKey: "a", SecretKey: "b"}
	for raw, ok := range map[string]bool{
		"file://" + "/" + strings.TrimPrefix(strings.ReplaceAll(t.TempDir(), `\`, "/"), "/"): true,
		"s3://bucket/prefix?endpoint=https://s3.example.org&region=eu-central-1":             true,
		"s3://bucket?endpoint=http://minio:9000":                                             true,
		"s3://bucket?endpoint=http://s3.example.org":                                         false, // plain http to the internet
		"s3://?endpoint=https://s3.example.org":                                              false,
		"s3://bucket/../x?endpoint=https://s3.example.org":                                   false,
		"file://host/path": false,
		"ftp://x":          false,
	} {
		s, err := blob.Open(raw, creds)
		if c, isCloser := s.(io.Closer); isCloser {
			_ = c.Close()
		}
		if (err == nil) != ok {
			t.Errorf("Open(%q) = %v", raw, err)
		}
	}
	if _, err := blob.Open("s3://bucket?endpoint=https://s3.example.org", blob.S3Credentials{}); err == nil {
		t.Error("s3 without credentials")
	}
}

// fakeSealer marks data with its user and purpose; Open checks both.
type fakeSealer struct{}

func (fakeSealer) Seal(_ context.Context, u uuid.UUID, purpose string, pt []byte) ([]byte, error) {
	return append([]byte(u.String()+"|"+purpose+"|"), pt...), nil
}

func (fakeSealer) Open(_ context.Context, u uuid.UUID, purpose string, ct []byte) ([]byte, error) {
	rest, ok := bytes.CutPrefix(ct, []byte(u.String()+"|"+purpose+"|"))
	if !ok {
		return nil, errors.New("decryption failed")
	}
	return rest, nil
}

func TestSealed(t *testing.T) {
	d, err := blob.NewDir(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	s := blob.NewSealed(d, fakeSealer{})
	ctx := context.Background()
	max, erika := uuid.New(), uuid.New()
	if err := s.Put(ctx, max, "documents/1/cv.pdf", []byte("CV of Max Mustermann")); err != nil {
		t.Fatal(err)
	}
	raw, err := d.Get(ctx, "users/"+max.String()+"/documents/1/cv.pdf")
	if err != nil || !bytes.HasPrefix(raw, []byte(max.String())) {
		t.Fatalf("stored = %q, %v (sealed under the user's prefix)", raw, err)
	}
	if b, err := s.Get(ctx, max, "documents/1/cv.pdf"); err != nil || string(b) != "CV of Max Mustermann" {
		t.Fatalf("get = %q, %v", b, err)
	}
	if _, err := s.Get(ctx, erika, "documents/1/cv.pdf"); !errors.Is(err, blob.ErrNotFound) {
		t.Errorf("other user: %v", err)
	}
	// An object moved to another key does not open.
	if err := d.Put(ctx, "users/"+max.String()+"/documents/2/cv.pdf", raw); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(ctx, max, "documents/2/cv.pdf"); err == nil {
		t.Error("moved object opened")
	}
	if err := s.Put(ctx, uuid.Nil, "x", nil); !errors.Is(err, blob.ErrBadKey) {
		t.Errorf("nil user: %v", err)
	}
	if err := s.Delete(ctx, max, "documents/1/cv.pdf"); err != nil {
		t.Fatal(err)
	}
}

func TestSealedWithEnvelope(t *testing.T) {
	pool := dbtest.AppPool(t)
	ring, err := crypto.NewKeyring(config.Secret(base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32))), "")
	if err != nil {
		t.Fatal(err)
	}
	var max uuid.UUID
	if err := db.InSystemTx(t.Context(), pool, func(tx pgx.Tx) error {
		return tx.QueryRow(t.Context(), "INSERT INTO users (email) VALUES ('max.mustermann@example.org') RETURNING id").Scan(&max)
	}); err != nil {
		t.Fatal(err)
	}
	d, err := blob.NewDir(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	s := blob.NewSealed(d, crypto.NewEnvelope(ring, pool))
	cv := []byte("Max Mustermann, Musterstrasse 1, 12345 Musterstadt")
	if err := s.Put(t.Context(), max, "documents/1/cv.txt", cv); err != nil {
		t.Fatal(err)
	}
	raw, err := d.Get(t.Context(), "users/"+max.String()+"/documents/1/cv.txt")
	if err != nil || bytes.Contains(raw, []byte("Mustermann")) {
		t.Fatalf("stored plaintext or %v", err)
	}
	if b, err := s.Get(t.Context(), max, "documents/1/cv.txt"); err != nil || !bytes.Equal(b, cv) {
		t.Fatalf("get = %q, %v", b, err)
	}
}
