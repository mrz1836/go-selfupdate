package notify

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	selfupdate "github.com/mrz1836/go-selfupdate"
)

// errSource is a stand-in failure from a release lookup.
var errSource = errors.New("release lookup failed")

// frozen is the instant every time-sensitive test is anchored to, so a
// TTL assertion never depends on how long the suite took to run.
var frozen = time.Date(2026, time.March, 14, 12, 0, 0, 0, time.UTC)

// stubSource returns a fixed release, an error, or a panic, and counts
// how many times it was asked. The count is what proves a cache hit
// avoided the network rather than merely returning the right answer.
//
// It is mutex-guarded because StartBackgroundCheck calls Latest from its
// own goroutine while the test observes the counter — the very
// concurrency the notifier exists to provide.
type stubSource struct {
	tag    string
	err    error
	panics bool

	mu      sync.Mutex
	calls   int
	lastCtx context.Context //nolint:containedctx // recorded for a deadline assertion, never used to make a call
}

// Latest records the call and returns the configured result.
func (s *stubSource) Latest(ctx context.Context) (*selfupdate.Release, error) {
	s.mu.Lock()
	s.calls++
	s.lastCtx = ctx
	s.mu.Unlock()

	if s.panics {
		panic("stub source panic")
	}
	if s.err != nil {
		return nil, s.err
	}
	return &selfupdate.Release{TagName: s.tag}, nil
}

// callCount reports how many lookups the stub has served.
func (s *stubSource) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

// lastContext returns the context of the most recent lookup.
func (s *stubSource) lastContext() context.Context {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastCtx
}

// envMap returns a Getenv seam backed by a map, so a test never mutates
// the process environment and every test in this package can run in
// parallel.
func envMap(vars map[string]string) func(string) string {
	return func(key string) string { return vars[key] }
}

// testConfig returns a Config wired entirely to test seams: a temp cache
// directory, a frozen clock, an empty environment, and a stub source.
func testConfig(t *testing.T, source selfupdate.ReleaseSource) Config {
	t.Helper()

	return Config{
		Owner:          "mrz1836",
		Repo:           "widget",
		AppName:        "widget",
		BinaryName:     "widget",
		CurrentVersion: "v1.0.0",
		CacheDir:       t.TempDir(),
		Source:         source,
		Getenv:         envMap(nil),
		Now:            func() time.Time { return frozen },
	}
}
