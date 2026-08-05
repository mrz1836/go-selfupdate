package notify

import (
	"bytes"
	"os"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/mrz1836/go-selfupdate/internal/testutil"
)

// asciiBanner is the golden ASCII notice for widget v1.0.0 → v1.2.0. It
// is written out in full rather than assembled, so a change to the box
// geometry has to be an intentional edit to this literal.
const asciiBanner = `
  +----------------------------------------------------------------------+
  |                                                                      |
  |   A new version of WIDGET is available!                              |
  |                                                                      |
  |   Current: v1.0.0         Latest: v1.2.0                             |
  |                                                                      |
  |   Update:                                                            |
  |   widget update                                                      |
  |                                                                      |
  +----------------------------------------------------------------------+
`

func TestFormatBannerASCIIGolden(t *testing.T) {
	t.Parallel()

	cfg := Config{AppName: "widget", BinaryName: "widget", Style: BannerASCII}
	got := FormatBanner(cfg, "v1.0.0", "v1.2.0")

	if got != asciiBanner {
		t.Fatalf("banner mismatch\n--- got ---\n%s\n--- want ---\n%s", got, asciiBanner)
	}
}

// TestBannerRowsAreRectangular guards the property the golden string
// encodes but does not explain: every row is the same rune width, so the
// box closes no matter how long the version strings are.
func TestBannerRowsAreRectangular(t *testing.T) {
	t.Parallel()

	styles := map[string]BannerStyle{"ascii": BannerASCII, "fancy": BannerFancy}
	versions := [][2]string{
		{"v1.0.0", "v1.2.0"},
		{"dev", "v10.20.30"},
		{"v1.2.3-beta.1+build.99", "v2.0.0-rc.1"},
		{"", ""},
	}

	for name, style := range styles {
		for _, v := range versions {
			cfg := Config{AppName: "widget", BinaryName: "widget", Style: style}
			banner := FormatBanner(cfg, v[0], v[1])

			var width int
			for i, line := range strings.Split(strings.Trim(banner, "\n"), "\n") {
				got := utf8.RuneCountInString(line)
				if i == 0 {
					width = got
					continue
				}
				if got != width {
					t.Fatalf("%s banner for %v: line %d is %d runes, want %d\n%s", name, v, i, got, width, banner)
				}
			}
			if want := bannerBoxWidth + 4; width != want {
				t.Fatalf("%s banner width = %d, want %d", name, width, want)
			}
		}
	}
}

func TestFormatBannerFancyUsesBoxDrawing(t *testing.T) {
	t.Parallel()

	cfg := Config{AppName: "widget", BinaryName: "widget", Style: BannerFancy}
	got := FormatBanner(cfg, "v1.0.0", "v1.2.0")

	for _, want := range []string{"╭", "╮", "│", "╰", "╯"} {
		if !strings.Contains(got, want) {
			t.Fatalf("fancy banner is missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "+---") {
		t.Fatalf("fancy banner contains ASCII rules:\n%s", got)
	}
}

// TestFormatBannerAutoOffTerminal is the case that matters for piped
// output and log files: with no terminal, the notice must degrade to
// ASCII rather than emit box-drawing characters into a file.
func TestFormatBannerAutoOffTerminal(t *testing.T) {
	t.Parallel()

	cfg := Config{AppName: "widget", BinaryName: "widget", BannerOut: &bytes.Buffer{}}
	if got := FormatBanner(cfg, "v1.0.0", "v1.2.0"); !strings.Contains(got, "+---") {
		t.Fatalf("auto style off a terminal should be ASCII:\n%s", got)
	}
}

func TestFormatBannerUsesUpgradeCommand(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		cfg  Config
		want string
	}{
		"explicit command":       {cfg: Config{AppName: "widget", UpgradeCommand: "brew upgrade widget"}, want: "brew upgrade widget"},
		"derived from binary":    {cfg: Config{AppName: "widget", BinaryName: "wg"}, want: "wg update"},
		"derived from app name":  {cfg: Config{AppName: "widget"}, want: "widget update"},
		"derived from repo name": {cfg: Config{Repo: "widget"}, want: "widget update"},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			cfg := tc.cfg
			cfg.Style = BannerASCII
			if got := FormatBanner(cfg, "v1.0.0", "v1.2.0"); !strings.Contains(got, tc.want) {
				t.Fatalf("banner does not mention %q:\n%s", tc.want, got)
			}
		})
	}
}

func TestShowBannerSilentCases(t *testing.T) {
	t.Parallel()

	tests := map[string]*Result{
		"nil result":        nil,
		"failed check":      {UpdateAvailable: true, Err: errSource},
		"already current":   {CurrentVersion: "v1.2.0", LatestVersion: "v1.2.0"},
		"no update flagged": {CurrentVersion: "v1.0.0", LatestVersion: "v1.2.0", UpdateAvailable: false},
	}

	for name, result := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var out bytes.Buffer
			ShowBanner(Config{AppName: "widget", BannerOut: &out, Getenv: testutil.EnvMap(nil)}, result)
			if out.Len() != 0 {
				t.Fatalf("ShowBanner wrote %q, want silence", out.String())
			}
		})
	}
}

func TestShowBannerWritesToConfiguredStream(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	cfg := Config{AppName: "widget", BinaryName: "widget", BannerOut: &out, Getenv: testutil.EnvMap(nil)}
	ShowBanner(cfg, &Result{CurrentVersion: "v1.0.0", LatestVersion: "v1.2.0", UpdateAvailable: true})

	got := out.String()
	if !strings.Contains(got, "A new version of WIDGET is available!") {
		t.Fatalf("banner = %q, want the update notice", got)
	}
	if !strings.Contains(got, "v1.2.0") {
		t.Fatalf("banner = %q, want the latest version", got)
	}
	// A captured stream is not a terminal, so no escape codes.
	if strings.Contains(got, "\033[") {
		t.Fatalf("banner = %q, want no color off a terminal", got)
	}
}

// TestShowBannerNilWriterDoesNotPanic covers the writeBanner guard for a
// Config that was never normalized and carries a nil stream.
func TestShowBannerNilWriterDoesNotPanic(t *testing.T) {
	t.Parallel()

	writeBanner(nil, "dropped", false)
}

func TestWriteBannerColor(t *testing.T) {
	t.Parallel()

	var colored, plain bytes.Buffer
	writeBanner(&colored, "notice", true)
	writeBanner(&plain, "notice", false)

	if want := bannerColorYellow + "notice" + bannerColorReset + "\n"; colored.String() != want {
		t.Fatalf("colored = %q, want %q", colored.String(), want)
	}
	if want := "notice\n"; plain.String() != want {
		t.Fatalf("plain = %q, want %q", plain.String(), want)
	}
}

func TestUseColor(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		out  any
		env  map[string]string
		want bool
	}{
		"buffer is never a terminal": {out: "buffer", want: false},
		"NO_COLOR suppresses":        {out: "tty", env: map[string]string{"NO_COLOR": "1"}, want: false},
		"CI suppresses":              {out: "tty", env: map[string]string{"CI": "true"}, want: false},
		"terminal allows":            {out: "tty", want: true},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			cfg := Config{AppName: "widget", Getenv: testutil.EnvMap(tc.env)}
			if tc.out == "tty" {
				dev, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
				if err != nil {
					t.Skipf("cannot open %s: %v", os.DevNull, err)
				}
				t.Cleanup(func() { _ = dev.Close() })
				cfg.BannerOut = dev
			} else {
				cfg.BannerOut = &bytes.Buffer{}
			}

			if got := useColor(cfg); got != tc.want {
				t.Fatalf("useColor() = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestUseColorNilGetenv covers the fallback to the real environment.
func TestUseColorNilGetenv(t *testing.T) {
	t.Setenv("NO_COLOR", "1")

	if useColor(Config{AppName: "widget", BannerOut: &bytes.Buffer{}}) {
		t.Fatal("useColor should honor NO_COLOR from the real environment")
	}
}

func TestIsTerminal(t *testing.T) {
	t.Parallel()

	if isTerminal(&bytes.Buffer{}) {
		t.Fatal("a buffer is not a terminal")
	}

	regular, err := os.CreateTemp(t.TempDir(), "notify-*")
	if err != nil {
		t.Fatalf("create temp file: %v", err)
	}
	t.Cleanup(func() { _ = regular.Close() })
	if isTerminal(regular) {
		t.Fatal("a regular file is not a terminal")
	}

	// A closed file cannot be stat'ed, which must read as "not a
	// terminal" rather than panic.
	closed, err := os.CreateTemp(t.TempDir(), "notify-closed-*")
	if err != nil {
		t.Fatalf("create temp file: %v", err)
	}
	_ = closed.Close()
	if isTerminal(closed) {
		t.Fatal("a closed file is not a terminal")
	}

	dev, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Skipf("cannot open %s: %v", os.DevNull, err)
	}
	t.Cleanup(func() { _ = dev.Close() })
	if !isTerminal(dev) {
		t.Fatalf("%s is a character device and should read as a terminal", os.DevNull)
	}
}

func TestBannerWriterDefault(t *testing.T) {
	t.Parallel()

	if got := bannerWriter(Config{}); got != os.Stderr {
		t.Fatalf("bannerWriter() = %v, want os.Stderr", got)
	}
	var buf bytes.Buffer
	if got := bannerWriter(Config{BannerOut: &buf}); got != &buf {
		t.Fatal("bannerWriter should return the configured stream")
	}
}

func TestPadRightAndPadVersion(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		in          string
		width       int
		want        string // padRight pads or truncates to width
		wantVersion string // padVersion pads to a minimum but never truncates
	}{
		"pads short":  {in: "ab", width: 5, want: "ab   ", wantVersion: "ab   "},
		"exact width": {in: "abcde", width: 5, want: "abcde", wantVersion: "abcde"},
		"long: padRight truncates, padVersion keeps":  {in: "abcdefgh", width: 5, want: "abcde", wantVersion: "abcdefgh"},
		"multi-byte counted":                          {in: "héllo", width: 6, want: "héllo ", wantVersion: "héllo "},
		"multi-byte: padRight cuts, padVersion keeps": {in: "héllo", width: 3, want: "hél", wantVersion: "héllo"},
		"empty": {in: "", width: 3, want: "   ", wantVersion: "   "},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if got := padRight(tc.in, tc.width); got != tc.want {
				t.Fatalf("padRight(%q, %d) = %q, want %q", tc.in, tc.width, got, tc.want)
			}
			if got := padVersion(tc.in, tc.width); got != tc.wantVersion {
				t.Fatalf("padVersion(%q, %d) = %q, want %q", tc.in, tc.width, got, tc.wantVersion)
			}
		})
	}
}

// TestFancyStyleUnknownValue pins the default branch: an out-of-range
// style behaves like BannerAuto rather than silently picking fancy.
func TestFancyStyleUnknownValue(t *testing.T) {
	t.Parallel()

	cfg := Config{AppName: "widget", Style: BannerStyle(42), BannerOut: &bytes.Buffer{}}
	if fancyStyle(cfg) {
		t.Fatal("an unknown style off a terminal should not be fancy")
	}
}
