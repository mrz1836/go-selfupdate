// Package managed adds the supervised layer around an upgrade: refuse
// to run inside a caller-defined drain window, and prove the tool still
// works afterwards.
//
// It exists because an unattended upgrade has two failure modes an
// interactive one does not. The first is timing: an upgrade that
// interrupts the work the host exists to do is a failed upgrade no
// matter what shipped, so [RunManaged] simply defers inside the drain
// window and lets the supervisor retry later. The second is silence: a
// binary that installs cleanly and then cannot start has broken the
// service without reporting anything, so a post-upgrade health check
// runs as a tripwire and its failure is a rollback signal, not a
// warning.
//
// The package is optional and stands alone: it imports only the standard
// library, and the core update package does not import it. The upgrade
// step, the health check, the rollback, and the clock are all injected,
// so the whole flow is exercisable with no network, no host, and no
// waiting.
package managed

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
)

// minutesPerDay is the wrap modulus for drain-window arithmetic.
const minutesPerDay = 24 * 60

// hoursPerDay bounds the hour component of a clock mark.
const hoursPerDay = 24

// minutesPerHour bounds the minute component of a clock mark.
const minutesPerHour = 60

// clockMarkParts is the number of components in an "HH:MM" mark.
const clockMarkParts = 2

// Managed-layer errors.
var (
	// ErrHealthCheckFailed wraps a post-upgrade tripwire failure so a
	// supervisor can tell "installed but broken" apart from "failed to
	// install" and roll back only in the first case.
	ErrHealthCheckFailed = errors.New("go-selfupdate/managed: post-upgrade health check failed")
	// ErrRollbackFailed wraps a rollback that itself failed. It is the
	// worst outcome the package can report and is deliberately
	// distinguishable: the host is now running an unverified binary that
	// could not be reverted.
	ErrRollbackFailed = errors.New("go-selfupdate/managed: rollback failed")
	// ErrManagedConfig is returned when RunManaged is called without the
	// upgrade step wired.
	ErrManagedConfig = errors.New("go-selfupdate/managed: managed config incomplete")
	// ErrInvalidClockMark is returned for a malformed "HH:MM" drain
	// window mark.
	ErrInvalidClockMark = errors.New("go-selfupdate/managed: invalid clock mark")
)

// DrainWindow is the interval a managed upgrade must never run inside,
// expressed as two marks in minutes since local midnight.
//
// When BellMin is greater than CloseoutMin the window wraps midnight —
// the ordinary overnight case, such as 19:00 through 04:00. That wrap is
// the whole reason this is a type rather than a pair of ints: a naive
// range comparison silently reports "outside the window" for every
// minute of the night it was meant to protect.
type DrainWindow struct {
	// BellMin opens the window (minutes since local midnight).
	BellMin int
	// CloseoutMin closes it, inclusive.
	CloseoutMin int
}

// NewDrainWindow builds a window from two "HH:MM" 24-hour clock marks.
func NewDrainWindow(bell, closeout string) (DrainWindow, error) {
	b, err := parseHM(bell)
	if err != nil {
		return DrainWindow{}, fmt.Errorf("go-selfupdate/managed: drain window open: %w", err)
	}
	c, err := parseHM(closeout)
	if err != nil {
		return DrainWindow{}, fmt.Errorf("go-selfupdate/managed: drain window close: %w", err)
	}
	return DrainWindow{BellMin: b, CloseoutMin: c}, nil
}

// Contains reports whether minute-of-day m lies inside the window,
// inclusive of both marks and handling the midnight-wrapping case. m is
// normalized into [0, 1440).
func (w DrainWindow) Contains(m int) bool {
	m = ((m % minutesPerDay) + minutesPerDay) % minutesPerDay
	if w.BellMin <= w.CloseoutMin {
		return m >= w.BellMin && m <= w.CloseoutMin
	}
	return m >= w.BellMin || m <= w.CloseoutMin
}

// IsZero reports whether the window is empty — both marks at midnight —
// which [RunManaged] treats as "no drain window configured".
func (w DrainWindow) IsZero() bool { return w.BellMin == 0 && w.CloseoutMin == 0 }

// String renders the window as "HH:MM-HH:MM".
func (w DrainWindow) String() string {
	return fmt.Sprintf("%02d:%02d-%02d:%02d",
		w.BellMin/minutesPerHour, w.BellMin%minutesPerHour,
		w.CloseoutMin/minutesPerHour, w.CloseoutMin%minutesPerHour)
}

// MinuteOfDay returns t's minutes since local midnight — the value
// [DrainWindow.Contains] tests. The instant's location decides the
// answer, so a caller crossing time zones should pass a time already in
// the host's zone.
func MinuteOfDay(t time.Time) int { return t.Hour()*minutesPerHour + t.Minute() }

// parseHM parses an "HH:MM" 24-hour clock mark into minutes since
// midnight, strictly: two components, both numeric, both in range.
func parseHM(s string) (int, error) {
	parts := strings.Split(strings.TrimSpace(s), ":")
	if len(parts) != clockMarkParts {
		return 0, fmt.Errorf("%w: %q", ErrInvalidClockMark, s)
	}

	hour, herr := strconv.Atoi(parts[0])
	minute, merr := strconv.Atoi(parts[1])
	if herr != nil || merr != nil {
		return 0, fmt.Errorf("%w: %q", ErrInvalidClockMark, s)
	}
	if hour < 0 || hour >= hoursPerDay || minute < 0 || minute >= minutesPerHour {
		return 0, fmt.Errorf("%w: %q", ErrInvalidClockMark, s)
	}
	return hour*minutesPerHour + minute, nil
}

// ManagedOutcome names the terminal state of a managed upgrade attempt.
type ManagedOutcome string

// The managed-upgrade outcomes.
const (
	// OutcomeDeferred means the attempt landed inside the drain window
	// and nothing was tried. It is a success, not a failure: the
	// supervisor retries after the window closes.
	OutcomeDeferred ManagedOutcome = "deferred_drain_window"
	// OutcomeUpgraded means the upgrade ran and the health check, if
	// any, passed.
	OutcomeUpgraded ManagedOutcome = "upgraded"
	// OutcomeUpgradeFailed means the upgrade step itself errored; the
	// health check never ran and nothing needs rolling back.
	OutcomeUpgradeFailed ManagedOutcome = "upgrade_failed"
	// OutcomeHealthCheckFailed means the upgrade installed but the
	// tripwire failed. Treat it as a failed upgrade and roll back.
	OutcomeHealthCheckFailed ManagedOutcome = "health_check_failed"
	// OutcomeRolledBack means the health check failed and the rollback
	// then succeeded — the host is back on the previous binary.
	OutcomeRolledBack ManagedOutcome = "rolled_back"
	// OutcomeRollbackFailed means the health check failed and the
	// rollback failed too. This is the one outcome that needs a human.
	OutcomeRollbackFailed ManagedOutcome = "rollback_failed"
)

// ManagedConfig wires the managed-upgrade flow. Every side effect is a
// callback so the orchestration can be tested without performing one.
type ManagedConfig struct {
	// Window is the interval upgrades are refused inside. The zero
	// window defers nothing.
	Window DrainWindow
	// Upgrade performs the install. Required; it is usually a closure
	// over the core package's Install.
	Upgrade func(context.Context) error
	// HealthCheck runs after a successful upgrade and proves the new
	// binary works. Nil means no health gate, which the caller is
	// accepting deliberately.
	HealthCheck func(context.Context) error
	// Rollback reverts to the previous binary after a failed health
	// check. Nil means the failure is reported without reverting.
	Rollback func(context.Context) error
	// Now reports the instant the drain window is tested against. Nil
	// means time.Now.
	Now func() time.Time
	// Stdout receives one narration line per outcome. Nil discards it.
	Stdout io.Writer
}

// RunManaged executes the managed-upgrade flow: defer inside the drain
// window; otherwise upgrade, health-check, and roll back if the check
// fails.
//
// The returned error is nil for both success and a drain-window
// deferral, because a deferral is the correct behavior rather than a
// fault — a supervisor should treat it as exit 0 and try again later.
// Every real failure returns its outcome alongside a wrapped error, so
// callers can branch on either.
func RunManaged(ctx context.Context, cfg ManagedConfig) (ManagedOutcome, error) {
	if cfg.Upgrade == nil {
		return "", fmt.Errorf("%w: Upgrade step is nil", ErrManagedConfig)
	}

	if cfg.Window.Contains(MinuteOfDay(now(cfg))) {
		narrate(cfg.Stdout, "upgrade deferred: inside the drain window "+cfg.Window.String()+"; will retry after it closes")
		return OutcomeDeferred, nil
	}

	if err := cfg.Upgrade(ctx); err != nil {
		return OutcomeUpgradeFailed, err
	}

	if cfg.HealthCheck != nil {
		if err := cfg.HealthCheck(ctx); err != nil {
			return handleHealthCheckFailure(ctx, cfg, err)
		}
	}

	narrate(cfg.Stdout, "upgrade complete: post-upgrade health check passed")
	return OutcomeUpgraded, nil
}

// handleHealthCheckFailure reports a failed tripwire and reverts when a
// rollback is wired. The health-check error is preserved in every
// branch: it names what actually broke, and a rollback that succeeds
// does not make that information less useful.
func handleHealthCheckFailure(ctx context.Context, cfg ManagedConfig, cause error) (ManagedOutcome, error) {
	healthErr := fmt.Errorf("%w: %w", ErrHealthCheckFailed, cause)

	if cfg.Rollback == nil {
		narrate(cfg.Stdout, "upgrade FAILED its health check and no rollback is configured; the previous binary was not restored")
		return OutcomeHealthCheckFailed, healthErr
	}

	narrate(cfg.Stdout, "upgrade FAILED its health check; rolling back")
	if err := cfg.Rollback(ctx); err != nil {
		narrate(cfg.Stdout, "rollback FAILED; this host needs attention")
		return OutcomeRollbackFailed, fmt.Errorf("%w: %w (after %w)", ErrRollbackFailed, err, healthErr)
	}

	narrate(cfg.Stdout, "rollback complete: the previous binary was restored")
	return OutcomeRolledBack, healthErr
}

// now returns the configured clock, or the real one.
func now(cfg ManagedConfig) time.Time {
	if cfg.Now != nil {
		return cfg.Now()
	}
	return time.Now()
}

// narrate writes one line when a writer is present.
func narrate(w io.Writer, msg string) {
	if w == nil {
		return
	}
	_, _ = fmt.Fprintln(w, msg)
}
