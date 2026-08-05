package notify

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Cache and interval defaults.
const (
	// defaultCacheFileName is the cache file's name inside the cache
	// directory.
	defaultCacheFileName = "update-check.json"
	// defaultCheckInterval is how long a recorded check is trusted when
	// the caller expresses no preference.
	defaultCheckInterval = 24 * time.Hour
	// minCheckInterval floors the interval so a misconfiguration cannot
	// turn a passive notice into API abuse.
	minCheckInterval = 1 * time.Hour
	// maxCheckInterval caps the interval at 30 days, so an over-eager
	// value cannot pin a user to a stale version indefinitely.
	maxCheckInterval = 720 * time.Hour

	// cacheDirPerm and cacheFilePerm keep the cache owner-only. It
	// records nothing sensitive, but it lives in the user's config
	// directory and should not widen its neighbors' expectations.
	cacheDirPerm  = 0o700
	cacheFilePerm = 0o600

	// sharedDisableEnvVar is the cross-tool opt-out. Setting
	// NO_UPDATE_CHECK once silences every tool built on this library,
	// which is what someone who wants quiet actually means.
	sharedDisableEnvVar = "NO_UPDATE_CHECK"
	// disableEnvSuffix is appended to the application prefix to form the
	// per-tool opt-out, e.g. FLYWHEEL_NO_UPDATE_CHECK.
	disableEnvSuffix = "_NO_UPDATE_CHECK"
	// intervalEnvSuffix forms the per-tool interval override, e.g.
	// FLYWHEEL_UPDATE_CHECK_INTERVAL.
	intervalEnvSuffix = "_UPDATE_CHECK_INTERVAL"
)

// CacheEntry is the persisted result of the last update check.
type CacheEntry struct {
	CheckedAt      time.Time `json:"checked_at"`
	CurrentVersion string    `json:"current_version"`
	LatestVersion  string    `json:"latest_version"`
}

// IsDisabled reports whether update checking is turned off, by any of
// three routes: the application's own opt-out variable, the shared
// NO_UPDATE_CHECK variable, or a CI environment.
//
// CI matters most. A build agent that runs a CLI a thousand times is the
// worst possible client for a version check: nobody reads the banner and
// every invocation costs an API call.
func IsDisabled(cfg Config) bool {
	getenv := cfg.Getenv
	if getenv == nil {
		getenv = os.Getenv
	}

	if truthy(getenv(disableEnvVar(cfg))) {
		return true
	}
	if truthy(getenv(sharedDisableEnvVar)) {
		return true
	}
	return isCI(getenv)
}

// disableEnvVar returns the application-specific opt-out variable name.
func disableEnvVar(cfg Config) string {
	if cfg.DisableEnvVar != "" {
		return cfg.DisableEnvVar
	}
	return envPrefix(appNameOrRepo(cfg)) + disableEnvSuffix
}

// intervalEnvVar returns the application-specific interval override
// variable name.
func intervalEnvVar(cfg Config) string {
	if cfg.IntervalEnvVar != "" {
		return cfg.IntervalEnvVar
	}
	return envPrefix(appNameOrRepo(cfg)) + intervalEnvSuffix
}

// appNameOrRepo resolves the identity used to derive variable names,
// tolerating a Config that has not been normalized. IsDisabled is
// documented as callable before any other entry point, so it cannot
// assume normalize already ran.
func appNameOrRepo(cfg Config) string {
	switch {
	case cfg.AppName != "":
		return cfg.AppName
	case cfg.BinaryName != "":
		return cfg.BinaryName
	default:
		return cfg.Repo
	}
}

// truthy reports whether an environment value enables a flag. The
// accepted spellings cover what shells, CI providers, and humans
// actually write.
func truthy(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

// isCI reports whether the process looks like it is running on a build
// agent. Any non-empty CI value counts except an explicit denial, since
// providers spell the affirmative case a dozen ways but only ever spell
// the negative one as "false" or "0".
func isCI(getenv func(string) string) bool {
	v := strings.ToLower(strings.TrimSpace(getenv("CI")))
	switch v {
	case "", "0", "false", "no", "off":
		return false
	default:
		return true
	}
}

// GetCheckInterval returns how long a cached check is trusted, resolved
// from Config.CacheTTL, then the interval environment variable, then the
// 24h default. The result is always clamped to [1h, 720h].
func GetCheckInterval(cfg Config) time.Duration {
	if cfg.CacheTTL > 0 {
		return clampInterval(cfg.CacheTTL)
	}

	getenv := cfg.Getenv
	if getenv == nil {
		getenv = os.Getenv
	}
	raw := strings.TrimSpace(getenv(intervalEnvVar(cfg)))
	if raw == "" {
		return defaultCheckInterval
	}
	parsed, err := time.ParseDuration(raw)
	if err != nil || parsed <= 0 {
		// An unreadable override is a typo, not an instruction. Fall
		// back rather than failing a check nobody asked for.
		return defaultCheckInterval
	}
	return clampInterval(parsed)
}

// clampInterval bounds d to the permitted interval range.
func clampInterval(d time.Duration) time.Duration {
	return min(max(d, minCheckInterval), maxCheckInterval)
}

// CachePath returns the cache file's full path, resolving the default
// location when Config.CacheDir is empty.
func CachePath(cfg Config) (string, error) {
	dir := cfg.CacheDir
	if dir == "" {
		base, err := os.UserConfigDir()
		if err != nil {
			return "", fmt.Errorf("%w: %w", ErrCacheUnavailable, err)
		}
		dir = filepath.Join(base, appSlug(appNameOrRepo(cfg)))
	}
	name := cfg.CacheFileName
	if name == "" {
		name = defaultCacheFileName
	}
	return filepath.Join(dir, name), nil
}

// ReadCache returns the cached entry, or (nil, nil) when no cache file
// exists — a missing cache is the expected first-run state, not an
// error.
func ReadCache(cfg Config) (*CacheEntry, error) {
	path, err := CachePath(cfg)
	if err != nil {
		return nil, err
	}
	cleanupOrphanedTempFiles(path)

	data, err := os.ReadFile(path) //nolint:gosec // path is this application's own cache file
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil //nolint:nilnil // a missing cache is the "no entry" signal, not a failure
		}
		return nil, fmt.Errorf("go-selfupdate/notify: read cache %s: %w", path, err)
	}

	var entry CacheEntry
	if uerr := json.Unmarshal(data, &entry); uerr != nil {
		return nil, fmt.Errorf("go-selfupdate/notify: parse cache %s: %w", path, uerr)
	}
	return &entry, nil
}

// WriteCache persists entry atomically — temp file then rename — after
// stamping CheckedAt. A crash mid-write therefore leaves either the old
// entry or none, never a truncated file that the next read would reject.
func WriteCache(cfg Config, entry *CacheEntry) error {
	if entry == nil {
		return fmt.Errorf("%w: cache entry is nil", ErrIncompleteConfig)
	}

	path, err := CachePath(cfg)
	if err != nil {
		return err
	}
	cleanupOrphanedTempFiles(path)

	if mkErr := os.MkdirAll(filepath.Dir(path), cacheDirPerm); mkErr != nil {
		return fmt.Errorf("go-selfupdate/notify: create cache dir: %w", mkErr)
	}

	entry.CheckedAt = nowOrDefault(cfg)
	data, err := json.MarshalIndent(entry, "", "  ")
	if err != nil {
		return fmt.Errorf("go-selfupdate/notify: marshal cache: %w", err)
	}

	tmp := path + tempSuffix
	if werr := os.WriteFile(tmp, data, cacheFilePerm); werr != nil {
		return fmt.Errorf("go-selfupdate/notify: write cache temp: %w", werr)
	}
	if rerr := os.Rename(tmp, path); rerr != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("go-selfupdate/notify: rename cache: %w", rerr)
	}
	return nil
}

// IsCacheValid reports whether entry exists and is younger than the
// configured interval. A future-stamped entry — a clock that jumped
// backwards — is treated as valid rather than triggering a check storm.
func IsCacheValid(cfg Config, entry *CacheEntry) bool {
	if entry == nil || entry.CheckedAt.IsZero() {
		return false
	}
	return nowOrDefault(cfg).Sub(entry.CheckedAt) <= GetCheckInterval(cfg)
}

// ClearCache removes the cache file. Removing an absent file is a
// success.
func ClearCache(cfg Config) error {
	path, err := CachePath(cfg)
	if err != nil {
		return err
	}
	cleanupOrphanedTempFiles(path)

	if rerr := os.Remove(path); rerr != nil && !os.IsNotExist(rerr) {
		return fmt.Errorf("go-selfupdate/notify: remove cache: %w", rerr)
	}
	return nil
}

// tempSuffix is appended to the cache path for the atomic-write staging
// file.
const tempSuffix = ".tmp"

// cleanupOrphanedTempFiles removes a staging file left behind by a
// crashed write. It is best-effort: failing to clean up a stray temp
// file must never break the read or write that follows.
func cleanupOrphanedTempFiles(path string) {
	_ = os.Remove(path + tempSuffix)
}

// nowOrDefault returns the configured clock, or the real one.
func nowOrDefault(cfg Config) time.Time {
	if cfg.Now != nil {
		return cfg.Now()
	}
	return time.Now()
}
