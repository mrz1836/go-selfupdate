package testutil

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// These tests exercise the shared fixtures directly. They matter because a
// bug in a fixture is invisible where it is used — a MakeTarGz that dropped
// the exec bit, or a CountingTransport that stopped counting, would quietly
// weaken every suite that leans on it rather than failing loudly here.

func TestMakeTarGzRoundTrips(t *testing.T) {
	t.Parallel()

	data := MakeTarGz(t,
		TarEntry{Name: "bin/widget", Body: []byte("payload")},
		TarEntry{Name: "notes.txt", Body: []byte("hi"), Mode: 0o644},
	)

	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("gzip reader: %v", err)
	}
	t.Cleanup(func() { _ = gz.Close() })
	tr := tar.NewReader(gz)

	want := []struct {
		name string
		body string
		mode int64
	}{
		{"bin/widget", "payload", 0o755}, // zero Mode defaults to 0o755
		{"notes.txt", "hi", 0o644},
	}
	for _, w := range want {
		hdr, nerr := tr.Next()
		if nerr != nil {
			t.Fatalf("tar entry %q: %v", w.name, nerr)
		}
		if hdr.Name != w.name {
			t.Errorf("name = %q, want %q", hdr.Name, w.name)
		}
		if hdr.Mode != w.mode {
			t.Errorf("%s mode = %o, want %o", w.name, hdr.Mode, w.mode)
		}
		body, rerr := io.ReadAll(tr)
		if rerr != nil {
			t.Fatalf("read %q: %v", w.name, rerr)
		}
		if string(body) != w.body {
			t.Errorf("%s body = %q, want %q", w.name, body, w.body)
		}
	}
	if _, nerr := tr.Next(); nerr != io.EOF {
		t.Errorf("expected exactly two entries, got a third (err=%v)", nerr)
	}
}

func TestSHA256Hex(t *testing.T) {
	t.Parallel()

	// Well-known digests: the empty input and "abc".
	cases := map[string]string{
		"":    "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		"abc": "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad",
	}
	for in, want := range cases {
		if got := SHA256Hex([]byte(in)); got != want {
			t.Errorf("SHA256Hex(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCurrentAssetName(t *testing.T) {
	t.Parallel()

	want := fmt.Sprintf("widget_1.2.3_%s_%s.tar.gz", runtime.GOOS, runtime.GOARCH)
	if got := CurrentAssetName("widget", "1.2.3"); got != want {
		t.Errorf("CurrentAssetName() = %q, want %q", got, want)
	}
}

func TestWriteTempFileAndAssertContents(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := WriteTempFile(t, dir, "f.txt", []byte("body"), 0o600)

	if filepath.Dir(path) != dir || filepath.Base(path) != "f.txt" {
		t.Errorf("path = %q, want %s/f.txt", path, dir)
	}
	AssertFileContents(t, path, "body")

	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat: %v", err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Errorf("mode = %v, want 0600", info.Mode().Perm())
		}
	}
}

func TestLockDir(t *testing.T) {
	t.Parallel()

	// Verified on every host and user, since LockDir's chmod to 0500
	// succeeds even for root; the write-refusal it enables is asserted by
	// the suites that use it, not here.
	dir, target := LockDir(t, t.TempDir())

	if filepath.Dir(target) != dir {
		t.Errorf("target %q is not inside the locked dir %q", target, dir)
	}
	AssertFileContents(t, target, "locked binary")

	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat locked dir: %v", err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o500 {
		t.Errorf("locked dir mode = %v, want 0500", info.Mode().Perm())
	}
}

func TestEnvMap(t *testing.T) {
	t.Parallel()

	if got := EnvMap(nil)("ANYTHING"); got != "" {
		t.Errorf("EnvMap(nil)(...) = %q, want empty", got)
	}
	get := EnvMap(map[string]string{"K": "v"})
	if got := get("K"); got != "v" {
		t.Errorf("get(K) = %q, want v", got)
	}
	if got := get("MISSING"); got != "" {
		t.Errorf("get(MISSING) = %q, want empty", got)
	}
}

func TestCountingTransportRefusesAndCounts(t *testing.T) {
	t.Parallel()

	ct := &CountingTransport{}
	req, err := http.NewRequest(http.MethodGet, "http://example.invalid/x", http.NoBody)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	for i := 1; i <= 3; i++ {
		resp, rerr := ct.RoundTrip(req)
		if rerr == nil {
			t.Fatal("RoundTrip should refuse every request")
		}
		if resp != nil {
			t.Error("RoundTrip returned a response alongside its error")
		}
		if ct.Calls() != i {
			t.Errorf("Calls() = %d after %d round-trips", ct.Calls(), i)
		}
	}
}

func TestSkipGuardsAreCallable(t *testing.T) {
	t.Parallel()

	// On a non-Windows, non-root host these return; elsewhere they skip.
	// Either way they must not panic.
	SkipOnWindows(t)
	SkipIfRoot(t)
}
