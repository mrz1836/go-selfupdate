package notify

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// FuzzEnvPrefix asserts that envPrefix only ever emits characters that are
// valid in a POSIX environment-variable name — uppercase ASCII, digits,
// and underscore — whatever the application name contains. A stray
// character here would produce a variable a shell cannot set, silently
// disabling the per-tool opt-out or token override.
func FuzzEnvPrefix(f *testing.F) {
	for _, s := range []string{
		"widget", "go-pre-commit", "mage-x", "Widget CLI", "go.invoice",
		"", "  ", strings.Repeat("x", 200), "café", "a\x00b", "ǆ",
	} {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, app string) {
		for _, r := range envPrefix(app) {
			ok := (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_'
			if !ok {
				t.Errorf("envPrefix(%q) produced disallowed rune %q", app, r)
			}
		}
	})
}

// FuzzAppSlug asserts that appSlug always yields a non-empty,
// filesystem-safe directory name: only lowercase ASCII, digits, hyphen,
// and underscore, and never a leading or trailing hyphen. The slug names a
// directory in the user's config tree, so an empty or hyphen-edged value
// would put the cache somewhere surprising.
func FuzzAppSlug(f *testing.F) {
	for _, s := range []string{
		"widget", "go-pre-commit", "Widget CLI", "go.invoice",
		"--", "", "..a..", "___", "  spaced  ", "café", "a\x00b",
	} {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, app string) {
		slug := appSlug(app)
		if slug == "" {
			t.Errorf("appSlug(%q) = empty, want a non-empty slug", app)
		}
		for _, r := range slug {
			ok := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_'
			if !ok {
				t.Errorf("appSlug(%q) produced disallowed rune %q", app, r)
			}
		}
		if strings.HasPrefix(slug, "-") || strings.HasSuffix(slug, "-") {
			t.Errorf("appSlug(%q) = %q, want no leading or trailing hyphen", app, slug)
		}
	})
}

// FuzzFormatBanner asserts, over arbitrary version strings, the invariant
// TestBannerRowsAreRectangular pins for its four hand-picked cases: every
// rendered row is the same rune width, so the box always closes.
// FormatBanner is pure and takes caller-supplied versions, so a multibyte,
// empty, or pathologically long tag must never panic or break the
// geometry — the padding math counts runes, and this proves it holds
// beyond the inputs a table can enumerate.
func FuzzFormatBanner(f *testing.F) {
	seeds := []struct{ current, latest string }{
		{"v1.0.0", "v1.2.0"},
		{"", ""},
		{"dev", "v10.20.30"},
		{"v1.2.3-beta.1+build.99", "v2.0.0-rc.1"},
		{"バージョン一", "リリース二"},                   // multibyte runes
		{"🚀🚀🚀", "🎉"},                          // emoji: multi-byte runes
		{strings.Repeat("v1.2.3-", 40), "v2"}, // long enough to hit the width bound
		{"v1", strings.Repeat("9", 500)},
		{"  spaced  ", "\ttabbed"},
	}
	for _, s := range seeds {
		f.Add(s.current, s.latest)
	}

	f.Fuzz(func(t *testing.T, current, latest string) {
		cfg := testConfig(t, nil)
		for _, style := range []BannerStyle{BannerASCII, BannerFancy} {
			cfg.Style = style

			// The call itself is the no-panic assertion.
			banner := FormatBanner(cfg, current, latest)

			// A version carrying a newline would inject its own line into
			// the box — something git forbids a release tag from doing — so
			// the geometry invariant is asserted over the newline-free
			// domain the notice is actually rendered for.
			if strings.ContainsAny(current+latest, "\n") {
				continue
			}
			assertBannerRectangular(t, style, current, latest, banner)
		}
	})
}

// assertBannerRectangular fails the test unless every row of banner is the
// same rune width, equal to the box's own fixed width — the exact property
// TestBannerRowsAreRectangular checks, factored out so the fuzz target can
// reuse it.
func assertBannerRectangular(t *testing.T, style BannerStyle, current, latest, banner string) {
	t.Helper()

	width := -1
	for i, line := range strings.Split(strings.Trim(banner, "\n"), "\n") {
		got := utf8.RuneCountInString(line)
		if i == 0 {
			width = got
			continue
		}
		if got != width {
			t.Fatalf("style %d banner for (%q, %q): line %d is %d runes, want %d\n%s",
				style, current, latest, i, got, width, banner)
		}
	}
	if want := bannerBoxWidth + 4; width != want {
		t.Fatalf("style %d banner for (%q, %q): width = %d, want %d", style, current, latest, width, want)
	}
}
