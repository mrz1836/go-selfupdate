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
// A leading "v" is ignored and build metadata (after "+") never affects
// ordering. When the numeric tuples are equal, a version carrying a
// prerelease suffix (after "-") sorts strictly below one without, so a
// user on v1.2.0-rc2 is correctly offered the final v1.2.0.
func Compare(a, b string) int {
	devA, devB := IsDevVersion(a), IsDevVersion(b)
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

	// Equal numeric tuples: apply minimal semver precedence so a
	// prerelease sorts below the release it leads up to. Build metadata
	// is ignored, matching the semver spec.
	preA, preB := hasPrerelease(a), hasPrerelease(b)
	switch {
	case preA && !preB:
		return -1
	case preB && !preA:
		return 1
	default:
		return 0
	}
}

// IsNewer reports whether latest is strictly newer than current. A
// malformed latest is never newer.
func IsNewer(current, latest string) bool {
	return Compare(latest, current) > 0
}

// IsDevVersion reports whether v is a development marker: empty, the
// literal "dev", or a bare commit hash. All three sort below any real
// release, so a from-source build is never mistaken for one that outranks
// a published tag.
//
// It is exported because the marker semantics are already a public
// contract behind [Compare] and [IsNewer]: a caller that wants to gate a
// prompt or a passive notice on "is this a real release?" should answer
// the question the same way the ordering does, rather than re-deriving it.
func IsDevVersion(v string) bool {
	clean := strings.TrimPrefix(strings.TrimSpace(v), "v")
	return clean == "" || clean == devVersion || isLikelyCommitHash(clean)
}

// isLikelyCommitHash reports whether s has the shape of a git commit
// hash: 7 to 40 hexadecimal characters, at least one of which is a letter.
//
// The letter requirement is what keeps a purely numeric version scheme —
// a bare build number, or CalVer like 20240101 — out of the
// development-marker branch. Without it, "1234567" would be read as a
// commit hash and therefore as "older than every release", which turns a
// real version into a silent downgrade or a missed upgrade. A genuine
// short or long SHA effectively always contains an a-f digit.
func isLikelyCommitHash(s string) bool {
	if len(s) < commitHashMinLen || len(s) > commitHashMaxLen {
		return false
	}
	hasLetter := false
	for _, c := range s {
		switch {
		case c >= '0' && c <= '9':
		case (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F'):
			hasLetter = true
		default:
			return false
		}
	}
	return hasLetter
}

// hasPrerelease reports whether v carries a semver prerelease suffix — a
// "-" segment such as "-rc.1". Build metadata (after "+") is stripped
// first, because per the semver spec it does not affect precedence.
func hasPrerelease(v string) bool {
	clean := strings.TrimPrefix(strings.TrimSpace(v), "v")
	if plus := strings.IndexByte(clean, '+'); plus >= 0 {
		clean = clean[:plus]
	}
	return strings.IndexByte(clean, '-') >= 0
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
