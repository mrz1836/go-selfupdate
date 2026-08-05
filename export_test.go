package selfupdate

// This file bridges the unexported surface the black-box package
// selfupdate_test needs: the development-marker constant, the config
// normalizer, and the OS seam the Windows write-gate reads. Keeping these
// in an export_test.go file means they exist only under `go test` and
// never widen the public API.

// DevVersion exposes the development-marker constant to black-box tests.
const DevVersion = devVersion

// NormalizeConfig exposes Config.normalize so a black-box test can assert
// on the defaults the library fills in without reaching into the package.
func NormalizeConfig(c Config) (Config, error) { return c.normalize() }

// SetGOOS overrides the operating system the install write-path gates on,
// returning a function that restores the previous value. It lets a
// black-box test exercise the Windows gate on any host, and — because it
// mutates a package global — its callers must run serially.
func SetGOOS(v string) (restore func()) {
	prev := goos
	goos = v
	return func() { goos = prev }
}
