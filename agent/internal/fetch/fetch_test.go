package fetch

import (
	"archive/tar"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The downloader fetches URLs chosen by a paying stranger from inside the
// provider's network. These tests are the reason it is safe to do that.

func TestCheckURLRejectsUnsafeTargets(t *testing.T) {
	f := New(Options{})

	cases := []struct {
		name string
		url  string
		want string
	}{
		{"loopback by ip", "https://127.0.0.1/data.tar", "loopback"},
		{"loopback v6", "https://[::1]/data.tar", "loopback"},
		{"cloud metadata", "https://169.254.169.254/latest/meta-data/", "link-local"},
		{"private 10", "https://10.0.0.5/data.tar", "private"},
		{"private 192.168", "https://192.168.1.10/data.tar", "private"},
		{"private 172.16", "https://172.16.0.1/data.tar", "private"},
		{"carrier grade nat", "https://100.64.0.1/data.tar", "carrier-grade"},
		{"plaintext http", "http://example.com/data.tar", "https"},
		{"file scheme", "file:///etc/passwd", "scheme"},
		{"gopher scheme", "gopher://example.com/data", "scheme"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := f.Preflight(context.Background(), tc.url)
			if err == nil {
				t.Fatalf("Preflight(%q) allowed a blocked target", tc.url)
			}
			if !IsBlocked(err) {
				t.Fatalf("Preflight(%q) = %v, want a policy rejection", tc.url, err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q does not mention %q", err, tc.want)
			}
		})
	}
}

func TestAllowPrivatePermitsLANMirror(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", "4")
		_, _ = w.Write([]byte("data"))
	}))
	defer server.Close()

	// httptest listens on 127.0.0.1, which the default policy blocks.
	if _, err := New(Options{}).Preflight(context.Background(), server.URL); !IsBlocked(err) {
		t.Fatalf("default policy should block a loopback server, got %v", err)
	}

	size, err := New(Options{AllowPrivate: true, AllowHTTP: true}).
		Preflight(context.Background(), server.URL)
	if err != nil {
		t.Fatalf("operator opt-in should permit a LAN mirror: %v", err)
	}
	if size != 4 {
		t.Errorf("size = %d, want 4", size)
	}
}

func TestHostAllowlist(t *testing.T) {
	f := New(Options{HostAllowlist: []string{"datasets.example.org"}})

	if err := f.checkURL(mustParse(t, "https://elsewhere.example.com/x.tar")); !IsBlocked(err) {
		t.Errorf("host off the allowlist should be blocked, got %v", err)
	}
	if err := f.checkURL(mustParse(t, "https://DataSets.Example.ORG/x.tar")); err != nil {
		t.Errorf("allowlist should match case-insensitively: %v", err)
	}
}

func TestPreflightRejectsOversizedContentLength(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", "1073741824") // 1 GiB
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	f := New(Options{MaxBytes: 1 << 20, AllowPrivate: true, AllowHTTP: true})
	_, err := f.Preflight(context.Background(), server.URL)
	if !IsBlocked(err) {
		t.Fatalf("oversized dataset should be rejected before payment, got %v", err)
	}
	if !strings.Contains(err.Error(), "above this node's limit") {
		t.Errorf("error should name the limit: %v", err)
	}
}

func TestPreflightFallsBackToRangeWhenHeadIsRefused(t *testing.T) {
	// Presigned URLs commonly reject HEAD. A renter should not be turned away
	// for using one.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Range", "bytes 0-0/2048")
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write([]byte("x"))
	}))
	defer server.Close()

	size, err := New(Options{AllowPrivate: true, AllowHTTP: true}).
		Preflight(context.Background(), server.URL)
	if err != nil {
		t.Fatalf("range fallback failed: %v", err)
	}
	if size != 2048 {
		t.Errorf("size = %d, want 2048 from Content-Range", size)
	}
}

func TestDownloadEnforcesLimitOnAnUndeclaredBody(t *testing.T) {
	// A chunked response declares no length, so Preflight cannot judge it and
	// the byte counter is the only thing standing between a hostile URL and a
	// full disk. This is the case that matters.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		flusher, _ := w.(http.Flusher)
		for range 100 {
			_, _ = w.Write(make([]byte, 1024))
			if flusher != nil {
				flusher.Flush()
			}
		}
	}))
	defer server.Close()

	f := New(Options{MaxBytes: 4096, AllowPrivate: true, AllowHTTP: true})

	// Preflight cannot see a size, and says so rather than guessing.
	size, err := f.Preflight(context.Background(), server.URL)
	if err != nil {
		t.Fatalf("preflight of an undeclared body should pass: %v", err)
	}
	if size != -1 {
		t.Errorf("size = %d, want -1 for an unknown length", size)
	}

	// The download is cut off at the limit regardless.
	written, _, err := f.Download(context.Background(), server.URL,
		filepath.Join(t.TempDir(), "data.bin"), "", nil)
	if !IsBlocked(err) {
		t.Fatalf("an oversized undeclared body must be cut off, got %v", err)
	}
	if written > 4096+256<<10 {
		t.Errorf("wrote %d bytes past the limit before stopping", written)
	}
}

func TestDownloadVerifiesChecksum(t *testing.T) {
	payload := []byte("the exact bytes the renter asked for")
	sum := sha256.Sum256(payload)
	correct := hex.EncodeToString(sum[:])

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(payload)
	}))
	defer server.Close()

	f := New(Options{AllowPrivate: true, AllowHTTP: true})
	dir := t.TempDir()

	written, got, err := f.Download(context.Background(), server.URL, filepath.Join(dir, "ok.bin"), correct, nil)
	if err != nil {
		t.Fatalf("matching checksum should succeed: %v", err)
	}
	if written != int64(len(payload)) || got != correct {
		t.Errorf("written=%d sum=%s, want %d %s", written, got, len(payload), correct)
	}

	_, _, err = f.Download(context.Background(), server.URL, filepath.Join(dir, "bad.bin"),
		strings.Repeat("0", 64), nil)
	if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Errorf("mismatched checksum should fail loudly, got %v", err)
	}
}

func TestDownloadReportsProgress(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(make([]byte, 512<<10))
	}))
	defer server.Close()

	var calls int
	var last Progress
	f := New(Options{AllowPrivate: true, AllowHTTP: true})
	_, _, err := f.Download(context.Background(), server.URL,
		filepath.Join(t.TempDir(), "data.bin"), "", func(p Progress) {
			calls++
			last = p
		})
	if err != nil {
		t.Fatalf("download: %v", err)
	}
	// The final call always fires, whatever the timing of the throttled ones.
	if calls == 0 || last.Downloaded != 512<<10 {
		t.Errorf("progress calls=%d last=%d, want a final report of %d", calls, last.Downloaded, 512<<10)
	}
}

func TestFilenameFor(t *testing.T) {
	cases := []struct{ url, requested, want string }{
		{"https://example.org/sets/cifar10.tar.gz", "", "cifar10.tar.gz"},
		{"https://example.org/download?id=7", "", "download"},
		{"https://example.org/", "", "dataset"},
		{"https://example.org/a.tar", "mine.tar", "mine.tar"},
		{"https://example.org/a.tar", "../../etc/passwd", "passwd"},
	}
	for _, tc := range cases {
		if got := FilenameFor(tc.url, tc.requested); got != tc.want {
			t.Errorf("FilenameFor(%q, %q) = %q, want %q", tc.url, tc.requested, got, tc.want)
		}
	}
}

func TestExtractRefusesPathEscape(t *testing.T) {
	cases := []struct {
		name  string
		entry string
	}{
		{"parent traversal", "../escaped.txt"},
		{"deep traversal", "a/b/../../../escaped.txt"},
		{"absolute path", "/etc/cleargate-owned"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			archive := filepath.Join(dir, "evil.tar")
			writeTar(t, archive, tc.entry, []byte("owned"))

			dest := filepath.Join(dir, "data")
			if err := os.MkdirAll(dest, 0o755); err != nil {
				t.Fatal(err)
			}
			err := Extract(archive, dest, 1<<20)
			if err == nil {
				t.Fatalf("entry %q was extracted; it must be refused", tc.entry)
			}
			if !strings.Contains(err.Error(), "escapes") && !strings.Contains(err.Error(), "absolute") {
				t.Errorf("unexpected error for %q: %v", tc.entry, err)
			}
		})
	}
}

func TestExtractRefusesSymlinks(t *testing.T) {
	dir := t.TempDir()
	archive := filepath.Join(dir, "link.tar")

	file, err := os.Create(archive)
	if err != nil {
		t.Fatal(err)
	}
	writer := tar.NewWriter(file)
	if err := writer.WriteHeader(&tar.Header{
		Name:     "shortcut",
		Typeflag: tar.TypeSymlink,
		Linkname: "/etc/passwd",
		Mode:     0o777,
	}); err != nil {
		t.Fatal(err)
	}
	writer.Close()
	file.Close()

	dest := filepath.Join(dir, "data")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := Extract(archive, dest, 1<<20); err == nil || !strings.Contains(err.Error(), "link") {
		t.Fatalf("symlink entry should be refused, got %v", err)
	}
}

func TestExtractHonoursSizeLimit(t *testing.T) {
	dir := t.TempDir()
	archive := filepath.Join(dir, "big.tar")
	writeTar(t, archive, "big.bin", make([]byte, 64<<10))

	dest := filepath.Join(dir, "data")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := Extract(archive, dest, 8<<10); err == nil {
		t.Fatal("an archive expanding past the limit should be refused")
	}
}

func TestExtractUnpacksAGoodArchive(t *testing.T) {
	dir := t.TempDir()
	archive := filepath.Join(dir, "good.tar")
	writeTar(t, archive, "train/labels.csv", []byte("id,label\n1,cat\n"))

	dest := filepath.Join(dir, "data")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := Extract(archive, dest, 1<<20); err != nil {
		t.Fatalf("extract: %v", err)
	}

	content, err := os.ReadFile(filepath.Join(dest, "train", "labels.csv"))
	if err != nil {
		t.Fatalf("expected file was not extracted: %v", err)
	}
	if !strings.Contains(string(content), "cat") {
		t.Errorf("content = %q", content)
	}
}

func TestIsArchive(t *testing.T) {
	for _, name := range []string{"a.tar", "a.tar.gz", "a.tgz", "A.ZIP"} {
		if !IsArchive(name) {
			t.Errorf("IsArchive(%q) = false", name)
		}
	}
	for _, name := range []string{"a.csv", "weights.pt", "a.tar.bz2"} {
		if IsArchive(name) {
			t.Errorf("IsArchive(%q) = true", name)
		}
	}
}

func TestHumanBytes(t *testing.T) {
	cases := map[int64]string{
		-1:      "unknown size",
		512:     "512 B",
		2048:    "2.0 KiB",
		1 << 20: "1.0 MiB",
		1 << 30: "1.0 GiB",
	}
	for input, want := range cases {
		if got := HumanBytes(input); got != want {
			t.Errorf("HumanBytes(%d) = %q, want %q", input, got, want)
		}
	}
}

func writeTar(t *testing.T, archivePath, name string, content []byte) {
	t.Helper()
	file, err := os.Create(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()

	writer := tar.NewWriter(file)
	if err := writer.WriteHeader(&tar.Header{
		Name: name,
		Mode: 0o644,
		Size: int64(len(content)),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
}

func mustParse(t *testing.T, raw string) *url.URL {
	t.Helper()
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse %q: %v", raw, err)
	}
	return parsed
}
