package selfupdate

import (
	"bytes"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// quietConfig returns a Config wired for hermetic tests: a stub release
// source, a discarded log, and a captured stdout.
func quietConfig(t *testing.T, src ReleaseSource, targetPath string) (Config, *bytes.Buffer) {
	t.Helper()

	var out bytes.Buffer
	return Config{
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

func TestCheckReportsAnAvailableUpdate(t *testing.T) {
	release, _ := releaseFixture(t, "widget", "1.1.0", "widget", []byte("new binary"))
	cfg, _ := quietConfig(t, &stubSource{release: release}, filepath.Join(t.TempDir(), "widget"))

	info, err := Check(t.Context(), cfg)
	if err != nil {
		t.Fatalf("Check() = %v, want nil", err)
	}
	if !info.UpdateAvailable {
		t.Errorf("UpdateAvailable = false, want true for %s over %s", info.LatestVersion, info.CurrentVersion)
	}
	if info.LatestVersion != "v1.1.0" {
		t.Errorf("LatestVersion = %q, want v1.1.0", info.LatestVersion)
	}
	if info.AssetName == "" || info.DownloadURL == "" || info.ChecksumURL == "" {
		t.Errorf("Check left the asset details empty: %+v", info)
	}
}

func TestCheckReportsNoUpdateWhenCurrent(t *testing.T) {
	release, _ := releaseFixture(t, "widget", "1.0.0", "widget", []byte("same binary"))
	cfg, _ := quietConfig(t, &stubSource{release: release}, filepath.Join(t.TempDir(), "widget"))

	info, err := Check(t.Context(), cfg)
	if err != nil {
		t.Fatalf("Check() = %v, want nil", err)
	}
	if info.UpdateAvailable {
		t.Errorf("UpdateAvailable = true, want false when running the latest version")
	}
}

func TestCheckPerformsNoWrites(t *testing.T) {
	dir := t.TempDir()
	target := writeTempFile(t, dir, "widget", []byte("version one"), 0o755)

	before, err := os.Stat(target)
	if err != nil {
		t.Fatalf("stat target: %v", err)
	}

	release, _ := releaseFixture(t, "widget", "9.9.9", "widget", []byte("a much newer binary"))
	cfg, _ := quietConfig(t, &stubSource{release: release}, target)

	if _, err := Check(t.Context(), cfg); err != nil {
		t.Fatalf("Check() = %v, want nil", err)
	}

	body, err := os.ReadFile(target) //nolint:gosec // test-controlled path
	if err != nil {
		t.Fatalf("read target: %v", err)
	}
	if string(body) != "version one" {
		t.Errorf("Check rewrote the target: %q", body)
	}

	after, err := os.Stat(target)
	if err != nil {
		t.Fatalf("stat target: %v", err)
	}
	if !after.ModTime().Equal(before.ModTime()) || after.Size() != before.Size() {
		t.Error("Check modified the target file")
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	if len(entries) != 1 {
		t.Errorf("Check left %d entries in the install directory, want 1", len(entries))
	}
}

func TestCheckRefusesAnUnsupportedPlatformWithoutNetwork(t *testing.T) {
	// The point of the guard is that an unsupported platform costs
	// nothing. Asserting only on the error would still pass if a request
	// had already gone out, so count the round-trips.
	transport := &countingTransport{}
	src := &stubSource{release: &Release{TagName: "v2.0.0"}}

	cfg := Config{
		Owner:          "acme",
		Repo:           "widget",
		BinaryName:     "widget",
		CurrentVersion: "v1.0.0",
		TargetPath:     filepath.Join(t.TempDir(), "widget"),
		Source:         src,
		Client:         &http.Client{Transport: transport},
		Platforms:      []Platform{{OS: "plan9", Arch: "mips"}},
		Stdout:         io.Discard,
	}

	if _, err := Check(t.Context(), cfg); !errors.Is(err, ErrUnsupportedPlatform) {
		t.Fatalf("Check() = %v, want ErrUnsupportedPlatform", err)
	}
	if _, err := Install(t.Context(), cfg); !errors.Is(err, ErrUnsupportedPlatform) {
		t.Fatalf("Install() = %v, want ErrUnsupportedPlatform", err)
	}
	if transport.calls != 0 {
		t.Errorf("the platform guard let %d HTTP round-trip(s) through", transport.calls)
	}
	if src.calls != 0 {
		t.Errorf("the platform guard let %d release lookup(s) through", src.calls)
	}
}

func TestCheckRejectsAnIncompleteConfig(t *testing.T) {
	tests := map[string]Config{
		"no owner":  {Repo: "widget", BinaryName: "widget"},
		"no repo":   {Owner: "acme", BinaryName: "widget"},
		"no binary": {Owner: "acme", Repo: "widget"},
	}

	for name, cfg := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := Check(t.Context(), cfg); !errors.Is(err, ErrIncompleteConfig) {
				t.Errorf("Check() = %v, want ErrIncompleteConfig", err)
			}
		})
	}
}

func TestCheckSurfacesAMissingAssetWithTheReleaseStillPopulated(t *testing.T) {
	release := &Release{
		TagName: "v2.0.0",
		Assets: []ReleaseAsset{
			{Name: "widget_2.0.0_plan9_mips.tar.gz", BrowserDownloadURL: "https://example.test/other"},
			{Name: "widget_2.0.0_checksums.txt", BrowserDownloadURL: "https://example.test/sums"},
		},
	}
	cfg, _ := quietConfig(t, &stubSource{release: release}, filepath.Join(t.TempDir(), "widget"))

	info, err := Check(t.Context(), cfg)
	if !errors.Is(err, ErrAssetNotFound) {
		t.Fatalf("Check() = %v, want ErrAssetNotFound", err)
	}
	if info == nil || info.LatestVersion != "v2.0.0" {
		t.Errorf("Check should still report the version it found: %+v", info)
	}
}

func TestCheckPropagatesASourceFailure(t *testing.T) {
	cfg, _ := quietConfig(t, &stubSource{err: ErrGitHubAPIFailed}, filepath.Join(t.TempDir(), "widget"))

	if _, err := Check(t.Context(), cfg); !errors.Is(err, ErrGitHubAPIFailed) {
		t.Fatalf("Check() = %v, want ErrGitHubAPIFailed", err)
	}
}

func TestInstallReplacesTheBinary(t *testing.T) {
	release, _ := releaseFixture(t, "widget", "1.1.0", "widget", []byte("new binary"))
	target := writeTempFile(t, t.TempDir(), "widget", []byte("old binary"), 0o755)
	cfg, out := quietConfig(t, &stubSource{release: release}, target)

	result, err := Install(t.Context(), cfg)
	if err != nil {
		t.Fatalf("Install() = %v, want nil", err)
	}
	if !result.Updated {
		t.Error("Updated = false, want true")
	}
	if result.PreviousVersion != "v1.0.0" || result.LatestVersion != "v1.1.0" {
		t.Errorf("version transition = %s -> %s, want v1.0.0 -> v1.1.0", result.PreviousVersion, result.LatestVersion)
	}
	if result.ChecksumSHA256 == "" {
		t.Error("Result did not record the verified digest")
	}

	body, err := os.ReadFile(target) //nolint:gosec // test-controlled path
	if err != nil {
		t.Fatalf("read target: %v", err)
	}
	if string(body) != "new binary" {
		t.Errorf("target = %q, want the new binary", body)
	}

	if !strings.Contains(out.String(), "Upgrading from v1.0.0 to v1.1.0") {
		t.Errorf("stdout %q is missing the version transition line", out)
	}
}

func TestInstallSkipsWhenAlreadyCurrent(t *testing.T) {
	release, _ := releaseFixture(t, "widget", "1.0.0", "widget", []byte("same binary"))
	target := writeTempFile(t, t.TempDir(), "widget", []byte("old binary"), 0o755)
	cfg, out := quietConfig(t, &stubSource{release: release}, target)

	result, err := Install(t.Context(), cfg)
	if err != nil {
		t.Fatalf("Install() = %v, want nil", err)
	}
	if result.Updated {
		t.Error("Updated = true, want false when already on the latest version")
	}

	body, err := os.ReadFile(target) //nolint:gosec // test-controlled path
	if err != nil {
		t.Fatalf("read target: %v", err)
	}
	if string(body) != "old binary" {
		t.Errorf("target = %q, want it left alone", body)
	}
	if !strings.Contains(out.String(), "already up to date") {
		t.Errorf("stdout %q does not say the tool is current", out)
	}
}

func TestInstallForceReinstallsTheSameVersion(t *testing.T) {
	release, _ := releaseFixture(t, "widget", "1.0.0", "widget", []byte("reinstalled binary"))
	target := writeTempFile(t, t.TempDir(), "widget", []byte("old binary"), 0o755)
	cfg, _ := quietConfig(t, &stubSource{release: release}, target)

	result, err := Install(t.Context(), cfg, WithForce())
	if err != nil {
		t.Fatalf("Install() = %v, want nil", err)
	}
	if !result.Updated {
		t.Fatal("Updated = false, want true under WithForce")
	}

	body, err := os.ReadFile(target) //nolint:gosec // test-controlled path
	if err != nil {
		t.Fatalf("read target: %v", err)
	}
	if string(body) != "reinstalled binary" {
		t.Errorf("target = %q, want the reinstalled binary", body)
	}
}

func TestInstallCheckOnlyWritesNothing(t *testing.T) {
	release, _ := releaseFixture(t, "widget", "9.9.9", "widget", []byte("new binary"))
	target := writeTempFile(t, t.TempDir(), "widget", []byte("old binary"), 0o755)
	cfg, out := quietConfig(t, &stubSource{release: release}, target)

	result, err := Install(t.Context(), cfg, WithCheckOnly())
	if err != nil {
		t.Fatalf("Install() = %v, want nil", err)
	}
	if result.Updated {
		t.Error("Updated = true, want false under WithCheckOnly")
	}
	if result.LatestVersion != "v9.9.9" {
		t.Errorf("LatestVersion = %q, want v9.9.9", result.LatestVersion)
	}

	body, err := os.ReadFile(target) //nolint:gosec // test-controlled path
	if err != nil {
		t.Fatalf("read target: %v", err)
	}
	if string(body) != "old binary" {
		t.Errorf("check-only rewrote the target: %q", body)
	}
	if strings.Contains(out.String(), "Upgrading from") {
		t.Errorf("check-only announced an upgrade it did not perform: %q", out)
	}
}

func TestInstallVerboseNarratesTheSteps(t *testing.T) {
	release, _ := releaseFixture(t, "widget", "1.1.0", "widget", []byte("new binary"))
	target := writeTempFile(t, t.TempDir(), "widget", []byte("old binary"), 0o755)
	cfg, out := quietConfig(t, &stubSource{release: release}, target)

	if _, err := Install(t.Context(), cfg, WithVerbose()); err != nil {
		t.Fatalf("Install() = %v, want nil", err)
	}
	for _, want := range []string{"verified checksum entry", "downloading"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("verbose output %q is missing %q", out, want)
		}
	}
}

func TestInstallRefusesAManagedBinary(t *testing.T) {
	release, _ := releaseFixture(t, "widget", "1.1.0", "widget", []byte("new binary"))

	gobin := t.TempDir()
	target := writeTempFile(t, gobin, "widget", []byte("old binary"), 0o755)
	t.Setenv("GOBIN", gobin)

	cfg, _ := quietConfig(t, &stubSource{release: release}, target)

	_, err := Install(t.Context(), cfg)
	if !errors.Is(err, ErrManagedInstall) {
		t.Fatalf("Install() = %v, want ErrManagedInstall", err)
	}
	if !strings.Contains(err.Error(), target) {
		t.Errorf("error %q does not name the binary it refused to replace", err)
	}

	body, readErr := os.ReadFile(target) //nolint:gosec // test-controlled path
	if readErr != nil {
		t.Fatalf("read target: %v", readErr)
	}
	if string(body) != "old binary" {
		t.Errorf("a refused install still modified the binary: %q", body)
	}
}

func TestInstallRefusesAnUnwritableInstallDir(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("directory mode bits do not gate creation on windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permission bits")
	}

	release, _ := releaseFixture(t, "widget", "1.1.0", "widget", []byte("new binary"))

	locked := filepath.Join(t.TempDir(), "locked")
	if err := os.Mkdir(locked, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	target := writeTempFile(t, locked, "widget", []byte("old binary"), 0o755)
	if err := os.Chmod(locked, 0o500); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o700) })

	cfg, _ := quietConfig(t, &stubSource{release: release}, target)

	if _, err := Install(t.Context(), cfg); !errors.Is(err, ErrInstallDirNotWritable) {
		t.Fatalf("Install() = %v, want ErrInstallDirNotWritable", err)
	}
}

func TestInstallRejectsATamperedArchive(t *testing.T) {
	// The release advertises a checksums file whose digest does not match
	// what the asset URL actually serves — the shape of a compromised
	// mirror.
	archive := makeTarGz(t, tarEntry{name: "widget", body: []byte("hostile binary")})
	assetName := currentAssetName("widget", "1.1.0")

	mux := http.NewServeMux()
	mux.HandleFunc("/asset", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(archive)
	})
	mux.HandleFunc("/sums", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat("00", 32) + "  " + assetName + "\n"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	release := &Release{
		TagName: "v1.1.0",
		Assets: []ReleaseAsset{
			{Name: assetName, BrowserDownloadURL: srv.URL + "/asset"},
			{Name: "widget_1.1.0_checksums.txt", BrowserDownloadURL: srv.URL + "/sums"},
		},
	}

	target := writeTempFile(t, t.TempDir(), "widget", []byte("old binary"), 0o755)
	cfg, _ := quietConfig(t, &stubSource{release: release}, target)

	_, err := Install(t.Context(), cfg)
	if !errors.Is(err, ErrChecksumMismatch) {
		t.Fatalf("Install() = %v, want ErrChecksumMismatch", err)
	}

	body, readErr := os.ReadFile(target) //nolint:gosec // test-controlled path
	if readErr != nil {
		t.Fatalf("read target: %v", readErr)
	}
	if string(body) != "old binary" {
		t.Errorf("a tampered archive reached the install path: %q", body)
	}
}

func TestInstallRefusesAReleaseWithNoChecksums(t *testing.T) {
	assetName := currentAssetName("widget", "1.1.0")
	release := &Release{
		TagName: "v1.1.0",
		Assets:  []ReleaseAsset{{Name: assetName, BrowserDownloadURL: "https://example.test/asset"}},
	}

	target := writeTempFile(t, t.TempDir(), "widget", []byte("old binary"), 0o755)
	cfg, _ := quietConfig(t, &stubSource{release: release}, target)

	if _, err := Install(t.Context(), cfg); !errors.Is(err, ErrChecksumMissing) {
		t.Fatalf("Install() = %v, want ErrChecksumMissing", err)
	}
}

func TestInstallReportsAnArchiveWithoutTheBinary(t *testing.T) {
	release, _ := releaseFixture(t, "widget", "1.1.0", "something-else", []byte("wrong binary"))
	target := writeTempFile(t, t.TempDir(), "widget", []byte("old binary"), 0o755)
	cfg, _ := quietConfig(t, &stubSource{release: release}, target)

	if _, err := Install(t.Context(), cfg); !errors.Is(err, ErrBinaryNotFound) {
		t.Fatalf("Install() = %v, want ErrBinaryNotFound", err)
	}
}

func TestConfigNormalizeFillsDefaults(t *testing.T) {
	cfg := Config{Owner: "acme", Repo: "widget", BinaryName: "widget", TargetPath: "/tmp/widget"}

	got, err := cfg.normalize()
	if err != nil {
		t.Fatalf("normalize() = %v, want nil", err)
	}
	if got.Client == nil || got.Source == nil || got.Logger == nil {
		t.Error("normalize left a service seam nil")
	}
	if got.Stdout != os.Stdout {
		t.Error("normalize did not default Stdout to os.Stdout")
	}
	if got.BannerOut != os.Stderr {
		t.Error("normalize did not default BannerOut to os.Stderr")
	}
	if got.CurrentVersion != devVersion {
		t.Errorf("CurrentVersion = %q, want %q for an unstamped build", got.CurrentVersion, devVersion)
	}
	if len(got.Platforms) == 0 {
		t.Error("normalize did not default the platform matrix")
	}
	if cfg.Client != nil || cfg.Source != nil {
		t.Error("normalize mutated the caller's Config")
	}
}

func TestConfigNormalizeResolvesTheRunningBinary(t *testing.T) {
	cfg := Config{Owner: "acme", Repo: "widget", BinaryName: "widget"}

	got, err := cfg.normalize()
	if err != nil {
		t.Fatalf("normalize() = %v, want nil", err)
	}
	if got.TargetPath == "" {
		t.Fatal("normalize left TargetPath empty")
	}
	if !filepath.IsAbs(got.TargetPath) {
		t.Errorf("TargetPath = %q, want an absolute path", got.TargetPath)
	}
}
