package notify

import "testing"

// BenchmarkFormatBanner measures rendering the update notice, which a CLI
// does once per invocation when an update is available.
func BenchmarkFormatBanner(b *testing.B) {
	cfg := Config{AppName: "widget", BinaryName: "widget", Style: BannerASCII}

	for b.Loop() {
		_ = FormatBanner(cfg, "v1.0.0", "v1.2.0")
	}
}

// BenchmarkPadRight measures the rune-aware padding that every banner row
// is built from.
func BenchmarkPadRight(b *testing.B) {
	for b.Loop() {
		_ = padRight("v1.2.3", versionDisplayWidth)
	}
}
