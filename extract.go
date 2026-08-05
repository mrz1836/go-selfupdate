package selfupdate

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// maxExtractBytes is the default ceiling applied to extraction, both per
// file and in aggregate. The binaries this library installs are tens of
// megabytes; 500 MiB clears realistic growth by an order of magnitude
// while staying far below the zip-bomb danger zone.
const maxExtractBytes int64 = 500 << 20

// extractDirPerm is the mode for directories created during extraction.
// Owner-only: the extract directory is a private staging area, and a
// partial extract must never widen access to what it holds.
const extractDirPerm os.FileMode = 0o700

// safeJoin resolves a tar entry name against destDir, rejecting anything
// that would escape it. This is the Zip-Slip boundary.
//
// The guard is a prefix check on the *cleaned* target: once
// filepath.Clean has resolved every ".." segment, the result must be
// destDir itself or sit beneath destDir plus a separator. Comparing
// against the trailing separator rejects both a traversal that Clean
// resolves outside destDir ("../escape") and a sibling that merely
// shares destDir as a string prefix ("/tmp/dir-evil" against "/tmp/dir")
// — the case a naive strings.HasPrefix misses.
func safeJoin(destDir, name string) (string, error) {
	if name == "" {
		return "", fmt.Errorf("%w: empty entry name", ErrPathTraversal)
	}
	if filepath.IsAbs(name) || strings.HasPrefix(name, `\`) || strings.Contains(name, `:\`) {
		return "", fmt.Errorf("%w: absolute path not allowed: %s", ErrPathTraversal, name)
	}

	destDir = filepath.Clean(destDir)
	target := filepath.Clean(filepath.Join(destDir, name))
	if target != destDir && !strings.HasPrefix(target, destDir+string(os.PathSeparator)) {
		return "", fmt.Errorf("%w: %s", ErrPathTraversal, name)
	}
	return target, nil
}

// normalizeFileMode collapses a tar entry's mode to one of exactly two
// safe values: 0o755 when any execute bit is set, 0o644 otherwise.
//
// A release archive has no business carrying an exotic mode, so nothing
// else is ever carried forward. That is the guarantee: a tampered tarball
// cannot land a setuid, setgid, sticky, or world-writable binary on disk
// even if it somehow cleared checksum verification, because those bits
// are simply not among the two modes this can return.
func normalizeFileMode(mode os.FileMode) os.FileMode {
	if mode&0o111 != 0 {
		return 0o755
	}
	return 0o644
}

// extractTarGz extracts src into dest using the default size ceiling.
func extractTarGz(src, dest string) error {
	return extractTarGzWithCap(src, dest, maxExtractBytes)
}

// extractTarGzWithCap extracts every regular file in the gzipped tarball
// at src into dest, enforcing maxBytes both per entry and across the
// archive as a whole.
//
// The cap is a parameter rather than a constant so the zip-bomb guard
// can be exercised with a kilobyte-sized fixture instead of a 500 MiB
// one — a guard nobody can afford to test is a guard nobody tests.
//
// A traversal entry aborts the whole archive rather than being skipped.
// Extraction only happens after the archive's checksum has already been
// verified against the publisher's own manifest, so a hostile entry here
// means the signed artifact itself is compromised; continuing with "the
// rest" of such an archive would be the wrong instinct.
func extractTarGzWithCap(src, dest string, maxBytes int64) error {
	f, err := os.Open(src) //nolint:gosec // src is built inside a private temp dir owned by this package
	if err != nil {
		return fmt.Errorf("go-selfupdate: open archive: %w", err)
	}
	defer func() { _ = f.Close() }()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("go-selfupdate: gzip reader: %w", err)
	}
	defer func() { _ = gz.Close() }()

	return extractAllEntries(tar.NewReader(gz), dest, maxBytes)
}

// extractAllEntries walks every header in tr, writing regular files and
// tracking the running total against maxBytes.
func extractAllEntries(tr *tar.Reader, dest string, maxBytes int64) error {
	var total int64
	for {
		header, herr := tr.Next()
		if errors.Is(herr, io.EOF) {
			return nil
		}
		if herr != nil {
			return fmt.Errorf("go-selfupdate: tar header: %w", herr)
		}
		if header.Typeflag != tar.TypeReg {
			// Directories are created on demand per file, and symlinks
			// and device nodes have no place in a release archive.
			continue
		}

		written, eerr := extractOneFile(tr, header, dest, maxBytes)
		if eerr != nil {
			return eerr
		}

		total += written
		if total > maxBytes {
			return fmt.Errorf("%w: archive expands beyond %d bytes", ErrFileTooLarge, maxBytes)
		}
	}
}

// extractOneFile writes a single tar entry to disk after path
// validation, returning the number of bytes written.
func extractOneFile(tr *tar.Reader, header *tar.Header, dest string, maxBytes int64) (int64, error) {
	destPath, err := safeJoin(dest, header.Name)
	if err != nil {
		return 0, err
	}

	// Second traversal barrier, at the sink. safeJoin already rejects an
	// escaping entry, but re-asserting containment in the same function
	// that opens the file keeps the guarantee local to every filesystem
	// call below — and it is the exact prefix check the CodeQL go/zipslip
	// sanitizer recognizes, so a scanner sees the boundary too.
	if !strings.HasPrefix(destPath, filepath.Clean(dest)+string(os.PathSeparator)) {
		return 0, fmt.Errorf("%w: %s", ErrPathTraversal, header.Name)
	}

	if dirErr := os.MkdirAll(filepath.Dir(destPath), extractDirPerm); dirErr != nil {
		return 0, fmt.Errorf("go-selfupdate: mkdir for %s: %w", destPath, dirErr)
	}

	mode := normalizeFileMode(os.FileMode(header.Mode & 0o7777)) //nolint:gosec // masked to permission bits, then normalized
	destFile, err := os.OpenFile(destPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return 0, fmt.Errorf("go-selfupdate: create %s: %w", destPath, err)
	}

	// One byte past the cap so an entry sitting exactly at the limit is
	// distinguishable from one that exceeded it.
	written, copyErr := io.Copy(destFile, io.LimitReader(tr, maxBytes+1))
	closeErr := destFile.Close()

	if written > maxBytes {
		_ = os.Remove(destPath)
		return 0, fmt.Errorf("%w: %s exceeds %d bytes", ErrFileTooLarge, header.Name, maxBytes)
	}
	if copyErr != nil {
		return 0, fmt.Errorf("go-selfupdate: extract %s: %w", destPath, copyErr)
	}
	if closeErr != nil {
		return 0, fmt.Errorf("go-selfupdate: close %s: %w", destPath, closeErr)
	}

	// Chmod explicitly: os.OpenFile applies the caller's umask to the
	// mode argument, and the archive's 0o755 is what we want regardless
	// of how tight the user's umask is.
	if chmodErr := os.Chmod(destPath, mode); chmodErr != nil {
		return 0, fmt.Errorf("go-selfupdate: chmod %s: %w", destPath, chmodErr)
	}
	return written, nil
}

// locateBinary returns the path of binaryName inside an extracted
// directory tree. The walk is recursive so an archive that nests the
// executable under bin/ keeps working.
func locateBinary(extractDir, binaryName string) (string, error) {
	candidates := []string{binaryName}
	if !strings.HasSuffix(binaryName, ".exe") {
		candidates = append(candidates, binaryName+".exe")
	}

	var found string
	walkErr := filepath.WalkDir(extractDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		for _, name := range candidates {
			if d.Name() == name {
				found = path
				return fs.SkipAll
			}
		}
		return nil
	})
	if walkErr != nil {
		return "", fmt.Errorf("go-selfupdate: walk extract dir: %w", walkErr)
	}
	if found == "" {
		return "", fmt.Errorf("%w: %s", ErrBinaryNotFound, binaryName)
	}
	return found, nil
}
