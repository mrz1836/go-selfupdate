package selfupdate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mrz1836/go-selfupdate/internal/testutil"
)

func TestDetectManagedHomebrew(t *testing.T) {
	t.Parallel()

	paths := []string{
		filepath.Join(string(os.PathSeparator), "opt", "homebrew", "Cellar", "widget", "1.2.3", "bin", "widget"),
		filepath.Join(string(os.PathSeparator), "usr", "local", "Cellar", "widget", "1.2.3", "bin", "widget"),
	}

	for _, path := range paths {
		managed, reason := detectManaged(path, testutil.EnvMap(nil))
		if !managed {
			t.Errorf("detectManaged(%q) = false, want true for a Cellar path", path)
			continue
		}
		if !strings.Contains(reason, "brew upgrade widget") {
			t.Errorf("reason %q does not tell the user what to run instead", reason)
		}
	}
}

func TestDetectManagedGoBin(t *testing.T) {
	t.Parallel()

	t.Run("GOBIN is recognized", func(t *testing.T) {
		gobin := t.TempDir()
		path := testutil.WriteTempFile(t, gobin, "widget", []byte("binary"), 0o755)

		managed, reason := detectManaged(path, testutil.EnvMap(map[string]string{"GOBIN": gobin}))
		if !managed {
			t.Fatalf("detectManaged(%q) with GOBIN=%q = false, want true", path, gobin)
		}
		if !strings.Contains(reason, "release archive") {
			t.Errorf("reason %q does not point at the supported install method", reason)
		}
	})

	t.Run("GOPATH/bin is recognized", func(t *testing.T) {
		gopath := t.TempDir()
		binDir := filepath.Join(gopath, "bin")
		if err := os.Mkdir(binDir, 0o700); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		path := testutil.WriteTempFile(t, binDir, "widget", []byte("binary"), 0o755)

		managed, _ := detectManaged(path, testutil.EnvMap(map[string]string{"GOPATH": gopath}))
		if !managed {
			t.Fatalf("detectManaged(%q) with GOPATH=%q = false, want true", path, gopath)
		}
	})

	t.Run("a multi-entry GOPATH is searched in full", func(t *testing.T) {
		first := t.TempDir()
		second := t.TempDir()
		binDir := filepath.Join(second, "bin")
		if err := os.Mkdir(binDir, 0o700); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		path := testutil.WriteTempFile(t, binDir, "widget", []byte("binary"), 0o755)

		gopath := strings.Join([]string{first, second}, string(os.PathListSeparator))
		if managed, _ := detectManaged(path, testutil.EnvMap(map[string]string{"GOPATH": gopath})); !managed {
			t.Fatalf("detectManaged(%q) with a multi-entry GOPATH = false, want true", path)
		}
	})

	t.Run("a sibling directory is not mistaken for the bin directory", func(t *testing.T) {
		gopath := t.TempDir()
		if err := os.Mkdir(filepath.Join(gopath, "bin"), 0o700); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		other := filepath.Join(gopath, "binaries")
		if err := os.Mkdir(other, 0o700); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		path := testutil.WriteTempFile(t, other, "widget", []byte("binary"), 0o755)

		if managed, reason := detectManaged(path, testutil.EnvMap(map[string]string{"GOPATH": gopath})); managed {
			t.Errorf("detectManaged(%q) = true (%s), want false for a sibling directory", path, reason)
		}
	})
}

func TestDetectManagedNonWritableTarget(t *testing.T) {
	testutil.SkipOnWindows(t)
	t.Parallel()

	dir := t.TempDir()
	path := testutil.WriteTempFile(t, dir, "widget", []byte("binary"), 0o555)

	managed, reason := detectManaged(path, testutil.EnvMap(nil))
	if !managed {
		t.Fatalf("detectManaged(%q) on a read-only binary = false, want true", path)
	}
	if !strings.Contains(reason, "not writable") {
		t.Errorf("reason %q does not explain the permission problem", reason)
	}
}

func TestDetectManagedUnmanaged(t *testing.T) {
	t.Parallel()

	t.Run("an ordinary writable binary is not managed", func(t *testing.T) {
		path := testutil.WriteTempFile(t, t.TempDir(), "widget", []byte("binary"), 0o755)

		if managed, reason := detectManaged(path, testutil.EnvMap(map[string]string{"GOBIN": string(os.PathSeparator) + "nowhere"})); managed {
			t.Errorf("detectManaged(%q) = true (%s), want false", path, reason)
		}
	})

	t.Run("an empty path is not managed", func(t *testing.T) {
		if managed, _ := detectManaged("", testutil.EnvMap(nil)); managed {
			t.Error(`detectManaged("") = true, want false`)
		}
	})

	t.Run("a path that does not exist is not managed", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "absent")

		if managed, reason := detectManaged(path, testutil.EnvMap(nil)); managed {
			t.Errorf("detectManaged(%q) = true (%s), want false", path, reason)
		}
	})

	t.Run("a nil getenv falls back to the process environment", func(t *testing.T) {
		path := testutil.WriteTempFile(t, t.TempDir(), "widget", []byte("binary"), 0o755)

		if managed, reason := detectManaged(path, nil); managed {
			t.Errorf("detectManaged(%q, nil) = true (%s), want false", path, reason)
		}
	})
}

func TestDetectManagedFollowsSymlinks(t *testing.T) {
	testutil.SkipOnWindows(t)
	t.Parallel()

	// Homebrew's real shape: a symlink in <prefix>/bin pointing into the
	// Cellar. Detection has to follow it, or every Homebrew install looks
	// unmanaged.
	root := t.TempDir()
	cellar := filepath.Join(root, "Cellar", "widget", "1.2.3", "bin")
	if err := os.MkdirAll(cellar, 0o700); err != nil {
		t.Fatalf("mkdir cellar: %v", err)
	}
	real := testutil.WriteTempFile(t, cellar, "widget", []byte("binary"), 0o755)

	binDir := filepath.Join(root, "bin")
	if err := os.Mkdir(binDir, 0o700); err != nil {
		t.Fatalf("mkdir bin: %v", err)
	}
	link := filepath.Join(binDir, "widget")
	if err := os.Symlink(real, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	managed, reason := detectManaged(link, testutil.EnvMap(nil))
	if !managed {
		t.Fatalf("detectManaged(%q) = false, want true after following the symlink", link)
	}
	if !strings.Contains(reason, "brew upgrade") {
		t.Errorf("reason %q does not identify the Homebrew install", reason)
	}
}

func TestDetectManagedExportedWrapper(t *testing.T) {
	t.Parallel()

	// The exported entry point must behave like the injected one for a
	// plainly unmanaged path.
	path := testutil.WriteTempFile(t, t.TempDir(), "widget", []byte("binary"), 0o755)

	if managed, reason := DetectManaged(path); managed {
		t.Errorf("DetectManaged(%q) = true (%s), want false", path, reason)
	}
	if managed, _ := DetectManaged(""); managed {
		t.Error(`DetectManaged("") = true, want false`)
	}
}
