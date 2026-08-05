package selfupdate

import (
	"errors"
	"runtime"
	"strings"
	"testing"
)

func TestPlatformDefaults(t *testing.T) {
	t.Parallel()

	defaults := DefaultPlatforms()
	if len(defaults) == 0 {
		t.Fatal("DefaultPlatforms returned nothing")
	}

	seen := make(map[string]bool, len(defaults))
	for _, p := range defaults {
		if seen[p.String()] {
			t.Errorf("duplicate platform %s in defaults", p)
		}
		seen[p.String()] = true
	}

	for _, want := range []string{"linux/amd64", "linux/arm64", "darwin/amd64", "darwin/arm64", "windows/amd64", "windows/arm64"} {
		if !seen[want] {
			t.Errorf("default matrix is missing %s", want)
		}
	}
}

func TestPlatformString(t *testing.T) {
	t.Parallel()

	if got := (Platform{OS: "linux", Arch: "arm64"}).String(); got != "linux/arm64" {
		t.Errorf("String() = %q, want %q", got, "linux/arm64")
	}
	if got := CurrentPlatform(); got.OS != runtime.GOOS || got.Arch != runtime.GOARCH {
		t.Errorf("CurrentPlatform() = %v, want %s/%s", got, runtime.GOOS, runtime.GOARCH)
	}
}

func TestPlatformGuard(t *testing.T) {
	t.Parallel()

	current := CurrentPlatform()

	t.Run("nil set falls back to the default matrix", func(t *testing.T) {
		if err := guardPlatform(nil); err != nil {
			t.Errorf("guardPlatform(nil) on %s = %v, want nil", current, err)
		}
	})

	t.Run("current platform is accepted", func(t *testing.T) {
		if err := guardPlatform([]Platform{current}); err != nil {
			t.Errorf("guardPlatform(%v) = %v, want nil", current, err)
		}
	})

	t.Run("narrowed set rejects this platform", func(t *testing.T) {
		narrowed := []Platform{{OS: "plan9", Arch: "mips"}}
		err := guardPlatform(narrowed)
		if !errors.Is(err, ErrUnsupportedPlatform) {
			t.Fatalf("guardPlatform(%v) = %v, want ErrUnsupportedPlatform", narrowed, err)
		}
		if got := err.Error(); !strings.Contains(got, current.String()) {
			t.Errorf("error %q does not name the current platform %s", got, current)
		}
	})

	t.Run("matching os with a different arch is rejected", func(t *testing.T) {
		other := "amd64"
		if current.Arch == "amd64" {
			other = "arm64"
		}
		err := guardPlatform([]Platform{{OS: current.OS, Arch: other}})
		if !errors.Is(err, ErrUnsupportedPlatform) {
			t.Errorf("guardPlatform on mismatched arch = %v, want ErrUnsupportedPlatform", err)
		}
	})
}
