package selfupdate

import (
	"fmt"
	"strconv"
	"strings"
)

// devVersion is the conventional version string stamped into a build
// that was not produced from a release tag.
const devVersion = "dev"

// commitHashMinLen and commitHashMaxLen bound the "bare commit hash"
// shape recognized as a development marker: a short 7-character SHA
// through a full 40-character one.
const (
	commitHashMinLen = 7
	commitHashMaxLen = 40
)

// Compare returns 1 when a is newer than b, -1 when a is older, and 0
// when the two are equivalent or cannot be ordered.
//
// Two rules govern the edges:
//
//   - A development marker — an empty string, the literal "dev", or a
//     bare commit hash — sorts below every non-marker value, whether or
//     not that value parses. A build carrying no version has nothing to
//     compare against and nothing to lose by taking whatever the project
//     published, so an unusual tag scheme must not strand it.
//   - Two values that are neither development markers nor parseable
//     major.minor.patch tuples compare equal. Refusing to order two real
//     versions we cannot read is what keeps the library from ever
//     announcing an upgrade it cannot justify.
//
// A leading "v" is ignored, and any prerelease or build suffix (after
// "-" or "+") is dropped before the numeric comparison.
func Compare(a, b string) int {
	devA, devB := isDevVersion(a), isDevVersion(b)
	switch {
	case devA && devB:
		return 0
	case devA:
		return -1
	case devB:
		return 1
	}

	tupleA, errA := parseVersionTuple(a)
	tupleB, errB := parseVersionTuple(b)
	if errA != nil || errB != nil {
		// Unparseable and not a development marker: no evidence either
		// way, so report no ordering rather than guessing.
		return 0
	}

	for i := range 3 {
		switch {
		case tupleA[i] > tupleB[i]:
			return 1
		case tupleA[i] < tupleB[i]:
			return -1
		}
	}
	return 0
}

// IsNewer reports whether latest is strictly newer than current. A
// malformed latest is never newer.
func IsNewer(current, latest string) bool {
	return Compare(latest, current) > 0
}

// isDevVersion reports whether v is a development marker: empty, the
// literal "dev", or a bare commit hash. All three sort below any real
// release.
func isDevVersion(v string) bool {
	clean := strings.TrimPrefix(strings.TrimSpace(v), "v")
	return clean == "" || clean == devVersion || isLikelyCommitHash(clean)
}

// isLikelyCommitHash reports whether s has the shape of a git commit
// hash: 7 to 40 hexadecimal characters and nothing else.
func isLikelyCommitHash(s string) bool {
	if len(s) < commitHashMinLen || len(s) > commitHashMaxLen {
		return false
	}
	for _, c := range s {
		isHex := (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
		if !isHex {
			return false
		}
	}
	return true
}

// parseVersionTuple extracts the major.minor.patch components of a
// version string, stripping a leading "v" and any prerelease or build
// suffix. Anything that is not exactly three integer components is an
// error; callers translate that into a conservative "not newer".
func parseVersionTuple(v string) ([3]int, error) {
	clean := strings.TrimPrefix(strings.TrimSpace(v), "v")
	if idx := strings.IndexAny(clean, "-+"); idx >= 0 {
		clean = clean[:idx]
	}

	parts := strings.Split(clean, ".")
	if len(parts) != 3 {
		return [3]int{}, fmt.Errorf("%w: %q", errInvalidSemverTuple, v)
	}

	var out [3]int
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return [3]int{}, fmt.Errorf("%w: %q", errInvalidSemverTuple, v)
		}
		out[i] = n
	}
	return out, nil
}
