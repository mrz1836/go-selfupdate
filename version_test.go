package selfupdate

import "testing"

func TestVersionCompare(t *testing.T) {
	tests := []struct {
		name string
		a    string
		b    string
		want int
	}{
		{name: "equal plain", a: "1.2.3", b: "1.2.3", want: 0},
		{name: "equal with v prefix on one side", a: "v1.2.3", b: "1.2.3", want: 0},
		{name: "major newer", a: "2.0.0", b: "1.9.9", want: 1},
		{name: "minor newer", a: "1.3.0", b: "1.2.9", want: 1},
		{name: "patch newer", a: "1.2.4", b: "1.2.3", want: 1},
		{name: "older", a: "1.2.3", b: "1.2.4", want: -1},
		{name: "prerelease sorts below its release", a: "1.2.3-rc1", b: "1.2.3", want: -1},
		{name: "release sorts above its prerelease", a: "1.2.3", b: "1.2.3-rc1", want: 1},
		{name: "two prereleases of one version are unordered", a: "1.2.3-rc1", b: "1.2.3-rc2", want: 0},
		{name: "build suffix does not affect precedence", a: "1.2.3+build9", b: "1.2.3", want: 0},
		{name: "prerelease with build metadata still sorts below release", a: "1.2.3-rc1+build9", b: "1.2.3", want: -1},
		{name: "dev is older than a release", a: "dev", b: "0.0.1", want: -1},
		{name: "release is newer than dev", a: "0.0.1", b: "dev", want: 1},
		{name: "empty is a dev marker", a: "", b: "1.0.0", want: -1},
		{name: "commit hash is a dev marker", a: "a1b2c3d", b: "1.0.0", want: -1},
		{name: "full commit hash is a dev marker", a: "0123456789abcdef0123456789abcdef01234567", b: "1.0.0", want: -1},
		{name: "two dev markers are equivalent", a: "dev", b: "", want: 0},
		{name: "garbage is unordered against a release", a: "not-a-version", b: "1.0.0", want: 0},
		{name: "a dev marker sorts below even an unparseable value", a: "not-a-version", b: "dev", want: 1},
		{name: "two-part version is unordered", a: "1.2", b: "1.0.0", want: 0},
		{name: "negative component is unordered", a: "1.-2.3", b: "1.0.0", want: 0},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := Compare(tc.a, tc.b); got != tc.want {
				t.Errorf("Compare(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
			}
		})
	}
}

func TestVersionIsNewer(t *testing.T) {
	tests := []struct {
		name    string
		current string
		latest  string
		want    bool
	}{
		{name: "newer patch", current: "1.2.3", latest: "1.2.4", want: true},
		{name: "same version", current: "1.2.3", latest: "1.2.3", want: false},
		{name: "older latest", current: "1.2.4", latest: "1.2.3", want: false},
		{name: "dev build sees any release as newer", current: "dev", latest: "0.0.1", want: true},
		{name: "commit-hash build sees a release as newer", current: "deadbeef", latest: "0.0.1", want: true},
		{name: "malformed latest is never newer", current: "1.2.3", latest: "garbage", want: false},
		{name: "a dev build accepts an unusually tagged release", current: "dev", latest: "2026-08-release", want: true},
		{name: "v prefixes are ignored", current: "v1.0.0", latest: "v1.0.1", want: true},
		{name: "a prerelease user is offered the final release", current: "v1.2.0-rc2", latest: "v1.2.0", want: true},
		{name: "the final release does not re-offer a prerelease", current: "v1.2.0", latest: "v1.2.0-rc2", want: false},
		// A purely numeric build id must not be read as a dev marker: if it
		// were, the older 1.0.0 would look "newer" and trigger a downgrade.
		{name: "a numeric build id never downgrades to an older release", current: "1234567", latest: "1.0.0", want: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsNewer(tc.current, tc.latest); got != tc.want {
				t.Errorf("IsNewer(%q, %q) = %v, want %v", tc.current, tc.latest, got, tc.want)
			}
		})
	}
}

func TestVersionParseTuple(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    [3]int
		wantErr bool
	}{
		{name: "plain", in: "1.2.3", want: [3]int{1, 2, 3}},
		{name: "v prefix", in: "v10.20.30", want: [3]int{10, 20, 30}},
		{name: "whitespace", in: "  1.0.0 ", want: [3]int{1, 0, 0}},
		{name: "prerelease suffix", in: "1.2.3-beta.1", want: [3]int{1, 2, 3}},
		{name: "build suffix", in: "1.2.3+meta", want: [3]int{1, 2, 3}},
		{name: "too few parts", in: "1.2", wantErr: true},
		{name: "too many parts", in: "1.2.3.4", wantErr: true},
		{name: "non numeric", in: "1.x.3", wantErr: true},
		{name: "empty", in: "", wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseVersionTuple(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("parseVersionTuple(%q) = %v, want error", tc.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseVersionTuple(%q) unexpected error: %v", tc.in, err)
			}
			if got != tc.want {
				t.Errorf("parseVersionTuple(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestVersionIsLikelyCommitHash(t *testing.T) {
	tests := []struct {
		in   string
		want bool
	}{
		{in: "a1b2c3d", want: true},
		{in: "0123456789abcdef0123456789abcdef01234567", want: true},
		{in: "ABCDEF1", want: true},
		{in: "a1b2c3", want: false}, // too short
		{in: "deadbeefg", want: false},
		{in: "1.2.3", want: false},
		{in: "", want: false},
		{in: "1234567", want: false},                                   // all digits: a build number, not a hash
		{in: "20240101", want: false},                                  // CalVer, not a hash
		{in: "0123456789abcdef0123456789abcdef012345678", want: false}, // too long
	}

	for _, tc := range tests {
		t.Run(tc.in, func(t *testing.T) {
			if got := isLikelyCommitHash(tc.in); got != tc.want {
				t.Errorf("isLikelyCommitHash(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}
