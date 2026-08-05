package notify

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestConfigNormalizeDefaults(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	cfg, err := Config{Owner: "mrz1836", Repo: "widget", CacheDir: dir, Getenv: envMap(nil)}.normalize()
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}

	if cfg.AppName != "widget" || cfg.BinaryName != "widget" {
		t.Fatalf("identity = %q/%q, want both derived from the repo name", cfg.AppName, cfg.BinaryName)
	}
	if cfg.UpgradeCommand != "widget update" {
		t.Fatalf("UpgradeCommand = %q, want the derived default", cfg.UpgradeCommand)
	}
	if cfg.Timeout != defaultCheckTimeout {
		t.Fatalf("Timeout = %v, want %v", cfg.Timeout, defaultCheckTimeout)
	}
	if cfg.Client == nil || cfg.Client.Timeout != defaultCheckTimeout {
		t.Fatalf("Client = %+v, want one bounded by the check timeout", cfg.Client)
	}
	if cfg.CacheFileName != defaultCacheFileName {
		t.Fatalf("CacheFileName = %q, want %q", cfg.CacheFileName, defaultCacheFileName)
	}
	if cfg.BannerOut != os.Stderr {
		t.Fatal("BannerOut should default to stderr")
	}
	if cfg.Source == nil {
		t.Fatal("Source should default to the shared gh-then-REST source")
	}
	if cfg.Now == nil || cfg.Getenv == nil {
		t.Fatal("the clock and environment seams should be filled in")
	}
}

// TestConfigNormalizeFillsEnvironmentSeam covers the production wiring,
// where no Getenv is injected and the real environment is read.
func TestConfigNormalizeFillsEnvironmentSeam(t *testing.T) {
	t.Setenv("WIDGET_GITHUB_TOKEN", "from-env")

	cfg, err := Config{Owner: "mrz1836", Repo: "widget", CacheDir: t.TempDir()}.normalize()
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if cfg.Getenv == nil {
		t.Fatal("Getenv should default to os.Getenv")
	}
	if got := cfg.Getenv("WIDGET_GITHUB_TOKEN"); got != "from-env" {
		t.Fatalf("Getenv returned %q, want the real environment value", got)
	}
}

// TestConfigNormalizePrefersExplicitFields proves nothing a caller sets
// is quietly replaced — the whole point of the Config seam.
func TestConfigNormalizePrefersExplicitFields(t *testing.T) {
	t.Parallel()

	client := &http.Client{Timeout: time.Second}
	source := &stubSource{tag: "v9.9.9"}
	dir := t.TempDir()

	cfg, err := Config{
		Owner:          "mrz1836",
		Repo:           "widget",
		AppName:        "Widget CLI",
		BinaryName:     "wg",
		UpgradeCommand: "brew upgrade wg",
		CacheDir:       dir,
		CacheFileName:  "notice.json",
		Timeout:        2 * time.Second,
		Client:         client,
		Source:         source,
		Getenv:         envMap(nil),
	}.normalize()
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}

	if cfg.AppName != "Widget CLI" || cfg.BinaryName != "wg" || cfg.UpgradeCommand != "brew upgrade wg" {
		t.Fatalf("identity fields were overwritten: %+v", cfg)
	}
	if cfg.Client != client || cfg.Source != source {
		t.Fatal("injected client and source should survive normalize")
	}
	if cfg.CacheDir != dir || cfg.CacheFileName != "notice.json" || cfg.Timeout != 2*time.Second {
		t.Fatalf("cache and timeout settings were overwritten: %+v", cfg)
	}
}

func TestConfigNormalizeRequiresOwnerAndRepo(t *testing.T) {
	t.Parallel()

	for name, cfg := range map[string]Config{
		"no owner": {Repo: "widget"},
		"no repo":  {Owner: "mrz1836"},
		"neither":  {},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if _, err := cfg.normalize(); !errors.Is(err, ErrIncompleteConfig) {
				t.Fatalf("normalize error = %v, want ErrIncompleteConfig", err)
			}
		})
	}
}

// TestConfigNormalizeCacheUnavailable covers the derived-cache-dir
// failure path.
func TestConfigNormalizeCacheUnavailable(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("HOME", "")

	_, err := Config{Owner: "mrz1836", Repo: "widget", Getenv: envMap(nil)}.normalize()
	if !errors.Is(err, ErrCacheUnavailable) {
		t.Fatalf("normalize error = %v, want ErrCacheUnavailable", err)
	}
}

// TestConfigNormalizeDerivesCacheDir proves the default location is the
// user config directory, keyed by the slugged app name.
func TestConfigNormalizeDerivesCacheDir(t *testing.T) {
	base := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", base)
	t.Setenv("HOME", base)

	cfg, err := Config{Owner: "mrz1836", Repo: "widget", Getenv: envMap(nil)}.normalize()
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if filepath.Base(cfg.CacheDir) != "widget" {
		t.Fatalf("CacheDir = %q, want it to end in the app slug", cfg.CacheDir)
	}
}

func TestResolveToken(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		cfg  Config
		env  map[string]string
		want string
	}{
		"app token wins": {
			cfg:  Config{AppName: "widget"},
			env:  map[string]string{"WIDGET_GITHUB_TOKEN": "app", "GITHUB_TOKEN": "generic", "GH_TOKEN": "cli"},
			want: "app",
		},
		"falls back to GITHUB_TOKEN": {
			cfg:  Config{AppName: "widget"},
			env:  map[string]string{"GITHUB_TOKEN": "generic", "GH_TOKEN": "cli"},
			want: "generic",
		},
		"falls back to GH_TOKEN": {
			cfg:  Config{AppName: "widget"},
			env:  map[string]string{"GH_TOKEN": "cli"},
			want: "cli",
		},
		"explicit variable name": {
			cfg:  Config{AppName: "widget", TokenEnvVar: "LEGACY_TOKEN"},
			env:  map[string]string{"LEGACY_TOKEN": "legacy", "WIDGET_GITHUB_TOKEN": "app"},
			want: "legacy",
		},
		"whitespace is trimmed": {
			cfg:  Config{AppName: "widget"},
			env:  map[string]string{"WIDGET_GITHUB_TOKEN": "  spaced  "},
			want: "spaced",
		},
		"blank values are skipped": {
			cfg:  Config{AppName: "widget"},
			env:  map[string]string{"WIDGET_GITHUB_TOKEN": "   ", "GITHUB_TOKEN": "generic"},
			want: "generic",
		},
		"nothing set": {
			cfg: Config{AppName: "widget"}, want: "",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			cfg := tc.cfg
			cfg.Getenv = envMap(tc.env)
			if got := resolveToken(cfg); got != tc.want {
				t.Fatalf("resolveToken() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestResolveTokenNilGetenv covers the fallback to the real environment.
func TestResolveTokenNilGetenv(t *testing.T) {
	t.Setenv("WIDGET_GITHUB_TOKEN", "from-env")

	if got := resolveToken(Config{AppName: "widget"}); got != "from-env" {
		t.Fatalf("resolveToken() = %q, want the real environment value", got)
	}
}

func TestEnvPrefixAndAppSlug(t *testing.T) {
	t.Parallel()

	prefixes := map[string]string{
		"widget":        "WIDGET",
		"go-pre-commit": "GO_PRE_COMMIT",
		"mage-x":        "MAGE_X",
		"Widget CLI":    "WIDGET_CLI",
		"go.invoice":    "GO_INVOICE",
	}
	for in, want := range prefixes {
		if got := envPrefix(in); got != want {
			t.Errorf("envPrefix(%q) = %q, want %q", in, got, want)
		}
	}

	slugs := map[string]string{
		"widget":        "widget",
		"go-pre-commit": "go-pre-commit",
		"Widget CLI":    "widget-cli",
		"go.invoice":    "go-invoice",
		"--":            "go-selfupdate",
		"":              "go-selfupdate",
	}
	for in, want := range slugs {
		if got := appSlug(in); got != want {
			t.Errorf("appSlug(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestCheckUsesValidCache is the assertion the whole cache exists for: a
// fresh entry must cost zero lookups, not merely return the right
// answer.
func TestCheckUsesValidCache(t *testing.T) {
	t.Parallel()

	source := &stubSource{tag: "v2.0.0"}
	cfg := testConfig(t, source)

	if err := WriteCache(cfg, &CacheEntry{CurrentVersion: "v1.0.0", LatestVersion: "v1.5.0"}); err != nil {
		t.Fatalf("seed cache: %v", err)
	}

	result := Check(t.Context(), cfg)
	if result.Err != nil {
		t.Fatalf("Check: %v", result.Err)
	}
	if source.callCount() != 0 {
		t.Fatalf("source was called %d times, want 0 for a valid cache", source.callCount())
	}
	if !result.FromCache {
		t.Fatal("result should be flagged as coming from the cache")
	}
	if result.LatestVersion != "v1.5.0" || !result.UpdateAvailable {
		t.Fatalf("result = %+v, want the cached v1.5.0 reported as newer", result)
	}
	if !result.CheckedAt.Equal(frozen) {
		t.Fatalf("CheckedAt = %v, want the cached stamp %v", result.CheckedAt, frozen)
	}
}

func TestCheckFetchesWhenCacheIsStale(t *testing.T) {
	t.Parallel()

	source := &stubSource{tag: "v2.0.0"}
	cfg := testConfig(t, source)

	// Seed an entry stamped a week ago by writing it with an older
	// clock, then read it back with the frozen one.
	stale := cfg
	stale.Now = func() time.Time { return frozen.Add(-7 * 24 * time.Hour) }
	if err := WriteCache(stale, &CacheEntry{CurrentVersion: "v1.0.0", LatestVersion: "v1.1.0"}); err != nil {
		t.Fatalf("seed cache: %v", err)
	}

	result := Check(t.Context(), cfg)
	if result.Err != nil {
		t.Fatalf("Check: %v", result.Err)
	}
	if source.callCount() != 1 {
		t.Fatalf("source was called %d times, want exactly 1", source.callCount())
	}
	if result.FromCache {
		t.Fatal("a stale cache must not be reported as a cache hit")
	}
	if result.LatestVersion != "v2.0.0" || !result.UpdateAvailable {
		t.Fatalf("result = %+v, want the fetched v2.0.0", result)
	}

	// The fresh answer must be recorded, so the next invocation is free.
	entry, err := ReadCache(cfg)
	if err != nil || entry == nil {
		t.Fatalf("ReadCache after fetch = %+v, %v", entry, err)
	}
	if entry.LatestVersion != "v2.0.0" || !entry.CheckedAt.Equal(frozen) {
		t.Fatalf("cache entry = %+v, want the fetched version stamped now", entry)
	}
}

func TestCheckFreshBypassesValidCache(t *testing.T) {
	t.Parallel()

	source := &stubSource{tag: "v3.0.0"}
	cfg := testConfig(t, source)

	if err := WriteCache(cfg, &CacheEntry{CurrentVersion: "v1.0.0", LatestVersion: "v1.5.0"}); err != nil {
		t.Fatalf("seed cache: %v", err)
	}

	result := CheckFresh(t.Context(), cfg)
	if result.Err != nil {
		t.Fatalf("CheckFresh: %v", result.Err)
	}
	if source.callCount() != 1 {
		t.Fatalf("source was called %d times, want exactly 1", source.callCount())
	}
	if result.FromCache || result.LatestVersion != "v3.0.0" {
		t.Fatalf("result = %+v, want a fresh v3.0.0", result)
	}
}

func TestCheckReportsSourceError(t *testing.T) {
	t.Parallel()

	source := &stubSource{err: errSource}
	result := Check(t.Context(), testConfig(t, source))

	if !errors.Is(result.Err, errSource) {
		t.Fatalf("Err = %v, want the source failure", result.Err)
	}
	if result.UpdateAvailable {
		t.Fatal("a failed check must never claim an update is available")
	}
}

func TestCheckReportsConfigError(t *testing.T) {
	t.Parallel()

	result := Check(t.Context(), Config{CurrentVersion: "v1.0.0"})
	if !errors.Is(result.Err, ErrIncompleteConfig) {
		t.Fatalf("Err = %v, want ErrIncompleteConfig", result.Err)
	}
	if result.CurrentVersion != "v1.0.0" {
		t.Fatalf("CurrentVersion = %q, want it echoed back", result.CurrentVersion)
	}

	fresh := CheckFresh(t.Context(), Config{CurrentVersion: "v1.0.0"})
	if !errors.Is(fresh.Err, ErrIncompleteConfig) {
		t.Fatalf("CheckFresh Err = %v, want ErrIncompleteConfig", fresh.Err)
	}
}

// TestCheckNotNewer covers an up-to-date install: a successful check
// that must not produce a banner.
func TestCheckNotNewer(t *testing.T) {
	t.Parallel()

	source := &stubSource{tag: "v1.0.0"}
	result := Check(t.Context(), testConfig(t, source))

	if result.Err != nil {
		t.Fatalf("Check: %v", result.Err)
	}
	if result.UpdateAvailable {
		t.Fatalf("result = %+v, want no update for an equal version", result)
	}
}

// TestCheckAppliesTimeout proves the per-lookup bound reaches the
// source, so a hung endpoint cannot stall a CLI's startup.
func TestCheckAppliesTimeout(t *testing.T) {
	t.Parallel()

	source := &stubSource{tag: "v2.0.0"}
	cfg := testConfig(t, source)
	cfg.Timeout = 50 * time.Millisecond

	if result := Check(t.Context(), cfg); result.Err != nil {
		t.Fatalf("Check: %v", result.Err)
	}
	deadline, ok := source.lastContext().Deadline()
	if !ok {
		t.Fatal("the source context carries no deadline")
	}
	if remaining := time.Until(deadline); remaining > 50*time.Millisecond {
		t.Fatalf("deadline is %v away, want the configured 50ms bound", remaining)
	}
}

func TestStartBackgroundCheckReportsAnUpdate(t *testing.T) {
	t.Parallel()

	source := &stubSource{tag: "v2.0.0"}
	result := drain(t, StartBackgroundCheck(t.Context(), testConfig(t, source)))

	if result == nil {
		t.Fatal("background check produced nothing, want a result")
	}
	if !result.UpdateAvailable || result.LatestVersion != "v2.0.0" {
		t.Fatalf("result = %+v, want v2.0.0 flagged as newer", result)
	}
}

// TestStartBackgroundCheckStaysSilent covers every way the automatic
// path must produce nothing at all. Silence is the contract: a CLI that
// prints an update-check error has made its own problem the user's.
func TestStartBackgroundCheckStaysSilent(t *testing.T) {
	t.Parallel()

	tests := map[string]func(cfg *Config, source *stubSource){
		"disabled by the app variable": func(cfg *Config, _ *stubSource) {
			cfg.Getenv = envMap(map[string]string{"WIDGET_NO_UPDATE_CHECK": "1"})
		},
		"disabled by the shared variable": func(cfg *Config, _ *stubSource) {
			cfg.Getenv = envMap(map[string]string{"NO_UPDATE_CHECK": "true"})
		},
		"disabled under CI": func(cfg *Config, _ *stubSource) {
			cfg.Getenv = envMap(map[string]string{"CI": "true"})
		},
		"development build": func(cfg *Config, _ *stubSource) {
			cfg.CurrentVersion = "dev"
		},
		"unversioned build": func(cfg *Config, _ *stubSource) {
			cfg.CurrentVersion = ""
		},
		"lookup failed": func(_ *Config, source *stubSource) {
			source.err = errSource
		},
		"source panicked": func(_ *Config, source *stubSource) {
			source.panics = true
		},
	}

	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			source := &stubSource{tag: "v2.0.0"}
			cfg := testConfig(t, source)
			mutate(&cfg, source)

			if result := drain(t, StartBackgroundCheck(t.Context(), cfg)); result != nil {
				t.Fatalf("background check produced %+v, want silence", result)
			}
		})
	}
}

// TestStartBackgroundCheckSkipsWorkWhenDisabled proves the opt-out is
// checked before the lookup, not after: the point is to avoid the API
// call, not to hide its result.
func TestStartBackgroundCheckSkipsWorkWhenDisabled(t *testing.T) {
	t.Parallel()

	source := &stubSource{tag: "v2.0.0"}
	cfg := testConfig(t, source)
	cfg.Getenv = envMap(map[string]string{"WIDGET_NO_UPDATE_CHECK": "1"})

	drain(t, StartBackgroundCheck(t.Context(), cfg))
	if source.callCount() != 0 {
		t.Fatalf("source was called %d times while disabled, want 0", source.callCount())
	}
}

// TestStartBackgroundCheckIsIgnorable proves the channel is buffered: a
// caller that never reads it must not pin the goroutine.
func TestStartBackgroundCheckIsIgnorable(t *testing.T) {
	t.Parallel()

	source := &stubSource{tag: "v2.0.0"}
	ch := StartBackgroundCheck(t.Context(), testConfig(t, source))

	// Nobody reads ch; the producing goroutine must still finish, which
	// it proves by closing after the buffered send.
	deadline := time.After(2 * time.Second)
	for source.callCount() == 0 {
		select {
		case <-deadline:
			t.Fatal("background check never ran")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	if got := drain(t, ch); got == nil {
		t.Fatal("the buffered result should still be readable later")
	}
}

func TestIsDevVersion(t *testing.T) {
	t.Parallel()

	cases := map[string]bool{
		"":       true,
		"dev":    true,
		"v":      true,
		"  dev ": true,
		"v1.0.0": false,
		"1.0.0":  false,
		"devel":  false,
	}
	for in, want := range cases {
		if got := isDevVersion(in); got != want {
			t.Errorf("isDevVersion(%q) = %v, want %v", in, got, want)
		}
	}
}

// TestNotifierWiringIsBrief backs the claim that a tool adopts the
// passive notice in a few lines: build a Config, start the check, show
// the banner.
func TestNotifierWiringIsBrief(t *testing.T) {
	t.Parallel()

	var out strings.Builder
	cfg := testConfig(t, &stubSource{tag: "v2.0.0"})
	cfg.BannerOut = &out
	cfg.Style = BannerASCII

	ShowBanner(cfg, drain(t, StartBackgroundCheck(t.Context(), cfg)))

	if !strings.Contains(out.String(), "A new version of WIDGET is available!") {
		t.Fatalf("banner = %q, want the update notice", out.String())
	}
}

// drain reads the single buffered result, or reports nil when the
// channel closes without one.
func drain(t *testing.T, ch <-chan *Result) *Result {
	t.Helper()

	select {
	case r := <-ch:
		return r
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the background check")
		return nil
	}
}
