// Package notify implements the passive half of the update story: a
// cached "a new version is available" check and the banner that reports
// it. It never writes to the install path and never downloads a release
// — [github.com/mrz1836/go-selfupdate] does that, and only when the user
// asks for it.
//
// The split matters. A notice that fires on every invocation is a check
// against the GitHub API on every invocation, so the result is cached
// with a TTL and the whole thing is skipped under CI or when the user
// has opted out. A CLI must also never fail because its update check
// failed: [StartBackgroundCheck] swallows every error, including panics,
// and simply says nothing.
//
// Every seam the three donor implementations disagreed about — cache
// directory, TTL, opt-out variable, token variable, banner stream — is a
// [Config] field rather than a package-level constant or variable. That
// is a deliberate divergence: a library is imported by whoever wants it,
// so process-global mutable state would let one consumer's test reach
// into another consumer's cache. Adopting tools pass their historical
// cache directory through Config and keep the cache they already have.
package notify

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	selfupdate "github.com/mrz1836/go-selfupdate"
)

// defaultCheckTimeout bounds a single release-metadata lookup. A passive
// notice is worth a few seconds at most; past that the CLI has better
// things to do.
const defaultCheckTimeout = 10 * time.Second

// Notifier errors. A caller that only wants the banner can ignore these
// entirely — every entry point degrades to silence — but a caller
// wiring the notifier at startup will want to see a configuration
// mistake immediately.
var (
	// ErrIncompleteConfig means required Config fields were left empty.
	ErrIncompleteConfig = errors.New("go-selfupdate/notify: incomplete configuration")
	// ErrCacheUnavailable means the cache location could not be
	// resolved, which on a normal machine means the user config
	// directory is undiscoverable.
	ErrCacheUnavailable = errors.New("go-selfupdate/notify: cache directory unavailable")
)

// Config wires the passive notifier. Owner and Repo are required;
// everything else has a production default applied by normalize.
type Config struct {
	// Owner is the GitHub account or organization hosting the releases.
	Owner string
	// Repo is the GitHub repository name.
	Repo string
	// AppName identifies the tool in the banner and derives the default
	// cache directory and environment-variable prefix. Empty falls back
	// to BinaryName, then Repo.
	AppName string
	// BinaryName is the command users type. Empty falls back to
	// AppName.
	BinaryName string
	// CurrentVersion is the running build's version. A development
	// marker suppresses the background check entirely.
	CurrentVersion string
	// UpgradeCommand is the line the banner tells the user to run.
	// Empty yields "<binary> update".
	UpgradeCommand string

	// CacheDir holds the check-result cache. Empty resolves to
	// os.UserConfigDir()/<app>. Pass an existing location to keep a
	// cache the tool already maintains.
	CacheDir string
	// CacheFileName is the cache file's name within CacheDir. Empty
	// yields "update-check.json".
	CacheFileName string
	// CacheTTL is how long a recorded check is trusted. Zero consults
	// the interval environment variable, then falls back to 24h. Any
	// value is clamped to [1h, 720h] so a misconfiguration can neither
	// hammer the API nor pin a stale version forever.
	CacheTTL time.Duration

	// DisableEnvVar names the application-specific opt-out variable.
	// Empty yields <APP>_NO_UPDATE_CHECK. The shared NO_UPDATE_CHECK
	// and CI detection apply regardless.
	DisableEnvVar string
	// IntervalEnvVar names the variable holding a Go duration that
	// overrides CacheTTL. Empty yields <APP>_UPDATE_CHECK_INTERVAL.
	IntervalEnvVar string
	// TokenEnvVar names the application-specific GitHub token variable,
	// consulted before GITHUB_TOKEN and GH_TOKEN. Empty yields
	// <APP>_GITHUB_TOKEN.
	TokenEnvVar string

	// BannerOut receives the update notice. Nil means os.Stderr; a tool
	// whose output is read by humans on stdout may pass os.Stdout.
	BannerOut io.Writer
	// Style selects the banner's box-drawing characters. The zero value
	// picks per terminal.
	Style BannerStyle

	// Source resolves release metadata. Nil yields the shared
	// gh-CLI-first, REST-fallback source.
	Source selfupdate.ReleaseSource
	// Client fetches release metadata when the REST source is used. Nil
	// yields a client bounded by Timeout.
	Client *http.Client
	// Timeout bounds one metadata lookup. Zero yields 10s.
	Timeout time.Duration

	// Getenv reads environment variables. Nil means os.Getenv. It is a
	// test seam: injecting it here rather than mutating the process
	// environment keeps notifier tests parallel-safe.
	Getenv func(string) string
	// Now reports the current time. Nil means time.Now. It is the seam
	// that makes TTL expiry deterministic in tests.
	Now func() time.Time
}

// Result is the outcome of an update check. A Result with a non-nil Err
// is still returned rather than discarded, so an explicit caller can
// report why the check failed; the banner stays silent either way.
type Result struct {
	CurrentVersion  string
	LatestVersion   string
	UpdateAvailable bool
	CheckedAt       time.Time
	FromCache       bool
	Err             error
}

// normalize returns a copy of c with production defaults filled in. The
// caller's Config is never mutated.
func (c Config) normalize() (Config, error) {
	if c.Owner == "" || c.Repo == "" {
		return c, fmt.Errorf("%w: Owner and Repo are required", ErrIncompleteConfig)
	}

	if c.AppName == "" {
		c.AppName = c.BinaryName
	}
	if c.AppName == "" {
		c.AppName = c.Repo
	}
	if c.BinaryName == "" {
		c.BinaryName = c.AppName
	}
	if c.UpgradeCommand == "" {
		c.UpgradeCommand = c.BinaryName + " update"
	}

	if c.Getenv == nil {
		c.Getenv = os.Getenv
	}
	if c.Now == nil {
		c.Now = time.Now
	}
	if c.BannerOut == nil {
		c.BannerOut = os.Stderr
	}
	if c.Timeout <= 0 {
		c.Timeout = defaultCheckTimeout
	}
	if c.Client == nil {
		c.Client = &http.Client{Timeout: c.Timeout}
	}
	if c.CacheFileName == "" {
		c.CacheFileName = defaultCacheFileName
	}
	if c.CacheDir == "" {
		dir, err := os.UserConfigDir()
		if err != nil {
			return c, fmt.Errorf("%w: %w", ErrCacheUnavailable, err)
		}
		c.CacheDir = filepath.Join(dir, appSlug(c.AppName))
	}
	if c.Source == nil {
		c.Source = selfupdate.DefaultReleaseSource(c.Owner, c.Repo, nil, c.Client, "", resolveToken(c))
	}
	return c, nil
}

// envPrefix converts an application name into the SCREAMING_SNAKE_CASE
// prefix its environment variables use: "go-pre-commit" becomes
// "GO_PRE_COMMIT".
func envPrefix(app string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(app) {
		switch {
		case r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	return b.String()
}

// appSlug converts an application name into a filesystem-safe directory
// name.
func appSlug(app string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(app) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	slug := strings.Trim(b.String(), "-")
	if slug == "" {
		return "go-selfupdate"
	}
	return slug
}

// tokenEnvVar returns the application-specific token variable name.
func tokenEnvVar(cfg Config) string {
	if cfg.TokenEnvVar != "" {
		return cfg.TokenEnvVar
	}
	return envPrefix(cfg.AppName) + "_GITHUB_TOKEN"
}

// resolveToken returns the first non-empty GitHub token for cfg, applying
// the shared precedence: the application-specific variable, then
// GITHUB_TOKEN, then GH_TOKEN. The Config-specific derivation of the
// application variable stays here; the precedence itself lives in the root
// package so both halves of the library agree on it.
func resolveToken(cfg Config) string {
	return selfupdate.ResolveToken(cfg.Getenv, tokenEnvVar(cfg))
}

// Check reports whether a newer release exists, trusting a cache entry
// that is still within the TTL and otherwise fetching and recording one.
//
// It deliberately does not consult [IsDisabled]: an explicit call is the
// user asking. The opt-out governs the automatic path, which is
// [StartBackgroundCheck].
func Check(ctx context.Context, cfg Config) *Result {
	normalized, err := cfg.normalize()
	if err != nil {
		return &Result{CurrentVersion: cfg.CurrentVersion, Err: err}
	}

	if cached, rerr := ReadCache(normalized); rerr == nil && IsCacheValid(normalized, cached) {
		return &Result{
			CurrentVersion:  normalized.CurrentVersion,
			LatestVersion:   cached.LatestVersion,
			UpdateAvailable: selfupdate.IsNewer(normalized.CurrentVersion, cached.LatestVersion),
			CheckedAt:       cached.CheckedAt,
			FromCache:       true,
		}
	}
	return fetchAndCompare(ctx, normalized)
}

// CheckFresh forces a network lookup, bypassing a valid cache entry but
// still recording the result. It is what an explicit "check for updates"
// command should call.
func CheckFresh(ctx context.Context, cfg Config) *Result {
	normalized, err := cfg.normalize()
	if err != nil {
		return &Result{CurrentVersion: cfg.CurrentVersion, Err: err}
	}
	return fetchAndCompare(ctx, normalized)
}

// fetchAndCompare resolves the latest release, records it, and reports
// whether it outranks the running version. cfg must be normalized.
func fetchAndCompare(ctx context.Context, cfg Config) *Result {
	fetchCtx, cancel := context.WithTimeout(ctx, cfg.Timeout)
	defer cancel()

	release, err := cfg.Source.Latest(fetchCtx)
	if err != nil {
		return &Result{
			CurrentVersion: cfg.CurrentVersion,
			CheckedAt:      cfg.Now(),
			Err:            fmt.Errorf("go-selfupdate/notify: fetch latest release: %w", err),
		}
	}

	// A cache write failure is not worth surfacing: the check itself
	// succeeded, and the only cost is another lookup next time.
	_ = WriteCache(cfg, &CacheEntry{
		CurrentVersion: cfg.CurrentVersion,
		LatestVersion:  release.TagName,
	})

	return &Result{
		CurrentVersion:  cfg.CurrentVersion,
		LatestVersion:   release.TagName,
		UpdateAvailable: selfupdate.IsNewer(cfg.CurrentVersion, release.TagName),
		CheckedAt:       cfg.Now(),
	}
}

// StartBackgroundCheck runs [Check] in a goroutine and returns a
// buffered channel that yields at most one successful result before
// closing.
//
// It is the startup nudge, so it is built to be ignorable: the channel
// is buffered, so a caller that never reads it leaks nothing; a
// disabled check, a development build, an error, or a panic all produce
// silence rather than noise. Drain it late, with a short timeout, and
// hand what you get to [ShowBanner].
func StartBackgroundCheck(ctx context.Context, cfg Config) <-chan *Result {
	ch := make(chan *Result, 1)

	go func() {
		defer close(ch)
		// An update check must never be the reason a CLI dies.
		defer func() { _ = recover() }() //nolint:errcheck // recover's value is deliberately discarded

		if IsDisabled(cfg) || selfupdate.IsDevVersion(cfg.CurrentVersion) {
			return
		}
		if r := Check(ctx, cfg); r != nil && r.Err == nil {
			ch <- r
		}
	}()

	return ch
}
