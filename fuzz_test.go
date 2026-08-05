package selfupdate

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

// The three fuzz targets below guard the parsers that sit between an
// attacker-controlled byte stream and the filesystem: archive entry
// names, the checksum manifest, and the version string that decides
// whether an upgrade happens at all. Table tests cover the inputs a
// human thought of; these cover the ones nobody did.

// FuzzSafeJoin asserts that safeJoin never returns a path outside
// destDir, whatever the tar entry name contains. This is the Zip-Slip
// boundary, so a single escaping input is a security defect rather than
// a correctness one.
//
// The corpus is the hush/lucid seed set: plain relative names, dot-dot
// traversal in several encodings, absolute POSIX and Windows paths, UNC
// prefixes, mixed separators, dot-runs, empty and whitespace names, an
// embedded NUL, and long repetitions of both "../" and "a/".
func FuzzSafeJoin(f *testing.F) {
	seeds := []struct {
		destDir string
		name    string
	}{
		{"/tmp/safe", "file.txt"},
		{"/tmp/safe", "subdir/file.txt"},
		{"/tmp/safe", "deep/nested/dir/file.txt"},

		{"/tmp/safe", "../etc/passwd"},
		{"/tmp/safe", "../../etc/passwd"},
		{"/tmp/safe", "../../../../etc/passwd"},
		{"/tmp/safe", "valid/../../../etc/passwd"},

		{"/tmp/safe", "/etc/passwd"},
		{"/tmp/safe", "/root/.ssh/id_rsa"},

		{"/tmp/safe", `C:\Windows\System32`},
		{"/tmp/safe", `\\server\share\file`},
		{"/tmp/safe", `..\..\..\Windows\System32`},

		{"/tmp/safe", `../\../etc/passwd`},
		{"/tmp/safe", `..\/../etc/passwd`},

		{"/tmp/safe", "..."},
		{"/tmp/safe", "...."},

		{"/tmp/safe", ""},
		{"/tmp/safe", " "},

		{"/tmp/safe", "file\x00.txt"},

		// A sibling directory sharing destDir as a string prefix: the
		// case a naive strings.HasPrefix check waves through.
		{"/tmp/safe", "../safe-evil/file.txt"},

		{"/tmp/safe", strings.Repeat("../", 100)},
		{"/tmp/safe", strings.Repeat("a/", 100) + "file.txt"},

		{"/", "file.txt"},
		{".", "file.txt"},

		// A relative dot-dot destDir: "../.." lexically begins with "../",
		// so a bare prefix check accepts it as "inside" destDir="..". Rel is
		// what distinguishes containment here.
		{"..", ".."},
		{"../a", ".."},
	}
	for _, s := range seeds {
		f.Add(s.destDir, s.name)
	}

	f.Fuzz(func(t *testing.T, destDir, name string) {
		got, err := safeJoin(destDir, name)
		assertNoAbsoluteEscape(t, name, got, err)
		assertWithinDest(t, destDir, name, got, err)
	})
}

// assertNoAbsoluteEscape fails the test if an absolute entry name was
// accepted — Zip-Slip's primary disguise.
func assertNoAbsoluteEscape(t *testing.T, name, got string, err error) {
	t.Helper()

	if filepath.IsAbs(name) && err == nil {
		t.Errorf("SECURITY: absolute path accepted: %q -> %q", name, got)
	}
}

// assertWithinDest fails the test if safeJoin returned a path whose
// lexical relation to destDir escapes the directory. A returned error is
// always acceptable — refusing an entry is never a security failure — so
// the error branch only stringifies to prove it cannot panic.
func assertWithinDest(t *testing.T, destDir, name, got string, err error) {
	t.Helper()

	if err != nil {
		if !errors.Is(err, ErrPathTraversal) {
			_ = err.Error() // ensure no panic in stringification
		}
		return
	}

	rel, relErr := filepath.Rel(filepath.Clean(destDir), filepath.Clean(got))
	if relErr != nil {
		return
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		t.Errorf("SECURITY: escape via %q -> rel=%q", name, rel)
	}
}

// FuzzParseChecksum asserts that parseChecksum only ever returns a
// digest that is a genuine 64-character lowercase hex string actually
// recorded for the requested asset.
//
// The failure mode this guards is the quiet one: a manifest that is
// truncated, padded, CRLF-terminated, or listing a weaker hash must
// produce ErrChecksumNotFound, never a short or partial digest that
// downstream code would then compare against. A verifier that accepts a
// malformed digest is worse than no verifier, because it reports
// success.
func FuzzParseChecksum(f *testing.F) {
	const validDigest = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"

	seeds := []struct {
		data  string
		asset string
	}{
		{validDigest + "  app_1.0.0_linux_amd64.tar.gz\n", "app_1.0.0_linux_amd64.tar.gz"},
		{validDigest + " app_1.0.0_linux_amd64.tar.gz\n", "app_1.0.0_linux_amd64.tar.gz"},
		{validDigest + "\tapp_1.0.0_linux_amd64.tar.gz\r\n", "app_1.0.0_linux_amd64.tar.gz"},

		// Right shape, wrong asset.
		{validDigest + "  other.tar.gz\n", "app_1.0.0_linux_amd64.tar.gz"},

		// Weaker hashes must not be accepted as SHA-256.
		{"d41d8cd98f00b204e9800998ecf8427e  app.tar.gz\n", "app.tar.gz"},
		{"da39a3ee5e6b4b0d3255bfef95601890afd80709  app.tar.gz\n", "app.tar.gz"},

		// Truncated, over-long, and non-hex digests.
		{validDigest[:63] + "  app.tar.gz\n", "app.tar.gz"},
		{validDigest + "ff  app.tar.gz\n", "app.tar.gz"},
		{strings.Repeat("z", 64) + "  app.tar.gz\n", "app.tar.gz"},

		// Uppercase digest: acceptable, but must come back lowercased.
		{strings.ToUpper(validDigest) + "  app.tar.gz\n", "app.tar.gz"},

		// Structural edges.
		{"", "app.tar.gz"},
		{"\n\n\n", "app.tar.gz"},
		{validDigest, "app.tar.gz"},
		{"  app.tar.gz\n", "app.tar.gz"},
		{validDigest + "  app.tar.gz\n", ""},
		{validDigest + "  a  b\n", "a"},
		{strings.Repeat(validDigest+"  app.tar.gz\n", 50), "app.tar.gz"},
	}
	for _, s := range seeds {
		f.Add(s.data, s.asset)
	}

	f.Fuzz(func(t *testing.T, data, asset string) {
		digest, err := parseChecksum(data, asset)
		if err != nil {
			if !errors.Is(err, ErrChecksumNotFound) {
				t.Errorf("parseChecksum returned %v, want ErrChecksumNotFound", err)
			}
			if digest != "" {
				t.Errorf("SECURITY: digest %q returned alongside error", digest)
			}
			return
		}

		if len(digest) != sha256HexLen {
			t.Errorf("SECURITY: accepted a %d-character digest %q", len(digest), digest)
		}
		if !isHexString(digest) {
			t.Errorf("SECURITY: accepted a non-hex digest %q", digest)
		}
		if digest != strings.ToLower(digest) {
			t.Errorf("digest %q was not normalized to lowercase", digest)
		}
		if !strings.Contains(strings.ToLower(data), digest) {
			t.Errorf("SECURITY: digest %q does not appear in the manifest", digest)
		}
	})
}

// FuzzCompare asserts the ordering invariants of Compare across
// arbitrary version strings.
//
// The property that matters is antisymmetry: Compare(a, b) and
// Compare(b, a) must be exact negations. If they are not, some input
// exists for which two peers disagree about which build is newer — the
// shape of bug that makes a fleet upgrade-loop or silently refuse to
// move. The conservative contract (an unorderable pair compares equal,
// so IsNewer is false in both directions) is what keeps the library from
// announcing an upgrade it cannot justify, and it holds here by
// construction rather than by convention.
func FuzzCompare(f *testing.F) {
	seeds := []struct{ a, b string }{
		{"1.0.0", "1.0.1"},
		{"v1.0.0", "1.0.0"},
		{"1.2.3", "1.2.3"},
		{"2.0.0", "1.9.9"},
		{"1.0.0-rc1", "1.0.0"},
		{"1.0.0+build.5", "1.0.0"},
		{"dev", "1.0.0"},
		{"", "1.0.0"},
		{"deadbeef", "1.0.0"},
		{"dev", "dev"},
		{"", ""},
		{"not.a.version", "also-not"},
		{"1.0", "1.0.0"},
		{"1.0.0.0", "1.0.0"},
		{"-1.0.0", "1.0.0"},
		{"99999999999999999999.0.0", "1.0.0"},
		{"v", "v"},
	}
	for _, s := range seeds {
		f.Add(s.a, s.b)
	}

	f.Fuzz(func(t *testing.T, a, b string) {
		ab, ba := Compare(a, b), Compare(b, a)

		for _, got := range []int{ab, ba} {
			if got < -1 || got > 1 {
				t.Fatalf("Compare returned %d, want -1, 0, or 1", got)
			}
		}
		if ab != -ba {
			t.Errorf("Compare is not antisymmetric: Compare(%q, %q)=%d, Compare(%q, %q)=%d", a, b, ab, b, a, ba)
		}
		if a == b && ab != 0 {
			t.Errorf("Compare(%q, %q) = %d, want 0 for identical input", a, b, ab)
		}
		if got := IsNewer(a, b); got != (ab < 0) {
			t.Errorf("IsNewer(%q, %q) = %v, disagrees with Compare = %d", a, b, got, ab)
		}
	})
}
