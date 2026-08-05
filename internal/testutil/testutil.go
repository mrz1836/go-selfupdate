// Package testutil holds the test fixtures shared across the module's
// test suites: tarball construction, hashing, temp-file helpers, an
// environment seam, a network-refusing transport, and a pair of skip
// guards.
//
// It imports only the standard library, deliberately. Every helper here
// is expressed in terms of testing.TB and stdlib types and touches no
// selfupdate type, so a white-box test in package selfupdate can import
// it without the import cycle that a package importing selfupdate would
// create. The three selfupdate-typed fixtures live in the sibling
// updatetest package instead.
package testutil

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"
)

// TarEntry describes one regular file written into a test archive.
type TarEntry struct {
	Name string
	Body []byte
	Mode int64
}

// MakeTarGz builds a gzipped tarball containing entries, in order. An
// entry with a zero Mode is written as 0o755, the goreleaser default for
// an executable.
func MakeTarGz(t testing.TB, entries ...TarEntry) []byte {
	t.Helper()

	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)

	for _, e := range entries {
		mode := e.Mode
		if mode == 0 {
			mode = 0o755
		}
		hdr := &tar.Header{
			Name:     e.Name,
			Mode:     mode,
			Size:     int64(len(e.Body)),
			Typeflag: tar.TypeReg,
			ModTime:  time.Unix(0, 0),
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("write tar header %q: %v", e.Name, err)
		}
		if _, err := tw.Write(e.Body); err != nil {
			t.Fatalf("write tar body %q: %v", e.Name, err)
		}
	}

	if err := tw.Close(); err != nil {
		t.Fatalf("close tar writer: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("close gzip writer: %v", err)
	}
	return buf.Bytes()
}

// SHA256Hex returns the lowercase hex SHA-256 of b.
func SHA256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// CurrentAssetName returns a goreleaser-shaped archive name for the
// running platform, e.g. "widget_1.2.3_darwin_arm64.tar.gz".
func CurrentAssetName(project, version string) string {
	return fmt.Sprintf("%s_%s_%s_%s.tar.gz", project, version, runtime.GOOS, runtime.GOARCH)
}

// WriteTempFile writes body into a new file named name inside dir and
// returns its path.
func WriteTempFile(t testing.TB, dir, name string, body []byte, mode os.FileMode) string {
	t.Helper()

	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, body, mode); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

// LockDir creates a read-only "install directory" for the tests that
// prove the updater refuses to write where it cannot: it makes
// parent/locked (mode 0500) holding one seed file, and registers a
// cleanup restoring 0700 so the enclosing t.TempDir() teardown can remove
// it. It returns the locked directory and the seeded file's path.
//
// The file is seeded before the directory is locked, because a 0500
// directory cannot be written into afterwards. Callers that only need the
// read-only directory itself ignore the second result.
func LockDir(t testing.TB, parent string) (dir, target string) {
	t.Helper()

	dir = filepath.Join(parent, "locked")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	target = WriteTempFile(t, dir, "widget", []byte("locked binary"), 0o755)
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatalf("chmod %s: %v", dir, err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
	return dir, target
}

// EnvMap returns a Getenv seam backed by vars, so a test never mutates
// the process environment and stays parallel-safe. EnvMap(nil) reports
// every variable as unset, which doubles as the "no environment" seam.
func EnvMap(vars map[string]string) func(string) string {
	return func(key string) string { return vars[key] }
}

// errNoNetwork is the failure a CountingTransport returns for every
// round-trip it refuses.
var errNoNetwork = errors.New("testutil: unexpected network request")

// CountingTransport records how many HTTP round-trips were attempted and
// refuses to perform any of them. A test that asserts "no network" wants
// proof that no request left the process, not merely that an error came
// back. It is mutex-guarded so the counter is safe to read while a
// background goroutine might still be attempting a request.
type CountingTransport struct {
	mu    sync.Mutex
	calls int
}

// RoundTrip counts the attempt and fails it.
func (c *CountingTransport) RoundTrip(*http.Request) (*http.Response, error) {
	c.mu.Lock()
	c.calls++
	c.mu.Unlock()
	return nil, errNoNetwork
}

// Calls reports how many round-trips were attempted.
func (c *CountingTransport) Calls() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

// AssertFileContents fails the test unless the file at path contains
// exactly want.
func AssertFileContents(t testing.TB, path, want string) {
	t.Helper()

	got, err := os.ReadFile(path) //nolint:gosec // path is a test-controlled temp file
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if string(got) != want {
		t.Errorf("%s = %q, want %q", path, got, want)
	}
}

// SkipOnWindows skips the test on Windows, where Unix mode bits and
// directory permissions do not gate file operations the way these
// assertions expect.
func SkipOnWindows(t testing.TB) {
	t.Helper()

	if runtime.GOOS == "windows" {
		t.Skip("unix mode bits are not meaningful on windows")
	}
}

// SkipIfRoot skips the test when it runs as root, which ignores the
// directory permission bits the assertion relies on.
func SkipIfRoot(t testing.TB) {
	t.Helper()

	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permission bits")
	}
}
