// Command minimal is a runnable, end-to-end example of wiring this
// library into a cobra CLI. It shows both features a tool gets:
//
//  1. An `update` command (aliased `upgrade`) that resolves the latest
//     GitHub release, verifies its SHA-256 checksum, and atomically
//     replaces the running binary — with --check, --force, and --verbose.
//  2. A passive "a new version is available" banner shown after a command
//     runs, but only once per cache interval, so it neither nags the user
//     nor makes a network call on every invocation.
//
// Build and run it:
//
//	go build ./examples/minimal
//	./minimal version           # prints the version
//	./minimal update --check    # is a newer release available?
//	./minimal update            # download, verify, and install it
//
// The example points at a repository that does not exist, so the update
// paths report that they cannot resolve a release — that is the intended
// outcome; the value here is the wiring. Point Owner and Repo at your own
// project (one that publishes GoReleaser archives plus a checksums.txt)
// and it works for real.
package main

import (
	"fmt"
	"os"
	"time"

	selfupdate "github.com/mrz1836/go-selfupdate"
	"github.com/mrz1836/go-selfupdate/cobracmd"
	"github.com/mrz1836/go-selfupdate/notify"
	"github.com/spf13/cobra"
)

// Identity of the tool being updated. In a real CLI these are constants
// in your root package.
const (
	owner      = "acme"
	repo       = "widget"
	binaryName = "widget"
)

// version is stamped at build time, conventionally with
// -ldflags "-X main.version=v1.2.3". The unstamped "dev" marks a
// development build, which the library never nags and never replaces
// without --force.
var version = "dev"

func main() {
	if err := newRootCommand().Execute(); err != nil {
		os.Exit(1) // cobra has already printed the error
	}
}

// newRootCommand builds the whole CLI: a version command, the update
// command, and the passive cached "new version available" banner.
func newRootCommand() *cobra.Command {
	root := &cobra.Command{
		Use:           binaryName,
		Short:         "A tiny CLI that keeps itself up to date",
		SilenceUsage:  true,
		SilenceErrors: false,
	}

	root.AddCommand(&cobra.Command{
		Use:   "version",
		Short: "Print the version",
		Args:  cobra.NoArgs,
		Run: func(cmd *cobra.Command, _ []string) {
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s %s\n", binaryName, version)
		},
	})

	// (1) The active half: `widget update` (and `widget upgrade`, its
	// alias) with --check, --force, and --verbose.
	root.AddCommand(cobracmd.New(selfupdate.Config{
		Owner:          owner,
		Repo:           repo,
		BinaryName:     binaryName,
		CurrentVersion: version,
	}))

	// (2) The passive half: after any command finishes, print a one-line
	// "a new version is available" banner IF a newer release exists.
	//
	// This is the part that stays out of the way:
	//   - The result is cached under os.UserConfigDir()/widget and is only
	//     re-fetched once per CacheTTL, so back-to-back commands make no
	//     network calls at all. Users can override the cadence at runtime
	//     with WIDGET_UPDATE_CHECK_INTERVAL (a Go duration, e.g. "12h").
	//   - The check runs in a background goroutine and is drained with a
	//     short timeout, so it never delays the command the user ran.
	//   - It stays silent under CI, silent for a development build, and
	//     silent when WIDGET_NO_UPDATE_CHECK or NO_UPDATE_CHECK is set.
	//   - Every error — even a panic — is swallowed, so an update check can
	//     never be the reason the CLI fails.
	cobracmd.AttachBanner(root, notify.Config{
		Owner:          owner,
		Repo:           repo,
		BinaryName:     binaryName,
		CurrentVersion: version,
		CacheTTL:       24 * time.Hour, // how long a check is trusted; 0 also means 24h
	})

	// Shortcut: when the default banner settings are fine, cobracmd.Attach
	// does both calls above from a single config —
	//
	//	cobracmd.Attach(root, selfupdate.Config{
	//	    Owner: owner, Repo: repo, BinaryName: binaryName, CurrentVersion: version,
	//	})

	return root
}
