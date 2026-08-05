package updatetest

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	selfupdate "github.com/mrz1836/go-selfupdate"
)

// These tests exercise the selfupdate-typed fixtures directly, so a broken
// stub, fixture server, or config seam fails here rather than surfacing as
// a confusing failure in a downstream suite.

func TestStubSourceReturnsReleaseAndRecordsContext(t *testing.T) {
	t.Parallel()

	rel := &selfupdate.Release{TagName: "v1.2.3"}
	s := &StubSource{Release: rel}

	type ctxKey string
	ctx := context.WithValue(context.Background(), ctxKey("k"), "v")

	got, err := s.Latest(ctx)
	if err != nil {
		t.Fatalf("Latest() = %v, want nil", err)
	}
	if got != rel {
		t.Errorf("Latest() = %+v, want the configured release", got)
	}
	if s.Calls() != 1 {
		t.Errorf("Calls() = %d, want 1", s.Calls())
	}
	if s.LastContext() != ctx {
		t.Error("LastContext() did not record the context passed to Latest")
	}
}

func TestStubSourceReturnsError(t *testing.T) {
	t.Parallel()

	sentinel := errors.New("lookup failed")
	s := &StubSource{Err: sentinel}

	if _, err := s.Latest(context.Background()); !errors.Is(err, sentinel) {
		t.Fatalf("Latest() = %v, want the configured error", err)
	}
	if s.Calls() != 1 {
		t.Errorf("Calls() = %d, want 1", s.Calls())
	}
}

func TestStubSourcePanics(t *testing.T) {
	t.Parallel()

	s := &StubSource{Panic: true}
	defer func() {
		if recover() == nil {
			t.Error("Latest() did not panic when Panic is set")
		}
		// The call is counted before the panic, mirroring a real lookup
		// that got far enough to matter.
		if s.Calls() != 1 {
			t.Errorf("Calls() = %d, want 1 even after a panic", s.Calls())
		}
	}()
	_, _ = s.Latest(context.Background())
}

func TestNewReleaseFixtureServesArchiveAndChecksums(t *testing.T) {
	t.Parallel()

	fx := NewReleaseFixture(t, "widget", "1.2.3", "widget", []byte("payload"))

	if fx.Release.TagName != "v1.2.3" || fx.Release.Name != "v1.2.3" {
		t.Errorf("release tag/name = %q/%q, want v1.2.3", fx.Release.TagName, fx.Release.Name)
	}
	if len(fx.Release.Assets) != 2 {
		t.Fatalf("got %d assets, want 2 (archive + checksums)", len(fx.Release.Assets))
	}
	if fx.AssetName == "" || fx.ChecksumName == "" || fx.SHA256 == "" || fx.BaseURL == "" {
		t.Fatalf("fixture left a field empty: %+v", fx)
	}

	// The server actually serves the archive and its checksums entry over
	// loopback — the two handler closures the fixture registers.
	archive := fetch(t, fx.BaseURL+"/"+fx.AssetName)
	if !bytes.Equal(archive, fx.Archive) {
		t.Error("served archive does not match the fixture's Archive bytes")
	}

	sums := fetch(t, fx.BaseURL+"/"+fx.ChecksumName)
	if !strings.Contains(string(sums), fx.SHA256) || !strings.Contains(string(sums), fx.AssetName) {
		t.Errorf("checksums body %q does not list %q for %q", sums, fx.SHA256, fx.AssetName)
	}
}

func TestQuietConfigWiresTestSeams(t *testing.T) {
	t.Parallel()

	src := &StubSource{Release: &selfupdate.Release{TagName: "v1.0.0"}}
	cfg, out := QuietConfig(t, src, "/tmp/widget")

	if cfg.Source != src {
		t.Error("QuietConfig did not wire the given source")
	}
	if cfg.TargetPath != "/tmp/widget" {
		t.Errorf("TargetPath = %q, want /tmp/widget", cfg.TargetPath)
	}
	if cfg.Owner != "acme" || cfg.Repo != "widget" || cfg.BinaryName != "widget" {
		t.Errorf("identity fields = %q/%q/%q, want acme/widget/widget", cfg.Owner, cfg.Repo, cfg.BinaryName)
	}
	if cfg.Logger == nil {
		t.Error("QuietConfig left the logger nil")
	}

	// Stdout is the returned buffer, so a test can assert on what the
	// updater printed.
	_, _ = fmt.Fprint(cfg.Stdout, "hello")
	if out.String() != "hello" {
		t.Errorf("captured stdout = %q, want %q", out.String(), "hello")
	}
}

// fetch GETs url over loopback and returns the body, failing the test on
// any error. httptest binds to 127.0.0.1, so this dials no real network.
func fetch(t *testing.T, url string) []byte {
	t.Helper()

	resp, err := http.Get(url) //nolint:gosec,noctx // url is this test's own loopback httptest server
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read %s: %v", url, err)
	}
	return body
}
