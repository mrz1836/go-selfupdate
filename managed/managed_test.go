package managed

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// errBoom is a stand-in failure for an injected callback.
var errBoom = errors.New("boom")

// at builds a local time at the given hour and minute. The date is
// irrelevant to the drain window, which only ever reads minute-of-day.
func at(hour, minute int) time.Time {
	return time.Date(2026, time.March, 14, hour, minute, 0, 0, time.Local)
}

// clock returns a Now callback frozen at t.
func clock(t time.Time) func() time.Time {
	return func() time.Time { return t }
}

func TestNewDrainWindow(t *testing.T) {
	tests := map[string]struct {
		bell, closeout string
		wantBell       int
		wantCloseout   int
		wantErr        bool
	}{
		"daytime window":      {bell: "09:00", closeout: "17:30", wantBell: 540, wantCloseout: 1050},
		"overnight window":    {bell: "19:00", closeout: "04:00", wantBell: 1140, wantCloseout: 240},
		"midnight boundaries": {bell: "00:00", closeout: "23:59", wantBell: 0, wantCloseout: 1439},
		"leading zeros":       {bell: "01:05", closeout: "02:07", wantBell: 65, wantCloseout: 127},
		"hour out of range":   {bell: "24:00", closeout: "01:00", wantErr: true},
		"minute out of range": {bell: "01:60", closeout: "02:00", wantErr: true},
		"negative hour":       {bell: "-1:00", closeout: "02:00", wantErr: true},
		"missing colon":       {bell: "0900", closeout: "17:00", wantErr: true},
		"too many parts":      {bell: "09:00:00", closeout: "17:00", wantErr: true},
		"non-numeric":         {bell: "ab:cd", closeout: "17:00", wantErr: true},
		"empty":               {bell: "", closeout: "17:00", wantErr: true},
		"bad closeout":        {bell: "09:00", closeout: "nope", wantErr: true},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			w, err := NewDrainWindow(tc.bell, tc.closeout)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("NewDrainWindow(%q, %q) = %+v, want error", tc.bell, tc.closeout, w)
				}
				if !errors.Is(err, ErrInvalidClockMark) {
					t.Fatalf("error %v does not wrap ErrInvalidClockMark", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("NewDrainWindow(%q, %q): %v", tc.bell, tc.closeout, err)
			}
			if w.BellMin != tc.wantBell || w.CloseoutMin != tc.wantCloseout {
				t.Fatalf("got %+v, want {BellMin:%d CloseoutMin:%d}", w, tc.wantBell, tc.wantCloseout)
			}
		})
	}
}

// TestDrainWindowContainsWrapping is the reason DrainWindow is a type:
// an overnight window spans midnight, and a naive range check reports
// "outside" for every minute of the night it was meant to protect.
func TestDrainWindowContainsWrapping(t *testing.T) {
	overnight := DrainWindow{BellMin: 19 * 60, CloseoutMin: 4 * 60}

	tests := map[string]struct {
		minute int
		want   bool
	}{
		"before the window":     {minute: 18 * 60, want: false},
		"at the open mark":      {minute: 19 * 60, want: true},
		"just after open":       {minute: 19*60 + 1, want: true},
		"just before midnight":  {minute: 23*60 + 59, want: true},
		"midnight itself":       {minute: 0, want: true},
		"deep in the night":     {minute: 2 * 60, want: true},
		"at the close mark":     {minute: 4 * 60, want: true},
		"just after the close":  {minute: 4*60 + 1, want: false},
		"middle of the workday": {minute: 12 * 60, want: false},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			if got := overnight.Contains(tc.minute); got != tc.want {
				t.Fatalf("Contains(%d) = %v, want %v", tc.minute, got, tc.want)
			}
		})
	}
}

func TestDrainWindowContainsNonWrapping(t *testing.T) {
	daytime := DrainWindow{BellMin: 9 * 60, CloseoutMin: 17 * 60}

	tests := map[string]struct {
		minute int
		want   bool
	}{
		"before open":        {minute: 8*60 + 59, want: false},
		"at open":            {minute: 9 * 60, want: true},
		"inside":             {minute: 13 * 60, want: true},
		"at close":           {minute: 17 * 60, want: true},
		"after close":        {minute: 17*60 + 1, want: false},
		"midnight outside":   {minute: 0, want: false},
		"normalized above":   {minute: 13*60 + minutesPerDay, want: true},
		"normalized below":   {minute: 13*60 - minutesPerDay, want: true},
		"normalized outside": {minute: minutesPerDay, want: false},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			if got := daytime.Contains(tc.minute); got != tc.want {
				t.Fatalf("Contains(%d) = %v, want %v", tc.minute, got, tc.want)
			}
		})
	}
}

func TestDrainWindowZeroAndString(t *testing.T) {
	var zero DrainWindow
	if !zero.IsZero() {
		t.Fatal("the zero DrainWindow should report IsZero")
	}
	// A zero window still contains midnight exactly; every other minute
	// is outside it, so it defers nothing in practice.
	if !zero.Contains(0) {
		t.Fatal("the zero window contains minute 0")
	}
	if zero.Contains(1) {
		t.Fatal("the zero window contains only minute 0")
	}

	w := DrainWindow{BellMin: 19*60 + 5, CloseoutMin: 4 * 60}
	if got, want := w.String(), "19:05-04:00"; got != want {
		t.Fatalf("String() = %q, want %q", got, want)
	}
}

func TestMinuteOfDay(t *testing.T) {
	tests := map[string]struct {
		when time.Time
		want int
	}{
		"midnight":    {when: at(0, 0), want: 0},
		"one past":    {when: at(0, 1), want: 1},
		"noon":        {when: at(12, 0), want: 720},
		"last minute": {when: at(23, 59), want: 1439},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			if got := MinuteOfDay(tc.when); got != tc.want {
				t.Fatalf("MinuteOfDay(%v) = %d, want %d", tc.when, got, tc.want)
			}
		})
	}
}

// TestRunManagedDefersInsideWindow proves the deferral is a success,
// not a failure: no upgrade ran, no error came back.
func TestRunManagedDefersInsideWindow(t *testing.T) {
	var upgraded, checked bool

	outcome, err := RunManaged(t.Context(), ManagedConfig{
		Window:      DrainWindow{BellMin: 19 * 60, CloseoutMin: 4 * 60},
		Now:         clock(at(22, 30)),
		Upgrade:     func(context.Context) error { upgraded = true; return nil },
		HealthCheck: func(context.Context) error { checked = true; return nil },
	})
	if err != nil {
		t.Fatalf("a drain-window deferral must not be an error, got %v", err)
	}
	if outcome != OutcomeDeferred {
		t.Fatalf("outcome = %q, want %q", outcome, OutcomeDeferred)
	}
	if upgraded || checked {
		t.Fatalf("nothing should run inside the window (upgraded=%v checked=%v)", upgraded, checked)
	}
}

func TestRunManagedProceedsOutsideWindow(t *testing.T) {
	var order []string
	var out bytes.Buffer

	outcome, err := RunManaged(t.Context(), ManagedConfig{
		Window:      DrainWindow{BellMin: 19 * 60, CloseoutMin: 4 * 60},
		Now:         clock(at(10, 0)),
		Upgrade:     func(context.Context) error { order = append(order, "upgrade"); return nil },
		HealthCheck: func(context.Context) error { order = append(order, "health"); return nil },
		Rollback:    func(context.Context) error { order = append(order, "rollback"); return nil },
		Stdout:      &out,
	})
	if err != nil {
		t.Fatalf("RunManaged: %v", err)
	}
	if outcome != OutcomeUpgraded {
		t.Fatalf("outcome = %q, want %q", outcome, OutcomeUpgraded)
	}
	if want := []string{"upgrade", "health"}; strings.Join(order, ",") != strings.Join(want, ",") {
		t.Fatalf("call order = %v, want %v", order, want)
	}
	if !strings.Contains(out.String(), "health check passed") {
		t.Fatalf("narration = %q, want a success line", out.String())
	}
}

// TestRunManagedOvernightWrapProceeds covers the minute a naive range
// check gets wrong in the other direction: just outside a wrapping
// window, work must proceed.
func TestRunManagedOvernightWrapProceeds(t *testing.T) {
	outcome, err := RunManaged(t.Context(), ManagedConfig{
		Window:  DrainWindow{BellMin: 19 * 60, CloseoutMin: 4 * 60},
		Now:     clock(at(4, 1)),
		Upgrade: func(context.Context) error { return nil },
	})
	if err != nil {
		t.Fatalf("RunManaged: %v", err)
	}
	if outcome != OutcomeUpgraded {
		t.Fatalf("outcome = %q, want %q", outcome, OutcomeUpgraded)
	}
}

func TestRunManagedUpgradeFailureSkipsHealthCheck(t *testing.T) {
	var checked bool

	outcome, err := RunManaged(t.Context(), ManagedConfig{
		Now:         clock(at(10, 0)),
		Upgrade:     func(context.Context) error { return errBoom },
		HealthCheck: func(context.Context) error { checked = true; return nil },
	})
	if !errors.Is(err, errBoom) {
		t.Fatalf("error = %v, want it to wrap errBoom", err)
	}
	if outcome != OutcomeUpgradeFailed {
		t.Fatalf("outcome = %q, want %q", outcome, OutcomeUpgradeFailed)
	}
	if checked {
		t.Fatal("the health check must not run after a failed upgrade")
	}
	if errors.Is(err, ErrHealthCheckFailed) {
		t.Fatal("an install failure must not be reported as a health-check failure")
	}
}

func TestRunManagedHealthCheckFailureWithoutRollback(t *testing.T) {
	var out bytes.Buffer

	outcome, err := RunManaged(t.Context(), ManagedConfig{
		Now:         clock(at(10, 0)),
		Upgrade:     func(context.Context) error { return nil },
		HealthCheck: func(context.Context) error { return errBoom },
		Stdout:      &out,
	})
	if outcome != OutcomeHealthCheckFailed {
		t.Fatalf("outcome = %q, want %q", outcome, OutcomeHealthCheckFailed)
	}
	if !errors.Is(err, ErrHealthCheckFailed) {
		t.Fatalf("error = %v, want it to wrap ErrHealthCheckFailed", err)
	}
	if !errors.Is(err, errBoom) {
		t.Fatalf("error = %v, want the underlying cause preserved", err)
	}
	if !strings.Contains(out.String(), "no rollback is configured") {
		t.Fatalf("narration = %q, want the missing-rollback warning", out.String())
	}
}

func TestRunManagedHealthCheckFailureRollsBack(t *testing.T) {
	var rolledBack bool
	var out bytes.Buffer

	outcome, err := RunManaged(t.Context(), ManagedConfig{
		Now:         clock(at(10, 0)),
		Upgrade:     func(context.Context) error { return nil },
		HealthCheck: func(context.Context) error { return errBoom },
		Rollback:    func(context.Context) error { rolledBack = true; return nil },
		Stdout:      &out,
	})
	if outcome != OutcomeRolledBack {
		t.Fatalf("outcome = %q, want %q", outcome, OutcomeRolledBack)
	}
	if !rolledBack {
		t.Fatal("the rollback callback should have run")
	}
	// The cause survives a successful rollback: the operator still needs
	// to know why the new build was rejected.
	if !errors.Is(err, ErrHealthCheckFailed) || !errors.Is(err, errBoom) {
		t.Fatalf("error = %v, want the health-check cause preserved after rollback", err)
	}
	if !strings.Contains(out.String(), "rollback complete") {
		t.Fatalf("narration = %q, want a rollback-complete line", out.String())
	}
}

func TestRunManagedRollbackFailure(t *testing.T) {
	rollbackErr := errors.New("cannot restore previous binary")
	var out bytes.Buffer

	outcome, err := RunManaged(t.Context(), ManagedConfig{
		Now:         clock(at(10, 0)),
		Upgrade:     func(context.Context) error { return nil },
		HealthCheck: func(context.Context) error { return errBoom },
		Rollback:    func(context.Context) error { return rollbackErr },
		Stdout:      &out,
	})
	if outcome != OutcomeRollbackFailed {
		t.Fatalf("outcome = %q, want %q", outcome, OutcomeRollbackFailed)
	}
	// Both halves of the story must be recoverable: what broke, and
	// that the revert did not work either.
	if !errors.Is(err, ErrRollbackFailed) {
		t.Fatalf("error = %v, want it to wrap ErrRollbackFailed", err)
	}
	if !errors.Is(err, rollbackErr) || !errors.Is(err, ErrHealthCheckFailed) {
		t.Fatalf("error = %v, want both the rollback and health-check causes", err)
	}
	if !strings.Contains(out.String(), "needs attention") {
		t.Fatalf("narration = %q, want the operator escalation line", out.String())
	}
}

func TestRunManagedIncompleteConfig(t *testing.T) {
	outcome, err := RunManaged(t.Context(), ManagedConfig{Now: clock(at(10, 0))})
	if !errors.Is(err, ErrManagedConfig) {
		t.Fatalf("error = %v, want it to wrap ErrManagedConfig", err)
	}
	if outcome != "" {
		t.Fatalf("outcome = %q, want the empty outcome for a config error", outcome)
	}
}

// TestRunManagedNilHealthCheck covers the caller who deliberately opts
// out of the tripwire.
func TestRunManagedNilHealthCheck(t *testing.T) {
	outcome, err := RunManaged(t.Context(), ManagedConfig{
		Now:     clock(at(10, 0)),
		Upgrade: func(context.Context) error { return nil },
	})
	if err != nil {
		t.Fatalf("RunManaged: %v", err)
	}
	if outcome != OutcomeUpgraded {
		t.Fatalf("outcome = %q, want %q", outcome, OutcomeUpgraded)
	}
}

// TestRunManagedDefaultClock proves the nil Now field falls back to the
// real clock rather than to the zero time, which would sit at midnight
// and defer inside every overnight window.
func TestRunManagedDefaultClock(t *testing.T) {
	var upgraded bool

	outcome, err := RunManaged(t.Context(), ManagedConfig{
		// A one-minute window an hour in the past: whatever "now" is,
		// this window does not contain it.
		Window:  windowAround(time.Now().Add(-1 * time.Hour)),
		Upgrade: func(context.Context) error { upgraded = true; return nil },
	})
	if err != nil {
		t.Fatalf("RunManaged: %v", err)
	}
	if outcome != OutcomeUpgraded || !upgraded {
		t.Fatalf("outcome = %q upgraded = %v, want an upgrade with the real clock", outcome, upgraded)
	}
}

// windowAround returns a one-minute window covering t's minute-of-day.
func windowAround(t time.Time) DrainWindow {
	m := MinuteOfDay(t)
	return DrainWindow{BellMin: m, CloseoutMin: m}
}

// TestRunManagedPassesContext proves the caller's context reaches every
// injected step, so a cancelled supervisor run actually stops.
func TestRunManagedPassesContext(t *testing.T) {
	type ctxKey string
	const key ctxKey = "marker"

	ctx := context.WithValue(t.Context(), key, "present")
	seen := map[string]bool{}

	record := func(name string) func(context.Context) error {
		return func(c context.Context) error {
			seen[name] = c.Value(key) == "present"
			if name == "health" {
				return errBoom
			}
			return nil
		}
	}

	_, _ = RunManaged(ctx, ManagedConfig{
		Now:         clock(at(10, 0)),
		Upgrade:     record("upgrade"),
		HealthCheck: record("health"),
		Rollback:    record("rollback"),
	})

	for _, name := range []string{"upgrade", "health", "rollback"} {
		if !seen[name] {
			t.Fatalf("%s did not receive the caller's context", name)
		}
	}
}

// TestNarrateNilWriter covers the discard path explicitly: a nil Stdout
// is the default for a daemon and must never panic.
func TestNarrateNilWriter(t *testing.T) {
	narrate(nil, "dropped")
}
