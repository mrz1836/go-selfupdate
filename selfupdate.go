package selfupdate

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

// downloadTimeout bounds the archive transfer. Long enough for a
// multi-megabyte binary over a slow link, short enough to notice a
// stalled mirror.
const downloadTimeout = 5 * time.Minute

// goos is the operating system the install write-path gates on. It is a
// package variable rather than a direct reference to runtime.GOOS so a
// test can exercise the Windows gate on any host; production never
// changes it.
var goos = runtime.GOOS

// Config wires every external seam the updater depends on. Only Owner,
// Repo, and BinaryName are required; every other field has a production
// default applied by normalize.
type Config struct {
	// Owner is the GitHub account or organization hosting the releases.
	Owner string
	// Repo is the GitHub repository name.
	Repo string
	// BinaryName is the executable's name inside the release archive.
	BinaryName string
	// CurrentVersion is the version of the running build. Empty is
	// treated as a development build, which every real release outranks.
	CurrentVersion string
	// TargetPath is the binary to replace. Empty resolves to
	// os.Executable with symlinks followed.
	TargetPath string
	// Client fetches archives and checksum files. Nil yields a client
	// bounded by downloadTimeout.
	Client *http.Client
	// TokenEnvVar names an application-specific GitHub token variable,
	// consulted before GITHUB_TOKEN and GH_TOKEN.
	TokenEnvVar string
	// Source resolves release metadata. Nil yields the gh-CLI-first,
	// REST-fallback source.
	Source ReleaseSource
	// Platforms is the set of OS/arch pairs the caller publishes assets
	// for. Nil yields DefaultPlatforms. Narrow it when a tool ships
	// fewer, so an unsupported user gets a clear refusal before any
	// network call.
	Platforms []Platform
	// Stdout receives user-facing progress, including the version
	// transition line. Nil means os.Stdout.
	Stdout io.Writer
	// Logger receives diagnostics. Nil means slog.Default.
	Logger *slog.Logger
}

// normalize returns a copy of c with production defaults filled in. The
// caller's Config is never mutated.
func (c Config) normalize() (Config, error) {
	if c.Owner == "" || c.Repo == "" {
		return c, fmt.Errorf("%w: Owner and Repo are required", ErrIncompleteConfig)
	}
	if c.BinaryName == "" {
		return c, fmt.Errorf("%w: BinaryName is required", ErrIncompleteConfig)
	}

	if c.Client == nil {
		c.Client = &http.Client{Timeout: downloadTimeout}
	}
	if c.Source == nil {
		token := ResolveToken(os.Getenv, c.TokenEnvVar)
		c.Source = DefaultReleaseSource(c.Owner, c.Repo, nil, c.Client, "", token)
	}
	if len(c.Platforms) == 0 {
		c.Platforms = DefaultPlatforms()
	}
	if c.Stdout == nil {
		c.Stdout = os.Stdout
	}
	if c.Logger == nil {
		c.Logger = slog.Default()
	}
	if c.CurrentVersion == "" {
		c.CurrentVersion = devVersion
	}

	if c.TargetPath == "" {
		exe, err := resolveExecPath()
		if err != nil {
			return c, err
		}
		c.TargetPath = exe
	}
	return c, nil
}

// resolveExecPath returns the running binary's path with symlinks
// resolved, so the writability probe and the rename both act on the real
// file rather than on a link that points somewhere else entirely.
func resolveExecPath() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("go-selfupdate: resolve executable: %w", err)
	}
	if resolved, rerr := filepath.EvalSymlinks(exe); rerr == nil {
		exe = resolved
	}
	return exe, nil
}

// preflightInstall applies the location guards Install enforces before it
// writes: it refuses a binary another installer owns, then proves the
// install directory can be written. It returns the first blocking error —
// [ErrManagedInstall] or [ErrInstallDirNotWritable] — or nil when an
// in-place replace can proceed. targetPath must already be resolved.
func preflightInstall(targetPath string) error {
	if managed, reason := DetectManaged(targetPath); managed {
		return fmt.Errorf("%w: %s: %s", ErrManagedInstall, targetPath, reason)
	}
	return probeInstallDirWritable(targetPath)
}

// InstallPreflight reports whether an in-place self-update could proceed
// from the running binary's location, with no network access and no
// write. It resolves the target the same way [Install] does, then applies
// the same location guards, so a caller can warn a user before an update
// even exists that where the binary lives would block one — a check from a
// read-only directory otherwise reports "up to date" on a binary Install
// could never replace.
//
// A nil error means the location is fine. A non-nil error is the very one
// Install would return there: [ErrManagedInstall] for a binary another
// installer owns, or [ErrInstallDirNotWritable] for a directory the user
// cannot write. The platform gate and the release lookup are deliberately
// not run here; this answers only "can the new binary be written where the
// old one lives?".
func InstallPreflight(cfg Config) error {
	target := cfg.TargetPath
	if target == "" {
		exe, err := resolveExecPath()
		if err != nil {
			return err
		}
		target = exe
	}
	return preflightInstall(target)
}

// Info is the read-only result of a version check.
type Info struct {
	CurrentVersion  string `json:"current_version"`
	LatestVersion   string `json:"latest_version"`
	UpdateAvailable bool   `json:"update_available"`
	AssetName       string `json:"asset_name,omitempty"`
	DownloadURL     string `json:"download_url,omitempty"`
	ChecksumURL     string `json:"-"`
	ReleaseNotes    string `json:"release_notes,omitempty"`
	ReleaseURL      string `json:"release_url,omitempty"`
}

// Result reports what an Install call did.
type Result struct {
	// PreviousVersion is the version that was running before the call.
	PreviousVersion string
	// LatestVersion is the newest release found.
	LatestVersion string
	// Updated is true only when a new binary was installed.
	Updated bool
	// TargetPath is the binary that was replaced, when Updated is true.
	TargetPath string
	// AssetName is the release archive that was installed.
	AssetName string
	// ChecksumSHA256 is the verified digest of that archive.
	ChecksumSHA256 string
}

// installOptions holds the per-call switches set by [Option] values.
type installOptions struct {
	force     bool
	verbose   bool
	checkOnly bool
}

// Option adjusts a single Install call.
type Option func(*installOptions)

// WithForce installs the latest release even when it is not newer than
// the running version.
func WithForce() Option { return func(o *installOptions) { o.force = true } }

// WithVerbose narrates each step to Config.Stdout.
func WithVerbose() Option { return func(o *installOptions) { o.verbose = true } }

// WithCheckOnly reports what an install would do without writing
// anything.
func WithCheckOnly() Option { return func(o *installOptions) { o.checkOnly = true } }

// Check resolves the latest release and reports whether it is newer than
// the running version. It performs no writes.
//
// The platform guard runs first, before an HTTP client is constructed,
// so an unsupported OS/arch costs no network round-trip.
func Check(ctx context.Context, cfg Config) (*Info, error) {
	if err := guardPlatform(cfg.Platforms); err != nil {
		return nil, err
	}
	cfg, err := cfg.normalize()
	if err != nil {
		return nil, err
	}
	return checkNormalized(ctx, cfg)
}

// checkNormalized is Check's body, split out so Install can reuse it
// without normalizing twice.
//
// An absent platform asset is reported as an error but the populated
// Info is returned alongside it: a caller rendering `--check` output
// still wants to show which version exists, even when this platform has
// nothing to download.
func checkNormalized(ctx context.Context, cfg Config) (*Info, error) {
	release, err := cfg.Source.Latest(ctx)
	if err != nil {
		return nil, err
	}

	info := &Info{
		CurrentVersion:  cfg.CurrentVersion,
		LatestVersion:   release.TagName,
		UpdateAvailable: IsNewer(cfg.CurrentVersion, release.TagName),
		ReleaseNotes:    release.Body,
		ReleaseURL:      release.HTMLURL,
	}

	asset, checksumURL, err := selectAsset(release, cfg.Repo, release.TagName)
	info.ChecksumURL = checksumURL
	if err != nil {
		return info, err
	}
	info.AssetName = asset.Name
	info.DownloadURL = asset.BrowserDownloadURL

	return info, nil
}

// Install runs the full update pipeline: guard the platform, refuse a
// binary another installer owns, prove the install directory is
// writable, resolve the release, verify its checksum, extract it, and
// atomically replace the running binary.
//
// There is exactly one install route. Because nothing falls back when it
// fails, every stage returns a distinct sentinel wrapped with the
// concrete path or asset involved, so the error alone tells the user
// what to do next.
func Install(ctx context.Context, cfg Config, opts ...Option) (Result, error) {
	var o installOptions
	for _, opt := range opts {
		opt(&o)
	}

	if err := guardPlatform(cfg.Platforms); err != nil {
		return Result{}, err
	}
	cfg, err := cfg.normalize()
	if err != nil {
		return Result{}, err
	}

	// Windows self-update needs a rename-aside dance this library does not
	// implement yet, so the write path is gated before any network work —
	// the download is skipped entirely and the user is pointed at the
	// releases page. checkOnly still reports below, and Check plus the
	// passive banner are unaffected, because those never write.
	if goos == "windows" && !o.checkOnly {
		return Result{PreviousVersion: cfg.CurrentVersion},
			fmt.Errorf("%w; download the latest build from https://github.com/%s/%s/releases/latest",
				ErrWindowsNotSupported, cfg.Owner, cfg.Repo)
	}

	info, err := checkNormalized(ctx, cfg)
	if err != nil {
		return Result{PreviousVersion: cfg.CurrentVersion}, err
	}

	result := Result{
		PreviousVersion: cfg.CurrentVersion,
		LatestVersion:   info.LatestVersion,
		TargetPath:      cfg.TargetPath,
	}
	if o.checkOnly || (!info.UpdateAvailable && !o.force) {
		if !o.checkOnly {
			_, _ = fmt.Fprintf(cfg.Stdout, "%s is already up to date (%s)\n", cfg.BinaryName, cfg.CurrentVersion)
		}
		return result, nil
	}

	// A development build is never replaced without --force. Its version
	// is unknown, so every real release outranks it; overwriting the
	// binary a developer just built, without asking, is the wrong default.
	if !o.force && IsDevVersion(cfg.CurrentVersion) {
		_, _ = fmt.Fprintf(cfg.Stdout,
			"%s is a development build (%s); run with --force to install %s\n",
			cfg.BinaryName, cfg.CurrentVersion, info.LatestVersion)
		return result, nil
	}

	if preErr := preflightInstall(cfg.TargetPath); preErr != nil {
		return result, preErr
	}

	_, _ = fmt.Fprintf(cfg.Stdout, "Updating from %s to %s\n", cfg.CurrentVersion, info.LatestVersion)

	installed, err := downloadVerifyInstall(ctx, cfg, info, o)
	if err != nil {
		return result, err
	}

	result.Updated = true
	result.AssetName = info.AssetName
	result.ChecksumSHA256 = installed
	// Debug, not Info: the successful install is already reported to the user on
	// Stdout ("Updating from … / Updated … to …") and returned in Result, so an
	// Info line on the default logger only interleaves duplicate noise into a CLI's
	// output. Kept as a structured Debug record for a host that wires a debug logger.
	cfg.Logger.Debug("go-selfupdate: installed",
		"binary", cfg.BinaryName, "from", cfg.CurrentVersion, "to", info.LatestVersion, "path", cfg.TargetPath)
	return result, nil
}

// downloadVerifyInstall performs the write half of Install and returns
// the verified digest of the archive it installed.
func downloadVerifyInstall(ctx context.Context, cfg Config, info *Info, o installOptions) (string, error) {
	if info.ChecksumURL == "" {
		return "", fmt.Errorf("%w: %s", ErrChecksumMissing, info.LatestVersion)
	}

	digest, err := fetchChecksum(ctx, cfg.Client, info.ChecksumURL, info.AssetName)
	if err != nil {
		return "", err
	}
	verbosef(cfg, o, "verified checksum entry for %s\n", info.AssetName)

	workDir, err := os.MkdirTemp("", "go-selfupdate-*")
	if err != nil {
		return "", fmt.Errorf("go-selfupdate: create work dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(workDir) }()

	archivePath := filepath.Join(workDir, filepath.Base(info.AssetName))
	verbosef(cfg, o, "downloading %s\n", info.AssetName)
	if dlErr := downloadAndVerify(ctx, cfg.Client, info.DownloadURL, digest, archivePath); dlErr != nil {
		return "", dlErr
	}

	extractDir := filepath.Join(workDir, "extract")
	if mkErr := os.MkdirAll(extractDir, extractDirPerm); mkErr != nil {
		return "", fmt.Errorf("go-selfupdate: create extract dir: %w", mkErr)
	}
	if exErr := extractTarGz(archivePath, extractDir); exErr != nil {
		return "", exErr
	}

	binaryPath, err := locateBinary(extractDir, cfg.BinaryName)
	if err != nil {
		return "", err
	}
	if instErr := installBinary(binaryPath, cfg.TargetPath); instErr != nil {
		return "", instErr
	}
	return digest, nil
}

// verbosef writes a progress line only when the caller asked for one.
func verbosef(cfg Config, o installOptions, format string, args ...any) {
	if !o.verbose {
		return
	}
	_, _ = fmt.Fprintf(cfg.Stdout, format, args...)
}
