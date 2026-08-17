package selfupdate

import "errors"

// Sentinel error catalog. Every documented failure mode maps to exactly
// one sentinel so callers can branch with errors.Is. Sentinel messages
// are static category strings — the concrete path, asset name, or status
// code is appended by the wrapping site, never baked into the sentinel.
//
// This catalog carries more weight here than it does in a tool with a
// fallback install method. Release-binary install is the only route this
// library offers, so when it fails there is no second attempt to paper
// over a vague message: the error IS the user experience. Every stage
// therefore returns its own sentinel rather than a generic failure.
var (
	// Release-lookup errors.
	ErrNoReleasesFound = errors.New("go-selfupdate: no releases found")
	ErrGHCLIFailed     = errors.New("go-selfupdate: gh CLI command failed")
	ErrGitHubAPIFailed = errors.New("go-selfupdate: GitHub API request failed")

	// Asset / platform errors.
	ErrAssetNotFound       = errors.New("go-selfupdate: no matching release asset for platform")
	ErrUnsupportedPlatform = errors.New("go-selfupdate: unsupported platform")
	ErrBinaryNotFound      = errors.New("go-selfupdate: binary not found in extracted files")

	// ErrWindowsNotSupported is returned when Install runs on Windows,
	// where replacing the running .exe needs a rename-aside dance this
	// library does not yet implement. Read-only paths (Check and the
	// passive banner) work on Windows; only the write path is gated, and
	// the wrapped message points the user at the releases page. Support is
	// planned, so this is a "not yet" rather than a permanent refusal.
	ErrWindowsNotSupported = errors.New("go-selfupdate: self-update is not available on Windows yet")

	// Download / network errors.
	ErrDownloadFailed = errors.New("go-selfupdate: download failed")

	// Checksum errors. A release without a usable checksum is a hard
	// failure, never a warn-and-proceed: an unverified binary is exactly
	// what this library exists to stop shipping.
	ErrChecksumFetchFailed = errors.New("go-selfupdate: failed to fetch checksums file")
	ErrChecksumNotFound    = errors.New("go-selfupdate: checksum not found in checksums file")
	ErrChecksumMismatch    = errors.New("go-selfupdate: checksum verification failed")
	ErrChecksumMissing     = errors.New("go-selfupdate: release has no checksums file; refusing to install unverified binary")

	// Extract errors. ErrPathTraversal and ErrFileTooLarge classify the two
	// security refusals; ErrExtractFailed classifies every other archive
	// read or write failure, so a caller can tell a corrupt or unreadable
	// archive apart from a hostile one.
	ErrPathTraversal = errors.New("go-selfupdate: path traversal attempt detected")
	ErrFileTooLarge  = errors.New("go-selfupdate: extracted file exceeds maximum allowed size")
	ErrExtractFailed = errors.New("go-selfupdate: failed to extract release archive")

	// Install errors. ErrInstallDirNotWritable and ErrManagedInstall are
	// the preflight refusals; ErrInstallFailed classifies a failure while
	// staging or renaming the new binary into place.
	ErrInstallDirNotWritable = errors.New("go-selfupdate: install dir not writable")
	ErrManagedInstall        = errors.New("go-selfupdate: binary is managed by another installer; refusing to replace it")
	ErrInstallFailed         = errors.New("go-selfupdate: failed to install binary")

	// Configuration errors.
	ErrIncompleteConfig = errors.New("go-selfupdate: incomplete configuration")

	// errInvalidSemverTuple is returned by parseVersionTuple when the
	// input is not parseable as major.minor.patch. Kept package-private
	// because callers never match on it: Compare already collapses every
	// parse failure into a single conservative "not newer" outcome.
	errInvalidSemverTuple = errors.New("go-selfupdate: not a semver tuple")
)
