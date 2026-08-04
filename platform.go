package selfupdate

import (
	"fmt"
	"runtime"
)

// Platform is one GOOS/GOARCH pair a caller publishes release assets
// for.
type Platform struct {
	OS   string
	Arch string
}

// String renders the platform in the conventional "os/arch" form.
func (p Platform) String() string { return p.OS + "/" + p.Arch }

// CurrentPlatform returns the platform of the running binary.
func CurrentPlatform() Platform {
	return Platform{OS: runtime.GOOS, Arch: runtime.GOARCH}
}

// DefaultPlatforms returns the release matrix the tools consuming this
// library publish by default: linux, darwin, and windows on amd64 and
// arm64.
//
// A caller that ships fewer platforms narrows this through
// Config.Platforms rather than editing the library — a tool with
// Unix-only dependencies, for example, passes just linux and darwin so
// an unsupported user gets a clear refusal instead of a confusing
// "no matching release asset".
func DefaultPlatforms() []Platform {
	return []Platform{
		{OS: "linux", Arch: "amd64"},
		{OS: "linux", Arch: "arm64"},
		{OS: "darwin", Arch: "amd64"},
		{OS: "darwin", Arch: "arm64"},
		{OS: "windows", Arch: "amd64"},
		{OS: "windows", Arch: "arm64"},
	}
}

// guardPlatform reports whether the running GOOS/GOARCH appears in the
// supported set, returning [ErrUnsupportedPlatform] when it does not. A
// nil or empty set falls back to [DefaultPlatforms].
//
// This runs as the first statement of Check and Install, before an HTTP
// client is even constructed, so an unsupported platform costs no
// network round-trip and leaks no request.
func guardPlatform(supported []Platform) error {
	if len(supported) == 0 {
		supported = DefaultPlatforms()
	}

	current := CurrentPlatform()
	for _, p := range supported {
		if p.OS == current.OS && p.Arch == current.Arch {
			return nil
		}
	}
	return fmt.Errorf("%w: %s", ErrUnsupportedPlatform, current)
}
