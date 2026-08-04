package notify

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestIsDisabled(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		cfg  Config
		env  map[string]string
		want bool
	}{
		"enabled by default": {
			cfg: Config{AppName: "widget"}, env: nil, want: false,
		},
		"app variable set to 1": {
			cfg: Config{AppName: "widget"}, env: map[string]string{"WIDGET_NO_UPDATE_CHECK": "1"}, want: true,
		},
		"app variable set to true": {
			cfg: Config{AppName: "widget"}, env: map[string]string{"WIDGET_NO_UPDATE_CHECK": "true"}, want: true,
		},
		"app variable set to yes": {
			cfg: Config{AppName: "widget"}, env: map[string]string{"WIDGET_NO_UPDATE_CHECK": "YES"}, want: true,
		},
		"app variable set to 0": {
			cfg: Config{AppName: "widget"}, env: map[string]string{"WIDGET_NO_UPDATE_CHECK": "0"}, want: false,
		},
		"hyphenated app name becomes underscored prefix": {
			cfg: Config{AppName: "go-pre-commit"}, env: map[string]string{"GO_PRE_COMMIT_NO_UPDATE_CHECK": "1"}, want: true,
		},
		"shared variable silences every tool": {
			cfg: Config{AppName: "widget"}, env: map[string]string{"NO_UPDATE_CHECK": "1"}, want: true,
		},
		"explicit variable name honored": {
			cfg:  Config{AppName: "widget", DisableEnvVar: "LEGACY_SKIP_UPDATE"},
			env:  map[string]string{"LEGACY_SKIP_UPDATE": "true", "WIDGET_NO_UPDATE_CHECK": ""},
			want: true,
		},
		"CI true": {
			cfg: Config{AppName: "widget"}, env: map[string]string{"CI": "true"}, want: true,
		},
		"CI with a provider-specific value": {
			cfg: Config{AppName: "widget"}, env: map[string]string{"CI": "woodpecker"}, want: true,
		},
		"CI explicitly denied": {
			cfg: Config{AppName: "widget"}, env: map[string]string{"CI": "false"}, want: false,
		},
		"CI empty": {
			cfg: Config{AppName: "widget"}, env: map[string]string{"CI": ""}, want: false,
		},
		"identity falls back to binary name": {
			cfg: Config{BinaryName: "widget"}, env: map[string]string{"WIDGET_NO_UPDATE_CHECK": "1"}, want: true,
		},
		"identity falls back to repo": {
			cfg: Config{Repo: "widget"}, env: map[string]string{"WIDGET_NO_UPDATE_CHECK": "1"}, want: true,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			cfg := tc.cfg
			cfg.Getenv = envMap(tc.env)
			if got := IsDisabled(cfg); got != tc.want {
				t.Fatalf("IsDisabled() = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestIsDisabledNilGetenv proves the documented "callable before
// normalize" contract: a bare Config must not panic on the nil seam.
func TestIsDisabledNilGetenv(t *testing.T) {
	t.Setenv("WIDGET_NO_UPDATE_CHECK", "1")

	if !IsDisabled(Config{AppName: "widget"}) {
		t.Fatal("IsDisabled should read the real environment when Getenv is nil")
	}
}

func TestGetCheckInterval(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		cfg  Config
		env  map[string]string
		want time.Duration
	}{
		"default when nothing is set": {
			cfg: Config{AppName: "widget"}, want: defaultCheckInterval,
		},
		"explicit TTL wins": {
			cfg: Config{AppName: "widget", CacheTTL: 6 * time.Hour}, want: 6 * time.Hour,
		},
		"explicit TTL floored": {
			cfg: Config{AppName: "widget", CacheTTL: time.Minute}, want: minCheckInterval,
		},
		"explicit TTL capped": {
			cfg: Config{AppName: "widget", CacheTTL: 5000 * time.Hour}, want: maxCheckInterval,
		},
		"environment override": {
			cfg: Config{AppName: "widget"}, env: map[string]string{"WIDGET_UPDATE_CHECK_INTERVAL": "3h"}, want: 3 * time.Hour,
		},
		"environment override floored": {
			cfg: Config{AppName: "widget"}, env: map[string]string{"WIDGET_UPDATE_CHECK_INTERVAL": "5m"}, want: minCheckInterval,
		},
		"environment override capped": {
			cfg: Config{AppName: "widget"}, env: map[string]string{"WIDGET_UPDATE_CHECK_INTERVAL": "9000h"}, want: maxCheckInterval,
		},
		"unparseable override falls back": {
			cfg: Config{AppName: "widget"}, env: map[string]string{"WIDGET_UPDATE_CHECK_INTERVAL": "soon"}, want: defaultCheckInterval,
		},
		"negative override falls back": {
			cfg: Config{AppName: "widget"}, env: map[string]string{"WIDGET_UPDATE_CHECK_INTERVAL": "-4h"}, want: defaultCheckInterval,
		},
		"explicit variable name honored": {
			cfg:  Config{AppName: "widget", IntervalEnvVar: "LEGACY_INTERVAL"},
			env:  map[string]string{"LEGACY_INTERVAL": "2h"},
			want: 2 * time.Hour,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			cfg := tc.cfg
			cfg.Getenv = envMap(tc.env)
			if got := GetCheckInterval(cfg); got != tc.want {
				t.Fatalf("GetCheckInterval() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestCachePath(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	cfg := Config{AppName: "widget", CacheDir: dir}

	path, err := CachePath(cfg)
	if err != nil {
		t.Fatalf("CachePath: %v", err)
	}
	if want := filepath.Join(dir, defaultCacheFileName); path != want {
		t.Fatalf("CachePath() = %q, want %q", path, want)
	}

	cfg.CacheFileName = "notice.json"
	path, err = CachePath(cfg)
	if err != nil {
		t.Fatalf("CachePath: %v", err)
	}
	if want := filepath.Join(dir, "notice.json"); path != want {
		t.Fatalf("CachePath() = %q, want %q", path, want)
	}
}

// TestCachePathDefaultDir covers the derived location, including the
// slug that turns an app name into a directory name.
func TestCachePathDefaultDir(t *testing.T) {
	base := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", base)
	t.Setenv("HOME", base)

	path, err := CachePath(Config{AppName: "Widget CLI"})
	if err != nil {
		t.Fatalf("CachePath: %v", err)
	}
	if filepath.Base(path) != defaultCacheFileName {
		t.Fatalf("CachePath() = %q, want it to end in %q", path, defaultCacheFileName)
	}
	if got := filepath.Base(filepath.Dir(path)); got != "widget-cli" {
		t.Fatalf("cache directory = %q, want the slugged app name", got)
	}
}

// TestCachePathUnavailable proves an undiscoverable config directory
// surfaces as a typed error rather than a silent empty path.
func TestCachePathUnavailable(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("HOME", "")

	if _, err := CachePath(Config{AppName: "widget"}); err == nil || !errors.Is(err, ErrCacheUnavailable) {
		t.Fatalf("CachePath error = %v, want ErrCacheUnavailable", err)
	}
}

func TestReadCacheMissingIsNotAnError(t *testing.T) {
	t.Parallel()

	cfg := Config{AppName: "widget", CacheDir: t.TempDir()}
	entry, err := ReadCache(cfg)
	if err != nil {
		t.Fatalf("ReadCache: %v", err)
	}
	if entry != nil {
		t.Fatalf("ReadCache() = %+v, want nil for a first run", entry)
	}
}

func TestWriteReadCacheRoundTrip(t *testing.T) {
	t.Parallel()

	cfg := Config{
		AppName:  "widget",
		CacheDir: filepath.Join(t.TempDir(), "nested"),
		Now:      func() time.Time { return frozen },
	}

	if err := WriteCache(cfg, &CacheEntry{CurrentVersion: "v1.0.0", LatestVersion: "v1.2.0"}); err != nil {
		t.Fatalf("WriteCache: %v", err)
	}

	entry, err := ReadCache(cfg)
	if err != nil {
		t.Fatalf("ReadCache: %v", err)
	}
	if entry == nil {
		t.Fatal("ReadCache() = nil, want the entry just written")
	}
	if entry.LatestVersion != "v1.2.0" || entry.CurrentVersion != "v1.0.0" {
		t.Fatalf("entry = %+v, want the written versions", entry)
	}
	if !entry.CheckedAt.Equal(frozen) {
		t.Fatalf("CheckedAt = %v, want the injected clock %v", entry.CheckedAt, frozen)
	}
}

// TestWriteCacheIsAtomic checks that the staging file does not survive a
// successful write: a leftover .tmp is how a crashed writer poisons the
// next one.
func TestWriteCacheIsAtomic(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	cfg := Config{AppName: "widget", CacheDir: dir, Now: func() time.Time { return frozen }}

	if err := WriteCache(cfg, &CacheEntry{LatestVersion: "v2.0.0"}); err != nil {
		t.Fatalf("WriteCache: %v", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read cache dir: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != defaultCacheFileName {
		t.Fatalf("cache dir contains %v, want only %q", entries, defaultCacheFileName)
	}
}

// TestOrphanedTempFileIsCleaned covers the crashed-writer case: the
// stale staging file is removed on the next cache operation.
func TestOrphanedTempFileIsCleaned(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	cfg := Config{AppName: "widget", CacheDir: dir}

	orphan := filepath.Join(dir, defaultCacheFileName+tempSuffix)
	if err := os.WriteFile(orphan, []byte("{"), 0o600); err != nil {
		t.Fatalf("seed orphan: %v", err)
	}

	if _, err := ReadCache(cfg); err != nil {
		t.Fatalf("ReadCache: %v", err)
	}
	if _, err := os.Stat(orphan); !os.IsNotExist(err) {
		t.Fatalf("orphaned temp file still present (stat err = %v)", err)
	}
}

func TestReadCacheCorrupt(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	cfg := Config{AppName: "widget", CacheDir: dir}
	if err := os.WriteFile(filepath.Join(dir, defaultCacheFileName), []byte("not json"), 0o600); err != nil {
		t.Fatalf("seed cache: %v", err)
	}

	if _, err := ReadCache(cfg); err == nil {
		t.Fatal("ReadCache() = nil error, want a parse failure")
	}
}

// TestReadCacheUnreadable covers the non-ENOENT read failure: a
// directory where the cache file should be.
func TestReadCacheUnreadable(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	cfg := Config{AppName: "widget", CacheDir: dir}
	if err := os.Mkdir(filepath.Join(dir, defaultCacheFileName), 0o750); err != nil {
		t.Fatalf("seed directory: %v", err)
	}

	if _, err := ReadCache(cfg); err == nil {
		t.Fatal("ReadCache() = nil error, want a read failure")
	}
}

func TestWriteCacheNilEntry(t *testing.T) {
	t.Parallel()

	err := WriteCache(Config{AppName: "widget", CacheDir: t.TempDir()}, nil)
	if !errors.Is(err, ErrIncompleteConfig) {
		t.Fatalf("WriteCache(nil) error = %v, want ErrIncompleteConfig", err)
	}
}

// TestWriteCacheDirFailure covers the branch where the cache directory
// cannot be created because a file already occupies its path.
func TestWriteCacheDirFailure(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	blocker := filepath.Join(base, "blocked")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatalf("seed blocker: %v", err)
	}

	cfg := Config{AppName: "widget", CacheDir: filepath.Join(blocker, "cache")}
	if err := WriteCache(cfg, &CacheEntry{LatestVersion: "v1.0.0"}); err == nil {
		t.Fatal("WriteCache() = nil error, want a directory-creation failure")
	}
}

// TestWriteCacheRenameFailure covers the staging-file cleanup: when the
// rename cannot complete, the half-written temp file must not be left
// behind for the next read to trip over.
func TestWriteCacheRenameFailure(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	// A non-empty directory sitting where the cache file belongs cannot
	// be replaced by a rename.
	blocker := filepath.Join(dir, defaultCacheFileName)
	if err := os.Mkdir(blocker, 0o750); err != nil {
		t.Fatalf("seed blocker: %v", err)
	}
	if err := os.WriteFile(filepath.Join(blocker, "occupant"), []byte("x"), 0o600); err != nil {
		t.Fatalf("seed occupant: %v", err)
	}

	cfg := Config{AppName: "widget", CacheDir: dir, Now: func() time.Time { return frozen }}
	if err := WriteCache(cfg, &CacheEntry{LatestVersion: "v1.0.0"}); err == nil {
		t.Fatal("WriteCache() = nil error, want a rename failure")
	}
	if _, err := os.Stat(blocker + tempSuffix); !os.IsNotExist(err) {
		t.Fatalf("staging file survived a failed rename (stat err = %v)", err)
	}
}

// TestReadOnlyCacheDir covers the write and remove failures a
// non-writable cache directory produces — the shape a locked-down or
// root-owned config directory takes on a shared machine.
func TestReadOnlyCacheDir(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root, which bypasses directory permissions")
	}
	t.Parallel()

	dir := t.TempDir()
	cfg := Config{AppName: "widget", CacheDir: dir, Now: func() time.Time { return frozen }}

	if err := WriteCache(cfg, &CacheEntry{LatestVersion: "v1.0.0"}); err != nil {
		t.Fatalf("seed cache: %v", err)
	}
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatalf("chmod cache dir: %v", err)
	}
	// Restore write permission before the temp-dir cleanup tries to
	// remove the tree.
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	if err := WriteCache(cfg, &CacheEntry{LatestVersion: "v2.0.0"}); err == nil {
		t.Fatal("WriteCache() = nil error, want a write failure in a read-only directory")
	}
	if err := ClearCache(cfg); err == nil {
		t.Fatal("ClearCache() = nil error, want a remove failure in a read-only directory")
	}
}

func TestIsCacheValid(t *testing.T) {
	t.Parallel()

	cfg := Config{AppName: "widget", CacheDir: t.TempDir(), Now: func() time.Time { return frozen }}

	tests := map[string]struct {
		entry *CacheEntry
		want  bool
	}{
		"nil entry":          {entry: nil, want: false},
		"zero timestamp":     {entry: &CacheEntry{}, want: false},
		"just written":       {entry: &CacheEntry{CheckedAt: frozen}, want: true},
		"inside the TTL":     {entry: &CacheEntry{CheckedAt: frozen.Add(-23 * time.Hour)}, want: true},
		"exactly at the TTL": {entry: &CacheEntry{CheckedAt: frozen.Add(-24 * time.Hour)}, want: true},
		"expired":            {entry: &CacheEntry{CheckedAt: frozen.Add(-25 * time.Hour)}, want: false},
		"clock jumped back":  {entry: &CacheEntry{CheckedAt: frozen.Add(2 * time.Hour)}, want: true},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if got := IsCacheValid(cfg, tc.entry); got != tc.want {
				t.Fatalf("IsCacheValid(%+v) = %v, want %v", tc.entry, got, tc.want)
			}
		})
	}
}

// TestIsCacheValidHonorsInterval proves the TTL is the configured one,
// not a package constant.
func TestIsCacheValidHonorsInterval(t *testing.T) {
	t.Parallel()

	cfg := Config{
		AppName:  "widget",
		CacheDir: t.TempDir(),
		CacheTTL: 2 * time.Hour,
		Now:      func() time.Time { return frozen },
	}
	if IsCacheValid(cfg, &CacheEntry{CheckedAt: frozen.Add(-3 * time.Hour)}) {
		t.Fatal("a 3h-old entry must be stale under a 2h TTL")
	}
	if !IsCacheValid(cfg, &CacheEntry{CheckedAt: frozen.Add(-90 * time.Minute)}) {
		t.Fatal("a 90m-old entry must be fresh under a 2h TTL")
	}
}

// TestIsCacheValidDefaultClock covers the nil-Now fallback.
func TestIsCacheValidDefaultClock(t *testing.T) {
	t.Parallel()

	cfg := Config{AppName: "widget", CacheDir: t.TempDir()}
	if !IsCacheValid(cfg, &CacheEntry{CheckedAt: time.Now()}) {
		t.Fatal("a just-written entry must be valid against the real clock")
	}
}

func TestClearCache(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	cfg := Config{AppName: "widget", CacheDir: dir, Now: func() time.Time { return frozen }}

	if err := WriteCache(cfg, &CacheEntry{LatestVersion: "v1.0.0"}); err != nil {
		t.Fatalf("WriteCache: %v", err)
	}
	if err := ClearCache(cfg); err != nil {
		t.Fatalf("ClearCache: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, defaultCacheFileName)); !os.IsNotExist(err) {
		t.Fatalf("cache file still present (stat err = %v)", err)
	}

	// Removing an absent cache is a success, not an error.
	if err := ClearCache(cfg); err != nil {
		t.Fatalf("ClearCache on an absent file: %v", err)
	}
}

// TestCacheOperationsPropagatePathErrors proves every entry point
// reports an unresolvable cache location rather than silently doing
// nothing.
func TestCacheOperationsPropagatePathErrors(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("HOME", "")

	cfg := Config{AppName: "widget"}

	if _, err := ReadCache(cfg); !errors.Is(err, ErrCacheUnavailable) {
		t.Fatalf("ReadCache error = %v, want ErrCacheUnavailable", err)
	}
	if err := WriteCache(cfg, &CacheEntry{}); !errors.Is(err, ErrCacheUnavailable) {
		t.Fatalf("WriteCache error = %v, want ErrCacheUnavailable", err)
	}
	if err := ClearCache(cfg); !errors.Is(err, ErrCacheUnavailable) {
		t.Fatalf("ClearCache error = %v, want ErrCacheUnavailable", err)
	}
}

func TestTruthyAndIsCI(t *testing.T) {
	t.Parallel()

	truthyCases := map[string]bool{
		"1": true, "true": true, "TRUE": true, "yes": true, "on": true, " true ": true,
		"": false, "0": false, "false": false, "no": false, "maybe": false,
	}
	for input, want := range truthyCases {
		if got := truthy(input); got != want {
			t.Errorf("truthy(%q) = %v, want %v", input, got, want)
		}
	}

	ciCases := map[string]bool{
		"": false, "0": false, "false": false, "no": false, "off": false,
		"1": true, "true": true, "gitlab": true,
	}
	for input, want := range ciCases {
		if got := isCI(envMap(map[string]string{"CI": input})); got != want {
			t.Errorf("isCI(CI=%q) = %v, want %v", input, got, want)
		}
	}
}
