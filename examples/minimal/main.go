// Command minimal is a runnable, end-to-end example of wiring this
// library into a cobra CLI.
//
// Everything a tool needs is here: an update command with the standard
// flags, and the passive banner that tells the user a new release
// exists. Build and run it:
//
//	go build ./examples/minimal
//	./minimal version
//	./minimal update --check
//
// The example points at a repository that does not exist, so `update`
// will report that it cannot resolve a release. That is the intended
// outcome — the value here is the wiring, not the download. Point Owner
// and Repo at your own project and it works.
package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	selfupdate "github.com/mrz1836/go-selfupdate"
	"github.com/mrz1836/go-selfupdate/cobracmd"
	"github.com/mrz1836/go-selfupdate/notify"
)

// Identity of the tool being updated. In a real CLI these are constants
// in your root package.
const (
	owner      = "acme"
	repo       = "widget"
	binaryName = "widget"
)

// version is stamped at build time, conventionally with
// -ldflags "-X main.version=v1.2.3". The unstamped value marks a
// development build, which the library never nags and never replaces
// without --force.
var version = "dev"

func main() {
	if err := newRootCommand().Execute(); err != nil {
		// cobra has already reported the error.
		os.Exit(1)
	}
}

// newRootCommand builds the whole CLI: two commands of its own, plus the
// update feature in two calls.
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

	// The active half: `widget update` (and `widget upgrade`, which is
	// registered as an alias) with --check, --force, and --verbose.
	root.AddCommand(cobracmd.New(selfupdate.Config{
		Owner:          owner,
		Repo:           repo,
		BinaryName:     binaryName,
		CurrentVersion: version,
	}))

	// The passive half: a cached check that prints a banner when a newer
	// release exists. It is silent under CI, silent for a development
	// build, silent when WIDGET_NO_UPDATE_CHECK or NO_UPDATE_CHECK is
	// set, and silent whenever the check fails or has not finished.
	cobracmd.AttachBanner(root, notify.Config{
		Owner:          owner,
		Repo:           repo,
		BinaryName:     binaryName,
		CurrentVersion: version,
	})

	return root
}
