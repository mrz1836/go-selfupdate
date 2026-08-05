package selfupdate

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestExtractSafeJoin(t *testing.T) {
	dest := filepath.Join(string(os.PathSeparator), "tmp", "dest")

	t.Run("accepts entries inside the destination", func(t *testing.T) {
		for _, name := range []string{"widget", "bin/widget", "./widget", "a/b/c/widget"} {
			got, err := safeJoin(dest, name)
			if err != nil {
				t.Errorf("safeJoin(%q) = %v, want nil", name, err)
				continue
			}
			if !strings.HasPrefix(got, dest) {
				t.Errorf("safeJoin(%q) = %q, which is outside %q", name, got, dest)
			}
		}
	})

	t.Run("rejects traversal and absolute entries", func(t *testing.T) {
		hostile := []string{
			"../escape",
			"../../etc/passwd",
			"bin/../../escape",
			"./../../escape",
			filepath.Join(string(os.PathSeparator), "etc", "passwd"),
			"",
		}
		for _, name := range hostile {
			if _, err := safeJoin(dest, name); !errors.Is(err, ErrPathTraversal) {
				t.Errorf("safeJoin(%q) = %v, want ErrPathTraversal", name, err)
			}
		}
	})

	t.Run("rejects a sibling that merely shares the destination prefix", func(t *testing.T) {
		// The classic strings.HasPrefix bug: "/tmp/dest-evil" starts
		// with "/tmp/dest" but is not inside it.
		if _, err := safeJoin(dest, "../dest-evil/widget"); !errors.Is(err, ErrPathTraversal) {
			t.Errorf("safeJoin sibling-prefix = %v, want ErrPathTraversal", err)
		}
	})

	t.Run("rejects windows-shaped absolute entries on every platform", func(t *testing.T) {
		for _, name := range []string{`\windows\system32\evil.exe`, `C:\windows\evil.exe`} {
			if _, err := safeJoin(dest, name); !errors.Is(err, ErrPathTraversal) {
				t.Errorf("safeJoin(%q) = %v, want ErrPathTraversal", name, err)
			}
		}
	})
}

func TestExtractNormalizeFileMode(t *testing.T) {
	tests := []struct {
		name string
		in   os.FileMode
		want os.FileMode
	}{
		{name: "executable stays executable", in: 0o755, want: 0o755},
		{name: "data file collapses to 0644", in: 0o644, want: 0o644},
		{name: "world-writable executable is tightened", in: 0o777, want: 0o755},
		{name: "setuid bit is stripped", in: 0o755 | os.ModeSetuid, want: 0o755},
		{name: "setgid bit is stripped", in: 0o755 | os.ModeSetgid, want: 0o755},
		{name: "sticky bit is stripped", in: 0o755 | os.ModeSticky, want: 0o755},
		{name: "owner-execute alone still counts", in: 0o700, want: 0o755},
		{name: "no permissions collapse to 0644", in: 0, want: 0o644},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := normalizeFileMode(tc.in); got != tc.want {
				t.Errorf("normalizeFileMode(%v) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestExtractTarGz(t *testing.T) {
	t.Run("extracts regular files and preserves the tree", func(t *testing.T) {
		archive := makeTarGz(t,
			tarEntry{name: "widget", body: []byte("binary")},
			tarEntry{name: "bin/helper", body: []byte("helper")},
			tarEntry{name: "README.md", body: []byte("docs"), mode: 0o644},
		)
		src := writeTempFile(t, t.TempDir(), "a.tar.gz", archive, 0o600)
		dest := t.TempDir()

		if err := extractTarGz(src, dest); err != nil {
			t.Fatalf("extractTarGz() = %v, want nil", err)
		}

		for name, want := range map[string]string{
			"widget":     "binary",
			"bin/helper": "helper",
			"README.md":  "docs",
		} {
			got, err := os.ReadFile(filepath.Join(dest, filepath.FromSlash(name))) //nolint:gosec // test-controlled path
			if err != nil {
				t.Errorf("read %s: %v", name, err)
				continue
			}
			if string(got) != want {
				t.Errorf("%s = %q, want %q", name, got, want)
			}
		}
	})

	t.Run("reports a corrupt archive that uses a file as a directory", func(t *testing.T) {
		// "foo" is a regular file and "foo/bar" then needs "foo" to be a
		// directory, so MkdirAll fails. A malformed archive like this must
		// surface an error, not panic or silently drop the entry.
		archive := makeTarGz(t,
			tarEntry{name: "foo", body: []byte("i am a file")},
			tarEntry{name: "foo/bar", body: []byte("nested")},
		)
		src := writeTempFile(t, t.TempDir(), "a.tar.gz", archive, 0o600)
		dest := t.TempDir()

		if err := extractTarGz(src, dest); err == nil {
			t.Fatal("extractTarGz() over a file-used-as-a-directory = nil, want an error")
		}
	})

	t.Run("normalizes modes on extracted files", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("unix mode bits are not meaningful on windows")
		}

		archive := makeTarGz(t,
			tarEntry{name: "widget", body: []byte("binary"), mode: 0o4777},
			tarEntry{name: "notes.txt", body: []byte("notes"), mode: 0o666},
		)
		src := writeTempFile(t, t.TempDir(), "a.tar.gz", archive, 0o600)
		dest := t.TempDir()

		if err := extractTarGz(src, dest); err != nil {
			t.Fatalf("extractTarGz() = %v, want nil", err)
		}

		for name, want := range map[string]os.FileMode{"widget": 0o755, "notes.txt": 0o644} {
			info, err := os.Stat(filepath.Join(dest, name))
			if err != nil {
				t.Fatalf("stat %s: %v", name, err)
			}
			if got := info.Mode().Perm(); got != want {
				t.Errorf("%s mode = %v, want %v", name, got, want)
			}
		}
	})

	t.Run("a traversal entry aborts the whole archive", func(t *testing.T) {
		archive := makeTarGz(t,
			tarEntry{name: "widget", body: []byte("legitimate")},
			tarEntry{name: "../escaped", body: []byte("hostile")},
		)
		src := writeTempFile(t, t.TempDir(), "a.tar.gz", archive, 0o600)
		parent := t.TempDir()
		dest := filepath.Join(parent, "extract")
		if err := os.Mkdir(dest, 0o700); err != nil {
			t.Fatalf("mkdir: %v", err)
		}

		err := extractTarGz(src, dest)
		if !errors.Is(err, ErrPathTraversal) {
			t.Fatalf("extractTarGz() = %v, want ErrPathTraversal", err)
		}
		if _, statErr := os.Stat(filepath.Join(parent, "escaped")); !errors.Is(statErr, os.ErrNotExist) {
			t.Error("a traversal entry escaped the destination directory")
		}
	})

	t.Run("an entry larger than the cap is rejected and removed", func(t *testing.T) {
		archive := makeTarGz(t, tarEntry{name: "widget", body: []byte(strings.Repeat("x", 4096))})
		src := writeTempFile(t, t.TempDir(), "a.tar.gz", archive, 0o600)
		dest := t.TempDir()

		err := extractTarGzWithCap(src, dest, 1024)
		if !errors.Is(err, ErrFileTooLarge) {
			t.Fatalf("extractTarGzWithCap() = %v, want ErrFileTooLarge", err)
		}
		if _, statErr := os.Stat(filepath.Join(dest, "widget")); !errors.Is(statErr, os.ErrNotExist) {
			t.Error("the oversized partial file was left on disk")
		}
	})

	t.Run("many small entries that exceed the cap in aggregate are rejected", func(t *testing.T) {
		// Each entry clears the per-file cap comfortably; only the
		// running total catches this shape of decompression bomb.
		entries := make([]tarEntry, 0, 8)
		for i := range 8 {
			entries = append(entries, tarEntry{
				name: "file" + string(rune('a'+i)),
				body: []byte(strings.Repeat("x", 400)),
			})
		}
		src := writeTempFile(t, t.TempDir(), "a.tar.gz", makeTarGz(t, entries...), 0o600)

		err := extractTarGzWithCap(src, t.TempDir(), 1024)
		if !errors.Is(err, ErrFileTooLarge) {
			t.Fatalf("extractTarGzWithCap() = %v, want ErrFileTooLarge on the aggregate", err)
		}
	})

	t.Run("an entry sitting exactly at the cap is accepted", func(t *testing.T) {
		archive := makeTarGz(t, tarEntry{name: "widget", body: []byte(strings.Repeat("x", 1024))})
		src := writeTempFile(t, t.TempDir(), "a.tar.gz", archive, 0o600)

		if err := extractTarGzWithCap(src, t.TempDir(), 1024); err != nil {
			t.Errorf("extractTarGzWithCap() at exactly the cap = %v, want nil", err)
		}
	})

	t.Run("a non-gzip file is reported, not panicked on", func(t *testing.T) {
		src := writeTempFile(t, t.TempDir(), "a.tar.gz", []byte("definitely not gzip"), 0o600)

		if err := extractTarGz(src, t.TempDir()); err == nil {
			t.Fatal("extractTarGz() on a non-gzip file = nil, want an error")
		}
	})

	t.Run("a missing archive is reported", func(t *testing.T) {
		if err := extractTarGz(filepath.Join(t.TempDir(), "absent.tar.gz"), t.TempDir()); err == nil {
			t.Fatal("extractTarGz() on a missing file = nil, want an error")
		}
	})
}

func TestExtractLocateBinary(t *testing.T) {
	t.Run("finds a binary at the archive root", func(t *testing.T) {
		dir := t.TempDir()
		want := writeTempFile(t, dir, "widget", []byte("binary"), 0o755)

		got, err := locateBinary(dir, "widget")
		if err != nil {
			t.Fatalf("locateBinary() = %v, want nil", err)
		}
		if got != want {
			t.Errorf("locateBinary() = %q, want %q", got, want)
		}
	})

	t.Run("finds a binary nested under bin/", func(t *testing.T) {
		dir := t.TempDir()
		nested := filepath.Join(dir, "bin")
		if err := os.Mkdir(nested, 0o700); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		want := writeTempFile(t, nested, "widget", []byte("binary"), 0o755)

		got, err := locateBinary(dir, "widget")
		if err != nil {
			t.Fatalf("locateBinary() = %v, want nil", err)
		}
		if got != want {
			t.Errorf("locateBinary() = %q, want %q", got, want)
		}
	})

	t.Run("finds a windows-suffixed binary", func(t *testing.T) {
		dir := t.TempDir()
		want := writeTempFile(t, dir, "widget.exe", []byte("binary"), 0o755)

		got, err := locateBinary(dir, "widget")
		if err != nil {
			t.Fatalf("locateBinary() = %v, want nil", err)
		}
		if got != want {
			t.Errorf("locateBinary() = %q, want %q", got, want)
		}
	})

	t.Run("an absent binary reports ErrBinaryNotFound", func(t *testing.T) {
		dir := t.TempDir()
		writeTempFile(t, dir, "README.md", []byte("docs"), 0o644)

		if _, err := locateBinary(dir, "widget"); !errors.Is(err, ErrBinaryNotFound) {
			t.Fatalf("locateBinary() = %v, want ErrBinaryNotFound", err)
		}
	})
}
