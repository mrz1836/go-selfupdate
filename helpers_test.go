package selfupdate

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"testing"
	"time"
)

// tarEntry describes one file written into a test archive.
type tarEntry struct {
	name string
	body []byte
	mode int64
}

// makeTarGz builds a gzipped tarball containing entries, in order.
func makeTarGz(t *testing.T, entries ...tarEntry) []byte {
	t.Helper()

	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)

	for _, e := range entries {
		mode := e.mode
		if mode == 0 {
			mode = 0o755
		}
		hdr := &tar.Header{
			Name:     e.name,
			Mode:     mode,
			Size:     int64(len(e.body)),
			Typeflag: tar.TypeReg,
			ModTime:  time.Unix(0, 0),
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("write tar header %q: %v", e.name, err)
		}
		if _, err := tw.Write(e.body); err != nil {
			t.Fatalf("write tar body %q: %v", e.name, err)
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

// sha256Hex returns the lowercase hex SHA-256 of b.
func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// writeTempFile writes body into a new file inside dir and returns its
// path.
func writeTempFile(t *testing.T, dir, name string, body []byte, mode os.FileMode) string {
	t.Helper()

	path := dir + string(os.PathSeparator) + name
	if err := os.WriteFile(path, body, mode); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

// currentAssetName returns a goreleaser-shaped archive name for the
// running platform.
func currentAssetName(project, version string) string {
	return fmt.Sprintf("%s_%s_%s_%s.tar.gz", project, version, runtime.GOOS, runtime.GOARCH)
}

// stubSource is a ReleaseSource that returns a fixed release or error.
type stubSource struct {
	release *Release
	err     error
	calls   int
}

// Latest records the call and returns the configured result.
func (s *stubSource) Latest(context.Context) (*Release, error) {
	s.calls++
	if s.err != nil {
		return nil, s.err
	}
	return s.release, nil
}

// countingTransport records how many HTTP round-trips were attempted and
// refuses to perform any of them. A test that asserts "no network" wants
// proof that no request left the process, not merely that an error came
// back.
type countingTransport struct {
	calls int
}

// RoundTrip counts the attempt and fails it.
func (c *countingTransport) RoundTrip(*http.Request) (*http.Response, error) {
	c.calls++
	return nil, fmt.Errorf("countingTransport: unexpected request") //nolint:err113 // test-only sentinel
}

// releaseFixture spins up an HTTP server serving an archive and its
// checksums file, and returns the release that points at them.
func releaseFixture(t *testing.T, project, version, binaryName string, body []byte) (*Release, []byte) {
	t.Helper()

	archive := makeTarGz(t, tarEntry{name: binaryName, body: body})
	assetName := currentAssetName(project, version)
	checksumName := fmt.Sprintf("%s_%s_checksums.txt", project, version)
	checksums := fmt.Sprintf("%s  %s\n", sha256Hex(archive), assetName)

	mux := http.NewServeMux()
	mux.HandleFunc("/"+assetName, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(archive)
	})
	mux.HandleFunc("/"+checksumName, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(checksums))
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	return &Release{
		TagName: "v" + version,
		Name:    "v" + version,
		Assets: []ReleaseAsset{
			{Name: assetName, BrowserDownloadURL: srv.URL + "/" + assetName},
			{Name: checksumName, BrowserDownloadURL: srv.URL + "/" + checksumName},
		},
	}, archive
}
