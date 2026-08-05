package notify

import (
	"errors"
	"testing"
	"time"

	selfupdate "github.com/mrz1836/go-selfupdate"
	"github.com/mrz1836/go-selfupdate/internal/testutil"
)

// errSource is a stand-in failure from a release lookup.
var errSource = errors.New("release lookup failed")

// frozen is the instant every time-sensitive test is anchored to, so a
// TTL assertion never depends on how long the suite took to run.
var frozen = time.Date(2026, time.March, 14, 12, 0, 0, 0, time.UTC)

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
		Getenv:         testutil.EnvMap(nil),
		Now:            func() time.Time { return frozen },
	}
}
