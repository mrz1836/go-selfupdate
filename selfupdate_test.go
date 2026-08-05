package selfupdate_test

import (
	"bytes"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	selfupdate "github.com/mrz1836/go-selfupdate"
	"github.com/mrz1836/go-selfupdate/internal/testutil"
	"github.com/mrz1836/go-selfupdate/internal/updatetest"
)

// These tests drive the package through its exported surface only —
// Check, Install, and the options — exactly as a consuming CLI would. The
// three typed fixtures they lean on live in the updatetest package, and
// the two unexported hooks they need (the development-marker constant, the
// config normalizer) come from export_test.go. Nothing here dials a real
// network: every release lookup is an updatetest.StubSource and every
// asset URL points at a local httptest server torn down by t.Cleanup.

func TestCheckReportsAnAvailableUpdate(t *testing.T) {
	release := updatetest.NewReleaseFixture(t, "widget", "1.1.0", "widget", []byte("new binary")).Release
	cfg, _ := updatetest.QuietConfig(t, &updatetest.StubSource{Release: release}, filepath.Join(t.TempDir(), "widget"))

	info, err := selfupdate.Check(t.Context(), cfg)
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
	release := updatetest.NewReleaseFixture(t, "widget", "1.0.0", "widget", []byte("same binary")).Release
	cfg, _ := updatetest.QuietConfig(t, &updatetest.StubSource{Release: release}, filepath.Join(t.TempDir(), "widget"))

	info, err := selfupdate.Check(t.Context(), cfg)
	if err != nil {
		t.Fatalf("Check() = %v, want nil", err)
	}
	if info.UpdateAvailable {
		t.Errorf("UpdateAvailable = true, want false when running the latest version")
	}
}

func TestCheckPerformsNoWrites(t *testing.T) {
	dir := t.TempDir()
	target := testutil.WriteTempFile(t, dir, "widget", []byte("version one"), 0o755)

	before, err := os.Stat(target)
	if err != nil {
		t.Fatalf("stat target: %v", err)
	}

	release := updatetest.NewReleaseFixture(t, "widget", "9.9.9", "widget", []byte("a much newer binary")).Release
	cfg, _ := updatetest.QuietConfig(t, &updatetest.StubSource{Release: release}, target)

	if _, err := selfupdate.Check(t.Context(), cfg); err != nil {
		t.Fatalf("Check() = %v, want nil", err)
	}

	testutil.AssertFileContents(t, target, "version one")

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
	// had already gone out, so count the round-trips and the lookups.
	transport := &testutil.CountingTransport{}
	src := &updatetest.StubSource{Release: &selfupdate.Release{TagName: "v2.0.0"}}

	cfg := selfupdate.Config{
		Owner:          "acme",
		Repo:           "widget",
		BinaryName:     "widget",
		CurrentVersion: "v1.0.0",
		TargetPath:     filepath.Join(t.TempDir(), "widget"),
		Source:         src,
		Client:         &http.Client{Transport: transport},
		Platforms:      []selfupdate.Platform{{OS: "plan9", Arch: "mips"}},
		Stdout:         io.Discard,
	}

	if _, err := selfupdate.Check(t.Context(), cfg); !errors.Is(err, selfupdate.ErrUnsupportedPlatform) {
		t.Fatalf("Check() = %v, want ErrUnsupportedPlatform", err)
	}
	if _, err := selfupdate.Install(t.Context(), cfg); !errors.Is(err, selfupdate.ErrUnsupportedPlatform) {
		t.Fatalf("Install() = %v, want ErrUnsupportedPlatform", err)
	}
	if transport.Calls() != 0 {
		t.Errorf("the platform guard let %d HTTP round-trip(s) through", transport.Calls())
	}
	if src.Calls() != 0 {
		t.Errorf("the platform guard let %d release lookup(s) through", src.Calls())
	}
}

func TestCheckRejectsAnIncompleteConfig(t *testing.T) {
	tests := map[string]selfupdate.Config{
		"no owner":  {Repo: "widget", BinaryName: "widget"},
		"no repo":   {Owner: "acme", BinaryName: "widget"},
		"no binary": {Owner: "acme", Repo: "widget"},
	}

	for name, cfg := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := selfupdate.Check(t.Context(), cfg); !errors.Is(err, selfupdate.ErrIncompleteConfig) {
				t.Errorf("Check() = %v, want ErrIncompleteConfig", err)
			}
		})
	}
}

func TestCheckSurfacesAMissingAssetWithTheReleaseStillPopulated(t *testing.T) {
	release := &selfupdate.Release{
		TagName: "v2.0.0",
		Assets: []selfupdate.ReleaseAsset{
			{Name: "widget_2.0.0_plan9_mips.tar.gz", BrowserDownloadURL: "https://example.test/other"},
			{Name: "widget_2.0.0_checksums.txt", BrowserDownloadURL: "https://example.test/sums"},
		},
	}
	cfg, _ := updatetest.QuietConfig(t, &updatetest.StubSource{Release: release}, filepath.Join(t.TempDir(), "widget"))

	info, err := selfupdate.Check(t.Context(), cfg)
	if !errors.Is(err, selfupdate.ErrAssetNotFound) {
		t.Fatalf("Check() = %v, want ErrAssetNotFound", err)
	}
	if info == nil || info.LatestVersion != "v2.0.0" {
		t.Errorf("Check should still report the version it found: %+v", info)
	}
}

func TestCheckPropagatesASourceFailure(t *testing.T) {
	cfg, _ := updatetest.QuietConfig(t, &updatetest.StubSource{Err: selfupdate.ErrGitHubAPIFailed}, filepath.Join(t.TempDir(), "widget"))

	if _, err := selfupdate.Check(t.Context(), cfg); !errors.Is(err, selfupdate.ErrGitHubAPIFailed) {
		t.Fatalf("Check() = %v, want ErrGitHubAPIFailed", err)
	}
}

func TestInstallReplacesTheBinary(t *testing.T) {
	release := updatetest.NewReleaseFixture(t, "widget", "1.1.0", "widget", []byte("new binary")).Release
	target := testutil.WriteTempFile(t, t.TempDir(), "widget", []byte("old binary"), 0o755)
	cfg, out := updatetest.QuietConfig(t, &updatetest.StubSource{Release: release}, target)

	result, err := selfupdate.Install(t.Context(), cfg)
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

	testutil.AssertFileContents(t, target, "new binary")

	if !strings.Contains(out.String(), "Updating from v1.0.0 to v1.1.0") {
		t.Errorf("stdout %q is missing the version transition line", out)
	}
}

func TestInstallRefusesADevelopmentBuildWithoutForce(t *testing.T) {
	release := updatetest.NewReleaseFixture(t, "widget", "1.2.0", "widget", []byte("new binary")).Release
	target := testutil.WriteTempFile(t, t.TempDir(), "widget", []byte("dev binary"), 0o755)
	cfg, out := updatetest.QuietConfig(t, &updatetest.StubSource{Release: release}, target)
	cfg.CurrentVersion = selfupdate.DevVersion

	// Without --force, the developer's own build is left in place, with an
	// explanation rather than a silent overwrite.
	result, err := selfupdate.Install(t.Context(), cfg)
	if err != nil {
		t.Fatalf("Install() = %v, want nil", err)
	}
	if result.Updated {
		t.Error("a development build was replaced without --force")
	}
	testutil.AssertFileContents(t, target, "dev binary")
	if !strings.Contains(out.String(), "development build") {
		t.Errorf("stdout = %q, want it to explain the dev build was kept", out.String())
	}

	// --force installs over it deliberately.
	forced, err := selfupdate.Install(t.Context(), cfg, selfupdate.WithForce())
	if err != nil {
		t.Fatalf("Install(--force) = %v, want nil", err)
	}
	if !forced.Updated {
		t.Error("--force did not install over the development build")
	}
	testutil.AssertFileContents(t, target, "new binary")
}

func TestInstallSkipsWhenAlreadyCurrent(t *testing.T) {
	release := updatetest.NewReleaseFixture(t, "widget", "1.0.0", "widget", []byte("same binary")).Release
	target := testutil.WriteTempFile(t, t.TempDir(), "widget", []byte("old binary"), 0o755)
	cfg, out := updatetest.QuietConfig(t, &updatetest.StubSource{Release: release}, target)

	result, err := selfupdate.Install(t.Context(), cfg)
	if err != nil {
		t.Fatalf("Install() = %v, want nil", err)
	}
	if result.Updated {
		t.Error("Updated = true, want false when already on the latest version")
	}

	testutil.AssertFileContents(t, target, "old binary")
	if !strings.Contains(out.String(), "already up to date") {
		t.Errorf("stdout %q does not say the tool is current", out)
	}
}

func TestInstallForceReinstallsTheSameVersion(t *testing.T) {
	release := updatetest.NewReleaseFixture(t, "widget", "1.0.0", "widget", []byte("reinstalled binary")).Release
	target := testutil.WriteTempFile(t, t.TempDir(), "widget", []byte("old binary"), 0o755)
	cfg, _ := updatetest.QuietConfig(t, &updatetest.StubSource{Release: release}, target)

	result, err := selfupdate.Install(t.Context(), cfg, selfupdate.WithForce())
	if err != nil {
		t.Fatalf("Install() = %v, want nil", err)
	}
	if !result.Updated {
		t.Fatal("Updated = false, want true under WithForce")
	}

	testutil.AssertFileContents(t, target, "reinstalled binary")
}

func TestInstallCheckOnlyWritesNothing(t *testing.T) {
	release := updatetest.NewReleaseFixture(t, "widget", "9.9.9", "widget", []byte("new binary")).Release
	target := testutil.WriteTempFile(t, t.TempDir(), "widget", []byte("old binary"), 0o755)
	cfg, out := updatetest.QuietConfig(t, &updatetest.StubSource{Release: release}, target)

	result, err := selfupdate.Install(t.Context(), cfg, selfupdate.WithCheckOnly())
	if err != nil {
		t.Fatalf("Install() = %v, want nil", err)
	}
	if result.Updated {
		t.Error("Updated = true, want false under WithCheckOnly")
	}
	if result.LatestVersion != "v9.9.9" {
		t.Errorf("LatestVersion = %q, want v9.9.9", result.LatestVersion)
	}

	testutil.AssertFileContents(t, target, "old binary")
	if strings.Contains(out.String(), "Updating from") {
		t.Errorf("check-only announced an upgrade it did not perform: %q", out)
	}
}

func TestInstallVerboseNarratesTheSteps(t *testing.T) {
	release := updatetest.NewReleaseFixture(t, "widget", "1.1.0", "widget", []byte("new binary")).Release
	target := testutil.WriteTempFile(t, t.TempDir(), "widget", []byte("old binary"), 0o755)
	cfg, out := updatetest.QuietConfig(t, &updatetest.StubSource{Release: release}, target)

	if _, err := selfupdate.Install(t.Context(), cfg, selfupdate.WithVerbose()); err != nil {
		t.Fatalf("Install() = %v, want nil", err)
	}
	for _, want := range []string{"verified checksum entry", "downloading"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("verbose output %q is missing %q", out, want)
		}
	}
}

// TestInstallLogsTheSuccessAtDebugNotInfo pins the quiet-by-default contract: a
// completed install already reports itself on Stdout and in the returned Result,
// so it must not also emit an Info line on the default logger and interleave
// duplicate noise into a CLI's clean output. The structured record survives at
// Debug for a host that wires a debug logger.
func TestInstallLogsTheSuccessAtDebugNotInfo(t *testing.T) {
	release := updatetest.NewReleaseFixture(t, "widget", "1.1.0", "widget", []byte("new binary")).Release
	target := testutil.WriteTempFile(t, t.TempDir(), "widget", []byte("old binary"), 0o755)
	cfg, _ := updatetest.QuietConfig(t, &updatetest.StubSource{Release: release}, target)

	// At the default INFO threshold the install confirmation must not appear.
	var infoBuf bytes.Buffer
	cfg.Logger = slog.New(slog.NewTextHandler(&infoBuf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	if _, err := selfupdate.Install(t.Context(), cfg, selfupdate.WithForce()); err != nil {
		t.Fatalf("Install() = %v, want nil", err)
	}
	if strings.Contains(infoBuf.String(), "go-selfupdate: installed") {
		t.Errorf("install logged its confirmation at INFO: %q", infoBuf.String())
	}

	// A debug logger still receives the structured record, at DEBUG.
	var debugBuf bytes.Buffer
	cfg.Logger = slog.New(slog.NewTextHandler(&debugBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	if _, err := selfupdate.Install(t.Context(), cfg, selfupdate.WithForce()); err != nil {
		t.Fatalf("Install() = %v, want nil", err)
	}
	if !strings.Contains(debugBuf.String(), "go-selfupdate: installed") {
		t.Errorf("debug logger did not receive the install record: %q", debugBuf.String())
	}
	if !strings.Contains(debugBuf.String(), "level=DEBUG") {
		t.Errorf("install record was not emitted at DEBUG: %q", debugBuf.String())
	}
}

func TestInstallRefusesAManagedBinary(t *testing.T) {
	release := updatetest.NewReleaseFixture(t, "widget", "1.1.0", "widget", []byte("new binary")).Release

	gobin := t.TempDir()
	target := testutil.WriteTempFile(t, gobin, "widget", []byte("old binary"), 0o755)
	t.Setenv("GOBIN", gobin)

	cfg, _ := updatetest.QuietConfig(t, &updatetest.StubSource{Release: release}, target)

	_, err := selfupdate.Install(t.Context(), cfg)
	if !errors.Is(err, selfupdate.ErrManagedInstall) {
		t.Fatalf("Install() = %v, want ErrManagedInstall", err)
	}
	if !strings.Contains(err.Error(), target) {
		t.Errorf("error %q does not name the binary it refused to replace", err)
	}

	testutil.AssertFileContents(t, target, "old binary")
}

func TestInstallRefusesAnUnwritableInstallDir(t *testing.T) {
	testutil.SkipOnWindows(t)
	testutil.SkipIfRoot(t)

	release := updatetest.NewReleaseFixture(t, "widget", "1.1.0", "widget", []byte("new binary")).Release
	_, target := testutil.LockDir(t, t.TempDir())
	cfg, _ := updatetest.QuietConfig(t, &updatetest.StubSource{Release: release}, target)

	if _, err := selfupdate.Install(t.Context(), cfg); !errors.Is(err, selfupdate.ErrInstallDirNotWritable) {
		t.Fatalf("Install() = %v, want ErrInstallDirNotWritable", err)
	}
}

func TestInstallRejectsATamperedArchive(t *testing.T) {
	// The release advertises a checksums file whose digest does not match
	// what the asset URL actually serves — the shape of a compromised
	// mirror.
	archive := testutil.MakeTarGz(t, testutil.TarEntry{Name: "widget", Body: []byte("hostile binary")})
	assetName := testutil.CurrentAssetName("widget", "1.1.0")

	mux := http.NewServeMux()
	mux.HandleFunc("/asset", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(archive)
	})
	mux.HandleFunc("/sums", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat("00", 32) + "  " + assetName + "\n"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	release := &selfupdate.Release{
		TagName: "v1.1.0",
		Assets: []selfupdate.ReleaseAsset{
			{Name: assetName, BrowserDownloadURL: srv.URL + "/asset"},
			{Name: "widget_1.1.0_checksums.txt", BrowserDownloadURL: srv.URL + "/sums"},
		},
	}

	target := testutil.WriteTempFile(t, t.TempDir(), "widget", []byte("old binary"), 0o755)
	cfg, _ := updatetest.QuietConfig(t, &updatetest.StubSource{Release: release}, target)

	_, err := selfupdate.Install(t.Context(), cfg)
	if !errors.Is(err, selfupdate.ErrChecksumMismatch) {
		t.Fatalf("Install() = %v, want ErrChecksumMismatch", err)
	}

	testutil.AssertFileContents(t, target, "old binary")
}

func TestInstallRefusesAReleaseWithNoChecksums(t *testing.T) {
	assetName := testutil.CurrentAssetName("widget", "1.1.0")
	release := &selfupdate.Release{
		TagName: "v1.1.0",
		Assets:  []selfupdate.ReleaseAsset{{Name: assetName, BrowserDownloadURL: "https://example.test/asset"}},
	}

	target := testutil.WriteTempFile(t, t.TempDir(), "widget", []byte("old binary"), 0o755)
	cfg, _ := updatetest.QuietConfig(t, &updatetest.StubSource{Release: release}, target)

	if _, err := selfupdate.Install(t.Context(), cfg); !errors.Is(err, selfupdate.ErrChecksumMissing) {
		t.Fatalf("Install() = %v, want ErrChecksumMissing", err)
	}
}

func TestInstallReportsAnArchiveWithoutTheBinary(t *testing.T) {
	release := updatetest.NewReleaseFixture(t, "widget", "1.1.0", "something-else", []byte("wrong binary")).Release
	target := testutil.WriteTempFile(t, t.TempDir(), "widget", []byte("old binary"), 0o755)
	cfg, _ := updatetest.QuietConfig(t, &updatetest.StubSource{Release: release}, target)

	if _, err := selfupdate.Install(t.Context(), cfg); !errors.Is(err, selfupdate.ErrBinaryNotFound) {
		t.Fatalf("Install() = %v, want ErrBinaryNotFound", err)
	}
}

func TestConfigNormalizeFillsDefaults(t *testing.T) {
	cfg := selfupdate.Config{Owner: "acme", Repo: "widget", BinaryName: "widget", TargetPath: "/tmp/widget"}

	got, err := selfupdate.NormalizeConfig(cfg)
	if err != nil {
		t.Fatalf("normalize() = %v, want nil", err)
	}
	if got.Client == nil || got.Source == nil || got.Logger == nil {
		t.Error("normalize left a service seam nil")
	}
	if got.Stdout != os.Stdout {
		t.Error("normalize did not default Stdout to os.Stdout")
	}
	if got.CurrentVersion != selfupdate.DevVersion {
		t.Errorf("CurrentVersion = %q, want %q for an unstamped build", got.CurrentVersion, selfupdate.DevVersion)
	}
	if len(got.Platforms) == 0 {
		t.Error("normalize did not default the platform matrix")
	}
	if cfg.Client != nil || cfg.Source != nil {
		t.Error("normalize mutated the caller's Config")
	}
}

func TestConfigNormalizeResolvesTheRunningBinary(t *testing.T) {
	cfg := selfupdate.Config{Owner: "acme", Repo: "widget", BinaryName: "widget"}

	got, err := selfupdate.NormalizeConfig(cfg)
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

// TestInstallRefusesWindowsBeforeAnyNetwork pins the Windows write-gate:
// Install must fail with ErrWindowsNotSupported before it looks up a
// release or opens a socket, mirroring the platform-guard test above.
// SetGOOS mutates a package global, so this test runs serially.
func TestInstallRefusesWindowsBeforeAnyNetwork(t *testing.T) {
	restore := selfupdate.SetGOOS("windows")
	defer restore()

	transport := &testutil.CountingTransport{}
	stub := &updatetest.StubSource{Release: &selfupdate.Release{TagName: "v2.0.0"}}

	cfg := selfupdate.Config{
		Owner:          "acme",
		Repo:           "widget",
		BinaryName:     "widget",
		CurrentVersion: "v1.0.0",
		TargetPath:     filepath.Join(t.TempDir(), "widget"),
		Source:         stub,
		Client:         &http.Client{Transport: transport},
		// The real host platform, so the platform guard passes and the
		// Windows gate is what actually fires.
		Platforms: []selfupdate.Platform{selfupdate.CurrentPlatform()},
		Stdout:    io.Discard,
	}

	if _, err := selfupdate.Install(t.Context(), cfg); !errors.Is(err, selfupdate.ErrWindowsNotSupported) {
		t.Fatalf("Install() = %v, want ErrWindowsNotSupported", err)
	}
	if transport.Calls() != 0 {
		t.Errorf("the Windows gate let %d HTTP round-trip(s) through", transport.Calls())
	}
	if stub.Calls() != 0 {
		t.Errorf("the Windows gate let %d release lookup(s) through", stub.Calls())
	}
}

// TestInstallCheckOnlyStillReportsOnWindows proves the gate is a
// write-gate only: --check still resolves and reports the release on
// Windows without writing anything. Serial, because SetGOOS is global.
func TestInstallCheckOnlyStillReportsOnWindows(t *testing.T) {
	restore := selfupdate.SetGOOS("windows")
	defer restore()

	release := updatetest.NewReleaseFixture(t, "widget", "9.9.9", "widget", []byte("new binary")).Release
	target := testutil.WriteTempFile(t, t.TempDir(), "widget", []byte("old binary"), 0o755)
	cfg, _ := updatetest.QuietConfig(t, &updatetest.StubSource{Release: release}, target)

	result, err := selfupdate.Install(t.Context(), cfg, selfupdate.WithCheckOnly())
	if err != nil {
		t.Fatalf("Install(--check-only) on Windows = %v, want nil", err)
	}
	if result.Updated {
		t.Error("check-only reported an update as performed on Windows")
	}
	if result.LatestVersion != "v9.9.9" {
		t.Errorf("LatestVersion = %q, want v9.9.9", result.LatestVersion)
	}
	testutil.AssertFileContents(t, target, "old binary")
}
