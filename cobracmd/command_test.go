package cobracmd

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	selfupdate "github.com/mrz1836/go-selfupdate"
	"github.com/mrz1836/go-selfupdate/notify"
	"github.com/spf13/cobra"
)

// errSourceUnavailable stands in for a release lookup that failed.
var errSourceUnavailable = errors.New("release source unavailable")

// stubSource is a ReleaseSource returning a fixed release, so no test
// touches the network.
type stubSource struct {
	release *selfupdate.Release
	err     error
}

// Latest returns the configured release or error.
func (s *stubSource) Latest(context.Context) (*selfupdate.Release, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.release, nil
}

// quietLogger discards the library's install diagnostics so a passing
// run's output stays readable.
func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// noEnv is a Getenv seam that reports every variable as unset.
//
// It is mandatory for every notifier test in this file: the real
// environment sets CI on a build agent, and IsDisabled treats CI as an
// opt-out — so a test reading the process environment would pass
// locally and assert nothing in CI.
func noEnv(string) string { return "" }

// makeTarGz builds a gzipped tarball holding one file.
func makeTarGz(t *testing.T, name string, body []byte) []byte {
	t.Helper()

	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)

	hdr := &tar.Header{
		Name:     name,
		Mode:     0o755,
		Size:     int64(len(body)),
		Typeflag: tar.TypeReg,
		ModTime:  time.Unix(0, 0),
	}
	if err := tw.WriteHeader(hdr); err != nil {
		t.Fatalf("write tar header: %v", err)
	}
	if _, err := tw.Write(body); err != nil {
		t.Fatalf("write tar body: %v", err)
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("close tar writer: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("close gzip writer: %v", err)
	}
	return buf.Bytes()
}

// releaseFixture serves a release archive and its checksums file, and
// returns the release pointing at them.
func releaseFixture(t *testing.T, repo, version, binaryName string, payload []byte) *selfupdate.Release {
	t.Helper()

	archive := makeTarGz(t, binaryName, payload)
	assetName := fmt.Sprintf("%s_%s_%s_%s.tar.gz", repo, version, runtime.GOOS, runtime.GOARCH)
	checksumName := fmt.Sprintf("%s_%s_checksums.txt", repo, version)
	sum := sha256.Sum256(archive)
	checksums := fmt.Sprintf("%s  %s\n", hex.EncodeToString(sum[:]), assetName)

	mux := http.NewServeMux()
	mux.HandleFunc("/"+assetName, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(archive)
	})
	mux.HandleFunc("/"+checksumName, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(checksums))
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	return &selfupdate.Release{
		TagName: "v" + version,
		Name:    "v" + version,
		Body:    "the release notes",
		Assets: []selfupdate.ReleaseAsset{
			{Name: assetName, BrowserDownloadURL: srv.URL + "/" + assetName},
			{Name: checksumName, BrowserDownloadURL: srv.URL + "/" + checksumName},
		},
	}
}

// execute runs cmd with args and returns everything it wrote.
func execute(t *testing.T, cmd *cobra.Command, args ...string) (string, error) {
	t.Helper()

	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), err
}

func TestNewRegistersCoreFlags(t *testing.T) {
	t.Parallel()

	cmd := New(selfupdate.Config{Owner: "acme", Repo: "widget", BinaryName: "widget"})

	for _, tc := range []struct {
		name      string
		shorthand string
	}{
		{flagForce, "f"},
		{flagCheck, ""},
		{flagVerbose, "v"},
	} {
		flag := cmd.Flags().Lookup(tc.name)
		if flag == nil {
			t.Fatalf("--%s is not registered", tc.name)
		}
		if flag.Shorthand != tc.shorthand {
			t.Errorf("--%s shorthand = %q, want %q", tc.name, flag.Shorthand, tc.shorthand)
		}
		if flag.Hidden {
			t.Errorf("--%s is hidden; the core flags are part of the documented UX", tc.name)
		}
	}

	if cmd.Flags().Lookup(flagUseBinary) != nil {
		t.Error("--use-binary is registered by default; it must be opt-in")
	}
}

func TestNewCommandNaming(t *testing.T) {
	t.Parallel()

	cfg := selfupdate.Config{Owner: "acme", Repo: "widget", BinaryName: "widget"}

	tests := []struct {
		name        string
		opts        []CmdOption
		wantUse     string
		wantAliases []string
	}{
		{"default", nil, "update", []string{"upgrade"}},
		{"upgrade", []CmdOption{WithUse("upgrade")}, "upgrade", []string{"update"}},
		{"custom", []CmdOption{WithUse("selfupdate")}, "selfupdate", nil},
		{"no aliases", []CmdOption{WithAliases()}, "update", nil},
		{"explicit aliases", []CmdOption{WithAliases("up")}, "update", []string{"up"}},
		{"empty use ignored", []CmdOption{WithUse("")}, "update", []string{"upgrade"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			cmd := New(cfg, tc.opts...)
			if cmd.Use != tc.wantUse {
				t.Errorf("Use = %q, want %q", cmd.Use, tc.wantUse)
			}
			if strings.Join(cmd.Aliases, ",") != strings.Join(tc.wantAliases, ",") {
				t.Errorf("Aliases = %v, want %v", cmd.Aliases, tc.wantAliases)
			}
		})
	}
}

func TestNewHelpTextOverrides(t *testing.T) {
	t.Parallel()

	cfg := selfupdate.Config{Owner: "acme", Repo: "widget", BinaryName: "widget"}

	derived := New(cfg)
	if !strings.Contains(derived.Short, "widget") {
		t.Errorf("derived Short = %q, want it to name the binary", derived.Short)
	}
	if !strings.Contains(derived.Long, "checksum") {
		t.Errorf("derived Long = %q, want it to mention checksum verification", derived.Long)
	}

	custom := New(cfg, WithShort("s"), WithLong("l"))
	if custom.Short != "s" || custom.Long != "l" {
		t.Errorf("overrides ignored: Short=%q Long=%q", custom.Short, custom.Long)
	}

	// A tool that never set BinaryName still gets a usable name.
	fallback := New(selfupdate.Config{Owner: "acme", Repo: "widget"})
	if !strings.Contains(fallback.Short, "widget") {
		t.Errorf("Short = %q, want the repo name as the fallback display name", fallback.Short)
	}
}

// TestCheckPerformsNoWrites is the regression pin for --check being a
// question rather than an instruction: an update is available and the
// command still leaves the target binary byte-for-byte untouched.
func TestCheckPerformsNoWrites(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	target := filepath.Join(dir, "widget")
	original := []byte("the running binary")
	if err := os.WriteFile(target, original, 0o755); err != nil { //nolint:gosec // a test fixture standing in for an executable
		t.Fatalf("seed target: %v", err)
	}
	before, err := os.Stat(target)
	if err != nil {
		t.Fatalf("stat target: %v", err)
	}

	cmd := New(selfupdate.Config{
		Owner:          "acme",
		Repo:           "widget",
		BinaryName:     "widget",
		CurrentVersion: "v1.0.0",
		TargetPath:     target,
		Source:         &stubSource{release: releaseFixture(t, "widget", "1.2.0", "widget", []byte("the new binary"))},
	})

	out, err := execute(t, cmd, "--check")
	if err != nil {
		t.Fatalf("execute --check: %v", err)
	}

	after, err := os.ReadFile(target) //nolint:gosec // path is this test's own temp file
	if err != nil {
		t.Fatalf("read target: %v", err)
	}
	if !bytes.Equal(after, original) {
		t.Errorf("--check rewrote the target binary: got %q, want %q", after, original)
	}
	afterStat, err := os.Stat(target)
	if err != nil {
		t.Fatalf("stat target: %v", err)
	}
	if !afterStat.ModTime().Equal(before.ModTime()) {
		t.Errorf("--check touched the target's mtime: %v -> %v", before.ModTime(), afterStat.ModTime())
	}

	if !strings.Contains(out, "v1.2.0") {
		t.Errorf("output = %q, want it to report the available version", out)
	}
	if strings.Contains(out, "the release notes") {
		t.Errorf("output = %q, want release notes withheld without --verbose", out)
	}
}

func TestCheckReportsUpToDate(t *testing.T) {
	t.Parallel()

	cmd := New(selfupdate.Config{
		Owner:          "acme",
		Repo:           "widget",
		BinaryName:     "widget",
		CurrentVersion: "v1.2.0",
		TargetPath:     filepath.Join(t.TempDir(), "widget"),
		Source:         &stubSource{release: releaseFixture(t, "widget", "1.2.0", "widget", []byte("same"))},
	})

	out, err := execute(t, cmd, "--check")
	if err != nil {
		t.Fatalf("execute --check: %v", err)
	}
	if !strings.Contains(out, "up to date") {
		t.Errorf("output = %q, want an up-to-date report", out)
	}
}

func TestCheckVerbosePrintsReleaseNotes(t *testing.T) {
	t.Parallel()

	cmd := New(selfupdate.Config{
		Owner:          "acme",
		Repo:           "widget",
		BinaryName:     "widget",
		CurrentVersion: "v1.0.0",
		TargetPath:     filepath.Join(t.TempDir(), "widget"),
		Source:         &stubSource{release: releaseFixture(t, "widget", "1.2.0", "widget", []byte("new"))},
	})

	out, err := execute(t, cmd, "--check", "--verbose")
	if err != nil {
		t.Fatalf("execute --check --verbose: %v", err)
	}
	if !strings.Contains(out, "the release notes") {
		t.Errorf("output = %q, want the release notes with --verbose", out)
	}
}

func TestCheckSurfacesSourceFailure(t *testing.T) {
	t.Parallel()

	cmd := New(selfupdate.Config{
		Owner:          "acme",
		Repo:           "widget",
		BinaryName:     "widget",
		CurrentVersion: "v1.0.0",
		TargetPath:     filepath.Join(t.TempDir(), "widget"),
		Source:         &stubSource{err: errSourceUnavailable},
	})
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true

	if _, err := execute(t, cmd, "--check"); !errors.Is(err, errSourceUnavailable) {
		t.Fatalf("error = %v, want the source failure surfaced to the caller", err)
	}
}

// TestUpdateInstallsRelease proves the drop-in actually drives the whole
// pipeline — resolve, verify, extract, replace — not merely that it
// registers flags.
func TestUpdateInstallsRelease(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	target := filepath.Join(dir, "widget")
	if err := os.WriteFile(target, []byte("old"), 0o755); err != nil { //nolint:gosec // a test fixture standing in for an executable
		t.Fatalf("seed target: %v", err)
	}

	cmd := New(selfupdate.Config{
		Owner:          "acme",
		Repo:           "widget",
		BinaryName:     "widget",
		CurrentVersion: "v1.0.0",
		TargetPath:     target,
		Source:         &stubSource{release: releaseFixture(t, "widget", "1.2.0", "widget", []byte("new"))},
		Logger:         quietLogger(),
	})

	out, err := execute(t, cmd)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}

	installed, err := os.ReadFile(target) //nolint:gosec // path is this test's own temp file
	if err != nil {
		t.Fatalf("read target: %v", err)
	}
	if string(installed) != "new" {
		t.Errorf("installed binary = %q, want the release payload", installed)
	}
	if !strings.Contains(out, "Upgrading from v1.0.0 to v1.2.0") {
		t.Errorf("output = %q, want the version transition line", out)
	}
	if !strings.Contains(out, "Updated widget to v1.2.0") {
		t.Errorf("output = %q, want the completion line", out)
	}
}

func TestUpdateAlreadyCurrentWithoutForce(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	target := filepath.Join(dir, "widget")
	if err := os.WriteFile(target, []byte("old"), 0o755); err != nil { //nolint:gosec // a test fixture standing in for an executable
		t.Fatalf("seed target: %v", err)
	}

	cfg := selfupdate.Config{
		Owner:          "acme",
		Repo:           "widget",
		BinaryName:     "widget",
		CurrentVersion: "v1.2.0",
		TargetPath:     target,
		Source:         &stubSource{release: releaseFixture(t, "widget", "1.2.0", "widget", []byte("new"))},
		Logger:         quietLogger(),
	}

	out, err := execute(t, New(cfg))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out, "already up to date") {
		t.Errorf("output = %q, want the no-op report", out)
	}
	current, err := os.ReadFile(target) //nolint:gosec // path is this test's own temp file
	if err != nil {
		t.Fatalf("read target: %v", err)
	}
	if string(current) != "old" {
		t.Errorf("target = %q, want it untouched without --force", current)
	}

	// --force installs the same version rather than declining.
	if _, err = execute(t, New(cfg), "--force"); err != nil {
		t.Fatalf("execute --force: %v", err)
	}
	forced, err := os.ReadFile(target) //nolint:gosec // path is this test's own temp file
	if err != nil {
		t.Fatalf("read target: %v", err)
	}
	if string(forced) != "new" {
		t.Errorf("target = %q, want --force to reinstall the release", forced)
	}
}

// TestDeprecatedUseBinaryFlagIsHiddenAndInert pins the compatibility
// contract: the flag is opt-in, absent from help, accepted without
// error, and changes nothing about what the command does.
func TestDeprecatedUseBinaryFlagIsHiddenAndInert(t *testing.T) {
	t.Parallel()

	newCmd := func() *cobra.Command {
		return New(selfupdate.Config{
			Owner:          "acme",
			Repo:           "widget",
			BinaryName:     "widget",
			CurrentVersion: "v1.0.0",
			TargetPath:     filepath.Join(t.TempDir(), "widget"),
			Source:         &stubSource{release: releaseFixture(t, "widget", "1.2.0", "widget", []byte("new"))},
		}, WithDeprecatedUseBinaryFlag())
	}

	flag := newCmd().Flags().Lookup(flagUseBinary)
	if flag == nil {
		t.Fatal("--use-binary is not registered when the option is passed")
	}
	if !flag.Hidden {
		t.Error("--use-binary is visible; a flag that selects nothing must not appear in help")
	}

	help, err := execute(t, newCmd(), "--help")
	if err != nil {
		t.Fatalf("execute --help: %v", err)
	}
	if strings.Contains(help, flagUseBinary) {
		t.Errorf("help output advertises --use-binary:\n%s", help)
	}

	withoutFlag, err := execute(t, newCmd(), "--check")
	if err != nil {
		t.Fatalf("execute --check: %v", err)
	}
	withFlag, err := execute(t, newCmd(), "--check", "--use-binary")
	if err != nil {
		t.Fatalf("execute --check --use-binary: %v", err)
	}
	if withFlag != withoutFlag {
		t.Errorf("--use-binary changed behavior:\nwith:    %q\nwithout: %q", withFlag, withoutFlag)
	}
}

func TestCommandRejectsPositionalArgs(t *testing.T) {
	t.Parallel()

	cmd := New(selfupdate.Config{Owner: "acme", Repo: "widget", BinaryName: "widget"})
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true

	if _, err := execute(t, cmd, "stray"); err == nil {
		t.Fatal("a positional argument was accepted; the command takes none")
	}
}

// bannerConfig returns a notifier config wired entirely to test seams:
// a stub source, a temp cache directory, and an environment that reports
// nothing set.
func bannerConfig(t *testing.T, out *bytes.Buffer, latest string) notify.Config {
	t.Helper()

	return notify.Config{
		Owner:          "acme",
		Repo:           "widget",
		BinaryName:     "widget",
		CurrentVersion: "v1.0.0",
		CacheDir:       t.TempDir(),
		BannerOut:      out,
		Style:          notify.BannerASCII,
		Getenv:         noEnv,
		Source:         &stubSource{release: &selfupdate.Release{TagName: latest}},
	}
}

func TestAttachBannerShowsNotice(t *testing.T) {
	t.Parallel()

	var banner bytes.Buffer
	root := &cobra.Command{
		Use: "widget",
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), "did the work")
			return nil
		},
	}
	AttachBanner(root, bannerConfig(t, &banner, "v1.2.0"))

	out, err := execute(t, root)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out, "did the work") {
		t.Errorf("output = %q, want the command's own output", out)
	}
	if !strings.Contains(banner.String(), "v1.2.0") {
		t.Errorf("banner = %q, want the available version", banner.String())
	}
}

func TestAttachBannerSilentWhenCurrent(t *testing.T) {
	t.Parallel()

	var banner bytes.Buffer
	root := &cobra.Command{Use: "widget", RunE: func(*cobra.Command, []string) error { return nil }}
	AttachBanner(root, bannerConfig(t, &banner, "v1.0.0"))

	if _, err := execute(t, root); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if banner.Len() != 0 {
		t.Errorf("banner = %q, want silence when the running version is current", banner.String())
	}
}

// TestAttachBannerSilentDuringUpdate pins the UX rule that a notice
// about the version you just installed is noise.
func TestAttachBannerSilentDuringUpdate(t *testing.T) {
	t.Parallel()

	var banner bytes.Buffer
	root := &cobra.Command{Use: "widget"}
	root.AddCommand(New(selfupdate.Config{
		Owner:          "acme",
		Repo:           "widget",
		BinaryName:     "widget",
		CurrentVersion: "v1.0.0",
		TargetPath:     filepath.Join(t.TempDir(), "widget"),
		Source:         &stubSource{release: releaseFixture(t, "widget", "1.2.0", "widget", []byte("new"))},
	}))
	AttachBanner(root, bannerConfig(t, &banner, "v1.2.0"))

	if _, err := execute(t, root, "update", "--check"); err != nil {
		t.Fatalf("execute update --check: %v", err)
	}
	if banner.Len() != 0 {
		t.Errorf("banner = %q, want silence during the update command itself", banner.String())
	}
}

func TestAttachBannerChainsExistingHooks(t *testing.T) {
	t.Parallel()

	var order []string
	var banner bytes.Buffer

	root := &cobra.Command{
		Use:              "widget",
		PersistentPreRun: func(*cobra.Command, []string) { order = append(order, "pre") },
		PersistentPostRunE: func(*cobra.Command, []string) error {
			order = append(order, "post")
			return nil
		},
		RunE: func(*cobra.Command, []string) error {
			order = append(order, "run")
			return nil
		},
	}
	AttachBanner(root, bannerConfig(t, &banner, "v1.2.0"))

	if _, err := execute(t, root); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if strings.Join(order, ",") != "pre,run,post" {
		t.Errorf("hook order = %v, want the caller's hooks preserved around the run", order)
	}
	if !strings.Contains(banner.String(), "v1.2.0") {
		t.Errorf("banner = %q, want the notice alongside the chained hooks", banner.String())
	}
}

func TestAttachBannerPropagatesChainedHookErrors(t *testing.T) {
	t.Parallel()

	var banner bytes.Buffer
	root := &cobra.Command{
		Use:               "widget",
		PersistentPreRunE: func(*cobra.Command, []string) error { return errSourceUnavailable },
		RunE:              func(*cobra.Command, []string) error { return nil },
		SilenceUsage:      true,
		SilenceErrors:     true,
	}

	// The check is left to fail: this command never reaches its
	// post-run, so the background goroutine outlives the command, and a
	// lookup that writes no cache entry keeps that from racing the
	// test's own temp-directory cleanup.
	cfg := bannerConfig(t, &banner, "v1.2.0")
	cfg.Source = &stubSource{err: errSourceUnavailable}
	AttachBanner(root, cfg)

	if _, err := execute(t, root); !errors.Is(err, errSourceUnavailable) {
		t.Fatalf("error = %v, want the chained pre-run failure surfaced", err)
	}
}

func TestAttachBannerNilRootIsNoOp(t *testing.T) {
	t.Parallel()

	// A nil root is a caller mistake that must not take the process
	// down over an update notice.
	AttachBanner(nil, notify.Config{Owner: "acme", Repo: "widget"})
}

func TestAttachBannerRespectsOptOut(t *testing.T) {
	t.Parallel()

	var banner bytes.Buffer
	cfg := bannerConfig(t, &banner, "v1.2.0")
	cfg.Getenv = func(name string) string {
		if name == "NO_UPDATE_CHECK" {
			return "1"
		}
		return ""
	}

	root := &cobra.Command{Use: "widget", RunE: func(*cobra.Command, []string) error { return nil }}
	AttachBanner(root, cfg)

	if _, err := execute(t, root); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if banner.Len() != 0 {
		t.Errorf("banner = %q, want silence when the user opted out", banner.String())
	}
}

func TestCommandContextFallsBackToBackground(t *testing.T) {
	t.Parallel()

	if commandContext(nil) == nil {
		t.Error("commandContext(nil) returned nil; callers pass the result straight to a lookup")
	}
	if commandContext(&cobra.Command{}) == nil {
		t.Error("commandContext returned nil for a command executed outside Execute")
	}
}

func TestBoolFlagNamesTheFlagOnFailure(t *testing.T) {
	t.Parallel()

	_, err := boolFlag(&cobra.Command{}, flagForce)
	if err == nil {
		t.Fatal("reading an unregistered flag succeeded")
	}
	if !strings.Contains(err.Error(), "--"+flagForce) {
		t.Errorf("error = %v, want it to name the flag", err)
	}
}
