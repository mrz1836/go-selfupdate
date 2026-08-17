package selfupdate

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// homebrewCellarDir is the directory component every Homebrew-installed
// binary lives under once its symlinks are resolved.
const homebrewCellarDir = "Cellar"

// DetectManaged reports whether the binary at path was placed there by
// another installer, and returns a message explaining what to run
// instead.
//
// This matters more than it looks. Release-binary install is the only
// route this library offers, so a user whose binary arrived through a
// package manager or a Go toolchain install has to be told that plainly
// — the alternative is overwriting a file some other tool believes it
// owns, leaving both broken. A clear refusal is the feature.
//
// Three provenance signals are checked, in order of confidence:
//
//   - A Homebrew Cellar path component. Homebrew installs into
//     <prefix>/Cellar/<formula>/<version>/bin and symlinks from
//     <prefix>/bin, so a resolved path is unambiguous.
//   - A GOBIN or GOPATH/bin prefix, meaning the binary came from a Go
//     toolchain install rather than a release archive.
//   - A target file with no owner-write bit, the shape of a binary
//     installed by a system package manager. Note this inspects the
//     *file*; whether the enclosing directory can be written — which is
//     what the rename actually needs — is proven separately by
//     probeInstallDirWritable, which reports its own error.
//
// Symlinks are resolved first so /opt/homebrew/bin/tool is recognized as
// the Cellar binary it points at.
func DetectManaged(path string) (bool, string) {
	return detectManaged(path, os.Getenv)
}

// detectManaged is DetectManaged with the environment injected, so the
// GOBIN and GOPATH branches are testable without mutating process state.
func detectManaged(path string, getenv func(string) string) (bool, string) {
	if path == "" {
		return false, ""
	}
	if getenv == nil {
		getenv = os.Getenv
	}

	resolved := path
	if r, err := filepath.EvalSymlinks(path); err == nil {
		resolved = r
	}
	if abs, err := filepath.Abs(resolved); err == nil {
		resolved = abs
	}

	name := filepath.Base(resolved)

	if isHomebrewPath(resolved) {
		return true, "installed via Homebrew; upgrade it with `brew upgrade " + name + "` instead"
	}

	if dir, ok := isGoBinPath(resolved, getenv); ok {
		return true, "installed into the Go binary directory " + dir +
			"; reinstall it from a release archive to enable self-update"
	}

	if info, err := os.Stat(resolved); err == nil && info.Mode().Perm()&0o200 == 0 {
		return true, "the binary at " + resolved +
			" is not writable by the current user; it appears to be managed by a system package manager"
	}

	return false, ""
}

// isHomebrewPath reports whether a resolved path sits inside a Homebrew
// Cellar.
func isHomebrewPath(resolved string) bool {
	return slices.Contains(strings.Split(filepath.ToSlash(resolved), "/"), homebrewCellarDir)
}

// isGoBinPath reports whether a resolved path sits inside GOBIN or
// GOPATH/bin, returning the matching directory.
//
// GOPATH may list several directories; each contributes its own bin
// subdirectory, and the default when the variable is unset is
// $HOME/go/bin.
func isGoBinPath(resolved string, getenv func(string) string) (string, bool) {
	var candidates []string
	if gobin := strings.TrimSpace(getenv("GOBIN")); gobin != "" {
		candidates = append(candidates, gobin)
	}

	gopath := strings.TrimSpace(getenv("GOPATH"))
	if gopath == "" {
		if home, err := os.UserHomeDir(); err == nil {
			gopath = filepath.Join(home, "go")
		}
	}
	for _, entry := range filepath.SplitList(gopath) {
		if entry != "" {
			candidates = append(candidates, filepath.Join(entry, "bin"))
		}
	}

	// The binary path arrives with symlinks already resolved, so the
	// candidates have to be resolved too. Without this, a GOPATH under a
	// symlinked directory — /var/folders on macOS, a home directory
	// mounted through a link — never matches its own bin subdirectory.
	parent := realPath(filepath.Dir(resolved))
	for _, dir := range candidates {
		if candidate := realPath(dir); candidate != "" && candidate == parent {
			return candidate, true
		}
	}
	return "", false
}

// realPath returns dir as an absolute path with symlinks resolved,
// falling back to the cleaned absolute form when the path does not exist.
func realPath(dir string) string {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return ""
	}
	if resolved, rerr := filepath.EvalSymlinks(abs); rerr == nil {
		return resolved
	}
	return filepath.Clean(abs)
}
