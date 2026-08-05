package selfupdate

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mrz1836/go-selfupdate/internal/testutil"
)

func TestProbeInstallDirWritable(t *testing.T) {
	t.Parallel()

	t.Run("writable directory passes and leaves nothing behind", func(t *testing.T) {
		dir := t.TempDir()
		target := filepath.Join(dir, "tool")

		if err := probeInstallDirWritable(target); err != nil {
			t.Fatalf("probeInstallDirWritable(%q) = %v, want nil", target, err)
		}

		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatalf("read dir: %v", err)
		}
		if len(entries) != 0 {
			t.Errorf("probe left %d file(s) behind: %v", len(entries), entries)
		}
	})

	t.Run("read-only directory reports the sentinel and the path", func(t *testing.T) {
		testutil.SkipOnWindows(t)
		testutil.SkipIfRoot(t)

		readOnly, _ := testutil.LockDir(t, t.TempDir())
		target := filepath.Join(readOnly, "tool")
		err := probeInstallDirWritable(target)
		if !errors.Is(err, ErrInstallDirNotWritable) {
			t.Fatalf("probeInstallDirWritable(%q) = %v, want ErrInstallDirNotWritable", target, err)
		}
		if !strings.Contains(err.Error(), "install dir not writable") {
			t.Errorf("error %q does not carry the operator-facing phrase", err)
		}
		if !strings.Contains(err.Error(), readOnly) {
			t.Errorf("error %q does not name the directory %q", err, readOnly)
		}
		if !strings.Contains(err.Error(), "~/.local/bin") {
			t.Errorf("error %q does not point the user at a user-writable directory", err)
		}
		if strings.Contains(err.Error(), "sudo") {
			t.Errorf("error %q suggests sudo; the guidance is deliberately elevation-free", err)
		}
	})

	t.Run("missing directory is install-blocking under the same sentinel", func(t *testing.T) {
		target := filepath.Join(t.TempDir(), "no-such-dir", "tool")

		err := probeInstallDirWritable(target)
		if !errors.Is(err, ErrInstallDirNotWritable) {
			t.Fatalf("probeInstallDirWritable(%q) = %v, want ErrInstallDirNotWritable", target, err)
		}
	})

	t.Run("a stale probe file does not lock the tool out", func(t *testing.T) {
		dir := t.TempDir()
		stale := filepath.Join(dir, probeFileName)
		if err := os.WriteFile(stale, []byte("left over from a crashed run"), 0o600); err != nil {
			t.Fatalf("seed stale probe: %v", err)
		}

		if err := probeInstallDirWritable(filepath.Join(dir, "tool")); err != nil {
			t.Fatalf("probe with a stale file present = %v, want nil", err)
		}
		if _, err := os.Stat(stale); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("stale probe file survived: %v", err)
		}
	})
}

func TestInstallPreflight(t *testing.T) {
	t.Parallel()

	t.Run("a writable location reports no blocker", func(t *testing.T) {
		target := filepath.Join(t.TempDir(), "tool")
		if err := InstallPreflight(Config{TargetPath: target}); err != nil {
			t.Fatalf("InstallPreflight(writable) = %v, want nil", err)
		}
	})

	t.Run("a read-only location is refused with actionable, sudo-free guidance", func(t *testing.T) {
		testutil.SkipOnWindows(t)
		testutil.SkipIfRoot(t)

		readOnly, _ := testutil.LockDir(t, t.TempDir())
		err := InstallPreflight(Config{TargetPath: filepath.Join(readOnly, "tool")})
		if !errors.Is(err, ErrInstallDirNotWritable) {
			t.Fatalf("InstallPreflight(read-only) = %v, want ErrInstallDirNotWritable", err)
		}
		if !strings.Contains(err.Error(), "~/.local/bin") {
			t.Errorf("error %q does not point at a user-writable directory", err)
		}
	})
}
