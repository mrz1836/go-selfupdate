package selfupdate

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/mrz1836/go-selfupdate/internal/testutil"
)

func TestInstallBinary(t *testing.T) {
	t.Parallel()

	t.Run("replaces the target and leaves no staging file", func(t *testing.T) {
		dir := t.TempDir()
		src := testutil.WriteTempFile(t, dir, "new-binary", []byte("version two"), 0o755)
		dst := testutil.WriteTempFile(t, dir, "widget", []byte("version one"), 0o755)

		if err := installBinary(src, dst); err != nil {
			t.Fatalf("installBinary() = %v, want nil", err)
		}

		got, err := os.ReadFile(dst) //nolint:gosec // test-controlled path
		if err != nil {
			t.Fatalf("read target: %v", err)
		}
		if string(got) != "version two" {
			t.Errorf("target = %q, want %q", got, "version two")
		}
		if _, statErr := os.Stat(dst + ".new"); !errors.Is(statErr, os.ErrNotExist) {
			t.Error("the staging file survived a successful install")
		}
	})

	t.Run("installs onto a path that does not exist yet", func(t *testing.T) {
		dir := t.TempDir()
		src := testutil.WriteTempFile(t, dir, "new-binary", []byte("fresh"), 0o755)
		dst := filepath.Join(dir, "widget")

		if err := installBinary(src, dst); err != nil {
			t.Fatalf("installBinary() = %v, want nil", err)
		}

		info, err := os.Stat(dst)
		if err != nil {
			t.Fatalf("stat target: %v", err)
		}
		if runtime.GOOS != "windows" && info.Mode().Perm() != installFilePerm {
			t.Errorf("mode = %v, want %v", info.Mode().Perm(), installFilePerm)
		}
	})

	t.Run("preserves a deliberately restricted target mode", func(t *testing.T) {
		testutil.SkipOnWindows(t)

		dir := t.TempDir()
		src := testutil.WriteTempFile(t, dir, "new-binary", []byte("version two"), 0o755)
		dst := testutil.WriteTempFile(t, dir, "widget", []byte("version one"), 0o700)

		if err := installBinary(src, dst); err != nil {
			t.Fatalf("installBinary() = %v, want nil", err)
		}

		info, err := os.Stat(dst)
		if err != nil {
			t.Fatalf("stat target: %v", err)
		}
		if got := info.Mode().Perm(); got != 0o700 {
			t.Errorf("mode = %v, want the preserved 0700", got)
		}
	})

	t.Run("the old inode survives for a process that still holds it", func(t *testing.T) {
		// This is the whole reason for rename-over-copy: a running
		// binary keeps reading the file it started from.
		dir := t.TempDir()
		src := testutil.WriteTempFile(t, dir, "new-binary", []byte("version two"), 0o755)
		dst := testutil.WriteTempFile(t, dir, "widget", []byte("version one"), 0o755)

		held, err := os.Open(dst) //nolint:gosec // test-controlled path
		if err != nil {
			t.Fatalf("open target: %v", err)
		}
		defer func() { _ = held.Close() }()

		if err := installBinary(src, dst); err != nil {
			t.Fatalf("installBinary() = %v, want nil", err)
		}

		buf := make([]byte, len("version one"))
		if _, err := held.Read(buf); err != nil {
			t.Fatalf("read from the held descriptor: %v", err)
		}
		if string(buf) != "version one" {
			t.Errorf("held descriptor now reads %q; the install truncated the live inode", buf)
		}
	})

	t.Run("a missing source fails without disturbing the target", func(t *testing.T) {
		dir := t.TempDir()
		dst := testutil.WriteTempFile(t, dir, "widget", []byte("version one"), 0o755)

		if err := installBinary(filepath.Join(dir, "absent"), dst); !errors.Is(err, ErrInstallFailed) {
			t.Fatalf("installBinary() with a missing source = %v, want ErrInstallFailed", err)
		}

		got, err := os.ReadFile(dst) //nolint:gosec // test-controlled path
		if err != nil {
			t.Fatalf("read target: %v", err)
		}
		if string(got) != "version one" {
			t.Errorf("target = %q, want the original contents intact", got)
		}
		if _, statErr := os.Stat(dst + ".new"); !errors.Is(statErr, os.ErrNotExist) {
			t.Error("a failed install left a staging file behind")
		}
	})

	t.Run("a read-only directory fails without corrupting the target", func(t *testing.T) {
		testutil.SkipOnWindows(t)
		testutil.SkipIfRoot(t)

		src := testutil.WriteTempFile(t, t.TempDir(), "new-binary", []byte("version two"), 0o755)
		_, dst := testutil.LockDir(t, t.TempDir())

		if err := installBinary(src, dst); !errors.Is(err, ErrInstallFailed) {
			t.Fatalf("installBinary() into a read-only directory = %v, want ErrInstallFailed", err)
		}

		testutil.AssertFileContents(t, dst, "locked binary")
	})

	t.Run("a rename failure is reported and clears the staging file", func(t *testing.T) {
		// Staging succeeds but the rename cannot land: dst is a non-empty
		// directory, which no file can be renamed over. This is the exact
		// branch that fires when the target is a locked running binary
		// (Windows), so it must return an error and leave no ".new" behind.
		dir := t.TempDir()
		src := testutil.WriteTempFile(t, dir, "new-binary", []byte("version two"), 0o755)

		dst := filepath.Join(dir, "occupied")
		if err := os.Mkdir(dst, 0o755); err != nil {
			t.Fatalf("mkdir dst: %v", err)
		}
		testutil.WriteTempFile(t, dst, "keep", []byte("resident"), 0o644)

		if err := installBinary(src, dst); !errors.Is(err, ErrInstallFailed) {
			t.Fatalf("installBinary() renaming over a directory = %v, want ErrInstallFailed", err)
		}
		if _, statErr := os.Stat(dst + ".new"); !errors.Is(statErr, os.ErrNotExist) {
			t.Error("a failed rename left the staging file behind")
		}
	})
}

func TestInstallCopyFile(t *testing.T) {
	t.Parallel()

	t.Run("copies content and applies the requested mode", func(t *testing.T) {
		dir := t.TempDir()
		src := testutil.WriteTempFile(t, dir, "src", []byte("payload"), 0o600)
		dst := filepath.Join(dir, "dst")

		if err := copyFile(src, dst, 0o755); err != nil {
			t.Fatalf("copyFile() = %v, want nil", err)
		}

		got, err := os.ReadFile(dst) //nolint:gosec // test-controlled path
		if err != nil {
			t.Fatalf("read destination: %v", err)
		}
		if string(got) != "payload" {
			t.Errorf("destination = %q, want %q", got, "payload")
		}

		info, err := os.Stat(dst)
		if err != nil {
			t.Fatalf("stat destination: %v", err)
		}
		if runtime.GOOS != "windows" && info.Mode().Perm() != 0o755 {
			t.Errorf("mode = %v, want 0755 regardless of umask", info.Mode().Perm())
		}
	})

	t.Run("truncates an existing destination rather than appending", func(t *testing.T) {
		dir := t.TempDir()
		src := testutil.WriteTempFile(t, dir, "src", []byte("short"), 0o600)
		dst := testutil.WriteTempFile(t, dir, "dst", []byte("a much longer previous body"), 0o600)

		if err := copyFile(src, dst, 0o644); err != nil {
			t.Fatalf("copyFile() = %v, want nil", err)
		}

		got, err := os.ReadFile(dst) //nolint:gosec // test-controlled path
		if err != nil {
			t.Fatalf("read destination: %v", err)
		}
		if string(got) != "short" {
			t.Errorf("destination = %q, want %q", got, "short")
		}
	})
}
