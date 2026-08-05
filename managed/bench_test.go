package managed

import "testing"

// BenchmarkDrainWindowContains measures the midnight-wrapping window check
// a supervisor runs before every managed upgrade attempt.
func BenchmarkDrainWindowContains(b *testing.B) {
	w := DrainWindow{BellMin: 19 * 60, CloseoutMin: 4 * 60}

	for b.Loop() {
		_ = w.Contains(22 * 60)
	}
}
