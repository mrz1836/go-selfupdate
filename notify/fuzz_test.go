package notify

import (
	"strings"
	"testing"
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
