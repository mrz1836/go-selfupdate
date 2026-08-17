package selfupdate

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// installFilePerm is the fallback mode for a freshly installed binary
// when the target does not already exist. It matches the goreleaser
// default.
const installFilePerm os.FileMode = 0o755

// installBinary replaces dst with the executable at src.
//
// The sequence is stage, fsync, chmod, rename: src is copied to a
// "<dst>.new" sibling, flushed to stable storage, given the right mode,
// and only then renamed over dst. Two properties follow from that
// ordering:
//
//   - The rename is atomic. dst is either the old binary or the new one,
//     never a half-written file, even if the machine loses power mid
//     install. This is what makes the backup-and-restore dance some
//     tools use unnecessary — that approach leaves a window in which the
//     binary does not exist at all, and if the process dies inside that
//     window the user is left with no working tool.
//   - A running process survives it. Renaming leaves the old inode
//     intact for anything that has it mapped, whereas truncating and
//     rewriting dst in place corrupts the memory map of the very process
//     doing the upgrade (SIGBUS on Linux).
//
// Staging as a *sibling* of dst rather than in the temp directory is
// what keeps the rename on one filesystem: the cross-device hop from the
// extract directory happens during the copy, where it is just I/O, not
// during the rename, where it would be EXDEV. The existing mode is
// preserved when dst is present so a deliberately restricted install
// does not silently widen.
func installBinary(src, dst string) error {
	mode := installFilePerm
	if info, statErr := os.Stat(dst); statErr == nil {
		mode = info.Mode().Perm()
	}

	tmp := dst + ".new"
	if copyErr := copyFile(src, tmp, mode); copyErr != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("%w: stage binary at %s: %w", ErrInstallFailed, tmp, copyErr)
	}

	if renErr := os.Rename(tmp, dst); renErr != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("%w: install binary at %s: %w", ErrInstallFailed, dst, renErr)
	}

	// The new binary is in place. copyFile already applied the mode to the
	// staging file before the rename, so this re-chmod is belt-and-braces
	// for a network filesystem that drops permission bits on create — a
	// failure here must never report a completed install as failed, so it
	// is best-effort.
	_ = os.Chmod(dst, mode)

	// Flush the directory entry so which name points at which inode is
	// durable across a power loss, not just the file contents (already
	// fsynced by copyFile). Best-effort: not every filesystem supports it.
	syncDir(filepath.Dir(dst))
	return nil
}

// syncDir best-effort fsyncs a directory so a completed rename survives a
// crash. Errors are ignored: some platforms and filesystems do not
// support syncing a directory handle, and a durability nicety must never
// turn a successful install into a reported failure.
func syncDir(dir string) {
	d, err := os.Open(dir) //nolint:gosec // dir is the resolved install target's own directory
	if err != nil {
		return
	}
	_ = d.Sync()
	_ = d.Close()
}

// copyFile copies src to dst with the given mode, fsyncing before close
// so the bytes are on stable storage before the caller renames over a
// working binary.
func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src) //nolint:gosec // src is this package's own extracted file
	if err != nil {
		return fmt.Errorf("open source: %w", err)
	}
	defer func() { _ = in.Close() }()

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode) //nolint:gosec // dst is derived from the resolved install target
	if err != nil {
		return fmt.Errorf("create destination: %w", err)
	}

	if _, copyErr := io.Copy(out, in); copyErr != nil {
		_ = out.Close()
		return fmt.Errorf("copy: %w", copyErr)
	}
	if syncErr := out.Sync(); syncErr != nil {
		_ = out.Close()
		return fmt.Errorf("fsync: %w", syncErr)
	}
	if closeErr := out.Close(); closeErr != nil {
		return fmt.Errorf("close destination: %w", closeErr)
	}

	// Explicit chmod: the mode passed to OpenFile is filtered by the
	// caller's umask, and an executable that lands non-executable is a
	// confusing failure to debug.
	if chmodErr := os.Chmod(dst, mode); chmodErr != nil {
		return fmt.Errorf("chmod destination: %w", chmodErr)
	}
	return nil
}
