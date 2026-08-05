package selfupdate

import (
	"testing"

	"github.com/mrz1836/go-selfupdate/internal/testutil"
)

// BenchmarkCompare measures the version ordering that runs on every check
// and every passive notice.
func BenchmarkCompare(b *testing.B) {
	for b.Loop() {
		_ = Compare("v1.2.3", "1.2.4")
	}
}

// BenchmarkParseChecksum measures the checksum-manifest parse that guards
// every install.
func BenchmarkParseChecksum(b *testing.B) {
	const asset = "widget_1.2.3_linux_amd64.tar.gz"
	data := testutil.SHA256Hex([]byte("a plausible release archive")) + "  " + asset + "\n"

	for b.Loop() {
		if _, err := parseChecksum(data, asset); err != nil {
			b.Fatalf("parseChecksum: %v", err)
		}
	}
}

// BenchmarkSafeJoin measures the Zip-Slip boundary check applied to every
// archive entry.
func BenchmarkSafeJoin(b *testing.B) {
	dest := b.TempDir()

	for b.Loop() {
		if _, err := safeJoin(dest, "bin/widget"); err != nil {
			b.Fatalf("safeJoin: %v", err)
		}
	}
}

// BenchmarkExtractTarGzWithCap measures a full guarded extraction of a
// small archive, the hot path between download and install.
func BenchmarkExtractTarGzWithCap(b *testing.B) {
	archive := testutil.MakeTarGz(b, testutil.TarEntry{Name: "widget", Body: []byte("a plausible binary payload")})
	src := testutil.WriteTempFile(b, b.TempDir(), "a.tar.gz", archive, 0o600)
	dest := b.TempDir()

	for b.Loop() {
		if err := extractTarGzWithCap(src, dest, maxExtractBytes); err != nil {
			b.Fatalf("extractTarGzWithCap: %v", err)
		}
	}
}
