// Package updatetest holds the three test fixtures that are typed against
// the selfupdate package: a stub release source, a release fixture backed
// by an httptest server, and a quiet Config.
//
// It imports selfupdate (and the stdlib-only testutil package), so it can
// be imported by external test packages — cobracmd, notify, and the
// black-box package selfupdate_test — but not by white-box package
// selfupdate tests, which would form an import cycle. The cycle-free
// helpers live in testutil.
//
// Nothing here dials a real network. StubSource returns a canned release,
// and NewReleaseFixture serves its archive and checksums file from an
// httptest server bound to localhost that t.Cleanup tears down.
package updatetest

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	selfupdate "github.com/mrz1836/go-selfupdate"
	"github.com/mrz1836/go-selfupdate/internal/testutil"
)

// StubSource is a [selfupdate.ReleaseSource] that returns a fixed release,
// a fixed error, or a panic, and records how many times it was asked and
// the context of the most recent lookup.
//
// The call count is what proves a cache hit avoided the network rather
// than merely returning the right answer, and the recorded context backs
// a deadline assertion. Both are mutex-guarded because
// StartBackgroundCheck calls Latest from its own goroutine while the test
// observes them.
type StubSource struct {
	Release *selfupdate.Release
	Err     error
	Panic   bool

	mu      sync.Mutex
	calls   int
	lastCtx context.Context //nolint:containedctx // recorded for a deadline assertion, never used to make a call
}

// Latest records the call and returns the configured result.
func (s *StubSource) Latest(ctx context.Context) (*selfupdate.Release, error) {
	s.mu.Lock()
	s.calls++
	s.lastCtx = ctx
	s.mu.Unlock()

	if s.Panic {
		panic("updatetest: stub source panic")
	}
	if s.Err != nil {
		return nil, s.Err
	}
	return s.Release, nil
}

// Calls reports how many lookups the stub has served.
func (s *StubSource) Calls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

// LastContext returns the context of the most recent lookup.
func (s *StubSource) LastContext() context.Context {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastCtx
}

// ReleaseFixture is a release served by a local httptest server, with the
// pieces a test needs to assert against it.
type ReleaseFixture struct {
	Release      *selfupdate.Release
	Archive      []byte
	AssetName    string
	ChecksumName string
	SHA256       string
	BaseURL      string
}

// NewReleaseFixture spins up an httptest server serving an archive and its
// checksums file for the running platform, and returns the release that
// points at them alongside the fixture details. The server is closed by
// t.Cleanup.
func NewReleaseFixture(t testing.TB, project, version, binaryName string, body []byte) ReleaseFixture {
	t.Helper()

	archive := testutil.MakeTarGz(t, testutil.TarEntry{Name: binaryName, Body: body})
	assetName := testutil.CurrentAssetName(project, version)
	checksumName := fmt.Sprintf("%s_%s_checksums.txt", project, version)
	sum := testutil.SHA256Hex(archive)
	checksums := fmt.Sprintf("%s  %s\n", sum, assetName)

	mux := http.NewServeMux()
	mux.HandleFunc("/"+assetName, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(archive)
	})
	mux.HandleFunc("/"+checksumName, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(checksums))
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	return ReleaseFixture{
		Release: &selfupdate.Release{
			TagName: "v" + version,
			Name:    "v" + version,
			Body:    "the release notes",
			Assets: []selfupdate.ReleaseAsset{
				{Name: assetName, BrowserDownloadURL: srv.URL + "/" + assetName},
				{Name: checksumName, BrowserDownloadURL: srv.URL + "/" + checksumName},
			},
		},
		Archive:      archive,
		AssetName:    assetName,
		ChecksumName: checksumName,
		SHA256:       sum,
		BaseURL:      srv.URL,
	}
}

// QuietConfig returns a [selfupdate.Config] wired for hermetic tests: the
// given release source, a discarded log, a captured stdout, and the given
// install target. The returned buffer is Config.Stdout.
func QuietConfig(t testing.TB, src selfupdate.ReleaseSource, targetPath string) (selfupdate.Config, *bytes.Buffer) {
	t.Helper()

	var out bytes.Buffer
	return selfupdate.Config{
		Owner:          "acme",
		Repo:           "widget",
		BinaryName:     "widget",
		CurrentVersion: "v1.0.0",
		TargetPath:     targetPath,
		Source:         src,
		Stdout:         &out,
		Logger:         slog.New(slog.NewTextHandler(io.Discard, nil)),
	}, &out
}
