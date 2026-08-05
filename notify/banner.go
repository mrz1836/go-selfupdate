package notify

import (
	"fmt"
	"io"
	"os"
	"strings"
	"unicode/utf8"
)

// Banner rendering constants.
const (
	// bannerBoxWidth is the character count between the box's left and
	// right borders.
	bannerBoxWidth = 70
	// versionDisplayWidth is the fixed column width for a version
	// string, wide enough for a typical "v1.2.3-beta.1".
	versionDisplayWidth = 12

	bannerColorReset  = "\033[0m"
	bannerColorYellow = "\033[33m"
)

// BannerStyle selects the characters the banner is drawn with.
type BannerStyle int

// The banner styles.
const (
	// BannerAuto draws the Unicode box on a terminal and the ASCII box
	// everywhere else. It is the zero value, so a caller that expresses
	// no preference gets the right thing in both places.
	BannerAuto BannerStyle = iota
	// BannerFancy always draws the Unicode box.
	BannerFancy
	// BannerASCII always draws the ASCII box.
	BannerASCII
)

// FormatBanner renders the update notice for current → latest. It
// performs no I/O, so a caller can log or test the exact text; the only
// environment it consults is the one needed to resolve BannerAuto.
func FormatBanner(cfg Config, current, latest string) string {
	title := "   A new version of " + bannerName(cfg) + " is available!"
	command := "   " + upgradeCommand(cfg)

	if fancyStyle(cfg) {
		return formatBannerFancy(title, command, current, latest)
	}
	return formatBannerASCII(title, command, current, latest)
}

// ShowBanner writes the update notice to Config.BannerOut when result
// reports an available update, and stays silent otherwise — including
// when the check failed. A failed update check is the notifier's
// problem, not the user's.
func ShowBanner(cfg Config, result *Result) {
	if result == nil || result.Err != nil || !result.UpdateAvailable {
		return
	}
	writeBanner(bannerWriter(cfg), FormatBanner(cfg, result.CurrentVersion, result.LatestVersion), useColor(cfg))
}

// writeBanner emits banner to w, wrapping it in color only when asked.
// It is the testable core of [ShowBanner].
func writeBanner(w io.Writer, banner string, color bool) {
	if w == nil {
		return
	}
	if color {
		_, _ = fmt.Fprintf(w, "%s%s%s\n", bannerColorYellow, banner, bannerColorReset)
		return
	}
	_, _ = fmt.Fprintf(w, "%s\n", banner)
}

// bannerWriter returns the configured banner stream, defaulting to
// stderr so the notice never contaminates piped stdout.
func bannerWriter(cfg Config) io.Writer {
	if cfg.BannerOut != nil {
		return cfg.BannerOut
	}
	return os.Stderr
}

// bannerName is the tool's name as it appears in the title: uppercased,
// and elided with an ellipsis when it is long enough to crowd out the
// rest of the sentence — so "is available!" is never the part that gets
// clipped by the box's width bound. The ellipsis is ASCII so the plain
// banner stays plain.
func bannerName(cfg Config) string {
	name := strings.ToUpper(appNameOrRepo(cfg))

	// Interior width minus the fixed title text is the room a name may
	// occupy before it starts eating the sentence around it.
	const fixed = len("   A new version of ") + len(" is available!")
	maxName := bannerBoxWidth - fixed
	if maxName < len(bannerEllipsis)+1 || utf8.RuneCountInString(name) <= maxName {
		return name
	}
	return string([]rune(name)[:maxName-len(bannerEllipsis)]) + bannerEllipsis
}

// bannerEllipsis marks a name clipped to fit the banner. ASCII on purpose.
const bannerEllipsis = "..."

// upgradeCommand returns the command the banner tells the user to run.
func upgradeCommand(cfg Config) string {
	if cfg.UpgradeCommand != "" {
		return cfg.UpgradeCommand
	}
	name := cfg.BinaryName
	if name == "" {
		name = appNameOrRepo(cfg)
	}
	return name + " update"
}

// fancyStyle reports whether the Unicode box should be used.
func fancyStyle(cfg Config) bool {
	switch cfg.Style {
	case BannerFancy:
		return true
	case BannerASCII:
		return false
	case BannerAuto:
		return isTerminal(bannerWriter(cfg))
	default:
		return isTerminal(bannerWriter(cfg))
	}
}

// useColor reports whether the banner should be colorized: only on a
// real terminal, never under CI, and never when NO_COLOR is set.
func useColor(cfg Config) bool {
	getenv := cfg.Getenv
	if getenv == nil {
		getenv = os.Getenv
	}
	if strings.TrimSpace(getenv("NO_COLOR")) != "" {
		return false
	}
	if isCI(getenv) {
		return false
	}
	return isTerminal(bannerWriter(cfg))
}

// isTerminal reports whether w is a character device, without pulling in
// golang.org/x/term. A writer that is not an *os.File — a buffer, a
// pipe wrapper, a test recorder — is never a terminal, which is exactly
// the answer a caller capturing output wants.
func isTerminal(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

// formatBannerFancy draws the notice with Unicode box characters.
func formatBannerFancy(title, command, current, latest string) string {
	return joinBanner(
		"  ╭"+strings.Repeat("─", bannerBoxWidth)+"╮",
		"  ╰"+strings.Repeat("─", bannerBoxWidth)+"╯",
		"│", title, command, current, latest,
	)
}

// formatBannerASCII draws the notice with plain ASCII, for a terminal
// (or a log file) that cannot be trusted with box-drawing characters.
func formatBannerASCII(title, command, current, latest string) string {
	rule := "  +" + strings.Repeat("-", bannerBoxWidth) + "+"
	return joinBanner(rule, rule, "|", title, command, current, latest)
}

// joinBanner assembles the shared banner body between the given top and
// bottom rules, using border as the vertical edge character.
func joinBanner(top, bottom, border, title, command, current, latest string) string {
	empty := padRight("", bannerBoxWidth)
	versions := fmt.Sprintf("   Current: %s   Latest: %s",
		padVersion(current, versionDisplayWidth), padVersion(latest, versionDisplayWidth))

	row := func(content string) string {
		return border + padRight(content, bannerBoxWidth) + border
	}

	lines := []string{
		"",
		top,
		"  " + border + empty + border,
		"  " + row(title),
		"  " + border + empty + border,
		"  " + row(versions),
		"  " + border + empty + border,
		"  " + row("   Update:"),
		"  " + row(command),
		"  " + border + empty + border,
		bottom,
		"",
	}
	return strings.Join(lines, "\n")
}

// padVersion pads a version string to a minimum column width so the two
// version columns line up, but never truncates it. A version is data: a
// silently clipped "v1.2.3-beta." would report a release that does not
// exist. A pathological length is bounded by the row's own width, not by
// mangling the number.
func padVersion(version string, width int) string {
	if utf8.RuneCountInString(version) >= width {
		return version
	}
	return padRight(version, width)
}

// padRight pads s to width, or truncates it there, counting runes rather
// than bytes so a multi-byte character does not shift the border.
func padRight(s string, width int) string {
	count := utf8.RuneCountInString(s)
	if count >= width {
		return string([]rune(s)[:width])
	}
	return s + strings.Repeat(" ", width-count)
}
