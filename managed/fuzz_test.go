package managed

import "testing"

// FuzzParseHM asserts that parseHM never panics and, when it accepts a
// mark, always returns a minute-of-day inside [0, 1440). A value outside
// that range would break the drain-window arithmetic that decides whether
// an unattended upgrade runs.
func FuzzParseHM(f *testing.F) {
	for _, s := range []string{
		"09:00", "00:00", "23:59", "24:00", "01:60", "-1:00",
		"0900", "09:00:00", "ab:cd", "", "1:05", " 9:9 ", ":", "12:",
	} {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, s string) {
		m, err := parseHM(s)
		if err != nil {
			return
		}
		if m < 0 || m >= minutesPerDay {
			t.Errorf("parseHM(%q) = %d, want within [0, %d)", s, m, minutesPerDay)
		}
	})
}
