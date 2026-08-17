// Package cobracmd is the drop-in wiring between a cobra CLI and the
// self-update library: one call registers the update command, a second
// registers the passive "a new version is available" notice.
//
// The point of this package is that adopting the library should be a
// diff that deletes code. A tool that already ships its own updater has
// somewhere between two hundred and five hundred lines of release
// lookup, archive extraction, and binary replacement; the replacement is
// this:
//
//	root.AddCommand(cobracmd.New(selfupdate.Config{
//	    Owner:          "acme",
//	    Repo:           "widget",
//	    BinaryName:     "widget",
//	    CurrentVersion: version,
//	}))
//	cobracmd.AttachBanner(root, notify.Config{
//	    Owner:          "acme",
//	    Repo:           "widget",
//	    BinaryName:     "widget",
//	    CurrentVersion: version,
//	})
//
// The flag set is deliberately the one the existing tools already
// expose — --force, --check, --verbose — so adopting the library is not
// a user-visible change. The one flag that does change meaning,
// --use-binary, is handled by [WithDeprecatedUseBinaryFlag]: this
// library installs from release archives and nothing else, so the flag
// no longer selects anything, but silently rejecting an argument that
// worked yesterday would break scripts for no benefit.
package cobracmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	selfupdate "github.com/mrz1836/go-selfupdate"
	"github.com/mrz1836/go-selfupdate/notify"
	"github.com/spf13/cobra"
)

// Command naming and flag names. The flag set mirrors what the fleet's
// hand-rolled updaters already register, so an adopting tool's users see
// no change.
const (
	// defaultUse is the command name used when the caller expresses no
	// preference.
	defaultUse = "update"
	// alternateUse is the other name the same feature ships under, added
	// as an alias by default so both spellings work out of the box.
	alternateUse = "upgrade"

	flagForce     = "force"
	flagCheck     = "check"
	flagVerbose   = "verbose"
	flagUseBinary = "use-binary"
)

// Annotation marking a command built by [New]. It exists so
// [AttachBanner] can stay quiet during an update: telling someone a new
// version is available immediately after they installed it is noise.
const (
	annotationKey   = "github.com/mrz1836/go-selfupdate"
	annotationValue = "update-command"
)

// bannerDrainTimeout bounds how long the process waits at exit for a
// background check that has not finished. A version notice is worth a
// fraction of a second and no more — a CLI that lingers on exit to
// deliver an advertisement is a worse tool than one that stays silent.
const bannerDrainTimeout = 750 * time.Millisecond

// cmdOptions holds the caller's command-shape choices.
type cmdOptions struct {
	use           string
	aliases       []string
	aliasesSet    bool
	short         string
	long          string
	useBinaryFlag bool
}

// CmdOption adjusts the command returned by [New].
type CmdOption func(*cmdOptions)

// WithUse sets the command's name. The fleet is split — some tools call
// it "update", others "upgrade" — so the name is the caller's to pick.
// The counterpart name remains available as an alias unless
// [WithAliases] says otherwise.
func WithUse(use string) CmdOption {
	return func(o *cmdOptions) {
		if use != "" {
			o.use = use
		}
	}
}

// WithAliases replaces the command's aliases. Passing no arguments
// removes the default alias, for a tool whose CLI already means
// something else by the counterpart name.
func WithAliases(aliases ...string) CmdOption {
	return func(o *cmdOptions) {
		o.aliases = aliases
		o.aliasesSet = true
	}
}

// WithShort overrides the one-line help text.
func WithShort(short string) CmdOption {
	return func(o *cmdOptions) { o.short = short }
}

// WithLong overrides the extended help text.
func WithLong(long string) CmdOption {
	return func(o *cmdOptions) { o.long = long }
}

// WithDeprecatedUseBinaryFlag registers a hidden, inert --use-binary
// flag.
//
// Some tools in the fleet ship that flag today to choose between
// downloading a release archive and building through the toolchain. This
// library offers only the first route, so the flag has nothing left to
// select. It is still accepted, because the alternative is an error for
// every script and shell alias that passes it, and a flag that fails
// loudly teaches the user nothing they can act on. It is hidden rather
// than documented so nobody learns it from --help, and it changes no
// behavior — a fact pinned by a test rather than by this comment.
func WithDeprecatedUseBinaryFlag() CmdOption {
	return func(o *cmdOptions) { o.useBinaryFlag = true }
}

// New returns the drop-in update command for cfg.
//
// cfg is the same [selfupdate.Config] the programmatic API takes; only
// Owner, Repo, and BinaryName are required. Config.Stdout is left to the
// command when nil, so output follows cobra's own stream and a test can
// capture it with cmd.SetOut.
func New(cfg selfupdate.Config, opts ...CmdOption) *cobra.Command {
	o := cmdOptions{use: defaultUse}
	for _, opt := range opts {
		if opt != nil {
			opt(&o)
		}
	}
	if !o.aliasesSet {
		o.aliases = defaultAliases(o.use)
	}

	cmd := &cobra.Command{
		Use:         o.use,
		Aliases:     o.aliases,
		Short:       shortOrDefault(o.short, cfg),
		Long:        longOrDefault(o.long, cfg),
		Args:        cobra.NoArgs,
		Annotations: map[string]string{annotationKey: annotationValue},
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runUpdate(cmd, cfg)
		},
	}

	flags := cmd.Flags()
	flags.BoolP(flagForce, "f", false, "Install the latest release even when it is not newer than the running version")
	flags.BoolP(flagCheck, "c", false, "Report whether an update is available without installing anything")
	flags.BoolP(flagVerbose, "v", false, "Narrate each step, and print the release notes with --check")

	if o.useBinaryFlag {
		flags.Bool(flagUseBinary, false, "Deprecated: accepted for compatibility and ignored")
		// MarkHidden fails only for a flag that was never registered.
		_ = flags.MarkHidden(flagUseBinary)
	}

	return cmd
}

// Attach is the one-call adoption path: it registers the update command
// on root and wires the passive "a new version is available" banner, both
// derived from a single [selfupdate.Config], so a tool states its
// identity — owner, repo, binary name, version — exactly once.
//
// It is precisely [New] + root.AddCommand + [AttachBanner], with the
// notifier's identity, release source, HTTP client, and token variable
// carried over from the same Config. Reach for New and AttachBanner
// directly when the command and the banner must differ — a separate cache
// directory, a custom banner stream, a different upgrade command string;
// Attach covers the common case where they do not. The registered command
// is returned for any further adjustment.
func Attach(root *cobra.Command, cfg selfupdate.Config, opts ...CmdOption) *cobra.Command {
	cmd := New(cfg, opts...)
	if root != nil {
		root.AddCommand(cmd)
	}
	AttachBanner(root, notify.Config{
		Owner:          cfg.Owner,
		Repo:           cfg.Repo,
		BinaryName:     cfg.BinaryName,
		CurrentVersion: cfg.CurrentVersion,
		Source:         cfg.Source,
		Client:         cfg.Client,
		TokenEnvVar:    cfg.TokenEnvVar,
	})
	return cmd
}

// defaultAliases returns the counterpart spelling of use, so a tool gets
// both "update" and "upgrade" without asking.
func defaultAliases(use string) []string {
	switch use {
	case defaultUse:
		return []string{alternateUse}
	case alternateUse:
		return []string{defaultUse}
	default:
		return nil
	}
}

// shortOrDefault returns the caller's one-line help, or a derived one.
func shortOrDefault(short string, cfg selfupdate.Config) string {
	if short != "" {
		return short
	}
	return "Update " + displayName(cfg) + " to the latest release"
}

// longOrDefault returns the caller's extended help, or a derived one
// that states what the command will and will not do to the user's
// machine.
func longOrDefault(long string, cfg selfupdate.Config) string {
	if long != "" {
		return long
	}
	name := displayName(cfg)
	return "Download the latest " + name + " release, verify its SHA-256 checksum against the\n" +
		"published checksums file, and atomically replace the running binary.\n\n" +
		"Nothing is written until the download has been verified. A binary that another\n" +
		"installer owns is refused rather than overwritten, with instructions for the\n" +
		"installer that owns it."
}

// displayName is the name to call the tool in user-facing output.
func displayName(cfg selfupdate.Config) string {
	if cfg.BinaryName != "" {
		return cfg.BinaryName
	}
	return cfg.Repo
}

// runUpdate reads the flags and dispatches to the check or install path.
func runUpdate(cmd *cobra.Command, cfg selfupdate.Config) error {
	force, err := boolFlag(cmd, flagForce)
	if err != nil {
		return err
	}
	checkOnly, err := boolFlag(cmd, flagCheck)
	if err != nil {
		return err
	}
	verbose, err := boolFlag(cmd, flagVerbose)
	if err != nil {
		return err
	}

	// A caller who set Config.Stdout meant it; otherwise follow cobra,
	// which is what makes the command's output capturable in a test.
	if cfg.Stdout == nil {
		cfg.Stdout = cmd.OutOrStdout()
	}

	if checkOnly {
		return reportCheck(commandContext(cmd), cmd, cfg, verbose)
	}
	return install(commandContext(cmd), cfg, force, verbose)
}

// reportCheck resolves the latest release and reports the comparison. It
// writes nothing to the install path: --check is a question, not an
// instruction.
func reportCheck(ctx context.Context, cmd *cobra.Command, cfg selfupdate.Config, verbose bool) error {
	info, err := selfupdate.Check(ctx, cfg)
	if err != nil {
		// Check returns a populated Info alongside ErrAssetNotFound: a
		// newer release exists, this platform just has nothing to
		// download. Report the version and point at the releases page
		// rather than dead-ending on a bare error.
		if errors.Is(err, selfupdate.ErrAssetNotFound) && info != nil && info.LatestVersion != "" {
			return reportMissingAsset(cfg.Stdout, cfg, info)
		}
		return err
	}

	out := cfg.Stdout
	if !info.UpdateAvailable {
		_, _ = fmt.Fprintf(out, "%s is up to date (%s)\n", displayName(cfg), info.CurrentVersion)
		warnIfNotInstallable(out, cfg)
		return nil
	}

	printAvailable(out, cfg, info)
	_, _ = fmt.Fprintf(out, "Run %q to install it.\n", cmd.CommandPath())
	if verbose {
		if info.ReleaseURL != "" {
			_, _ = fmt.Fprintf(out, "Release: %s\n", info.ReleaseURL)
		}
		if info.ReleaseNotes != "" {
			_, _ = fmt.Fprintf(out, "\nRelease notes for %s:\n%s\n", info.LatestVersion, info.ReleaseNotes)
		}
	}
	warnIfNotInstallable(out, cfg)
	return nil
}

// warnIfNotInstallable cautions when the running binary's location would
// block an in-place update, so a check from a read-only directory does not
// report "up to date" on a binary that update could never replace — the
// failure would otherwise surface only once a release actually shipped.
//
// Only the writability guard is surfaced. A binary another installer owns
// (a go-install build, a Homebrew binary) sits in a directory the user can
// write, so it does not trip this warning; that case is a deliberate
// choice with its own upgrade path, and the update command reports it
// directly when run. The message reuses the library's own error text — the
// same guidance the install path gives — minus the package prefix.
func warnIfNotInstallable(out io.Writer, cfg selfupdate.Config) {
	err := selfupdate.InstallPreflight(cfg)
	if !errors.Is(err, selfupdate.ErrInstallDirNotWritable) {
		return
	}
	_, _ = fmt.Fprintf(out, "warning: %s\n", strings.TrimPrefix(err.Error(), "go-selfupdate: "))
}

// printAvailable writes the one-line "a new version is available"
// transition shared by the check and missing-asset reports.
func printAvailable(out io.Writer, cfg selfupdate.Config, info *selfupdate.Info) {
	_, _ = fmt.Fprintf(out, "A new version of %s is available: %s -> %s\n",
		displayName(cfg), info.CurrentVersion, info.LatestVersion)
}

// reportMissingAsset renders the "a newer release exists, but not for
// your platform" case: name the version, then send the user somewhere
// they can act — the release page when known, the releases list otherwise.
func reportMissingAsset(out io.Writer, cfg selfupdate.Config, info *selfupdate.Info) error {
	printAvailable(out, cfg, info)
	_, _ = fmt.Fprintf(out, "This release has no prebuilt binary for %s. ", selfupdate.CurrentPlatform())
	if info.ReleaseURL != "" {
		_, _ = fmt.Fprintf(out, "Download it from %s\n", info.ReleaseURL)
	} else {
		_, _ = fmt.Fprintln(out, "Check the project's releases page.")
	}
	return nil
}

// install runs the real update and confirms what changed. The version
// transition line is printed by the library itself, so the only thing
// left to say is that the replacement finished.
func install(ctx context.Context, cfg selfupdate.Config, force, verbose bool) error {
	opts := make([]selfupdate.Option, 0, 2)
	if force {
		opts = append(opts, selfupdate.WithForce())
	}
	if verbose {
		opts = append(opts, selfupdate.WithVerbose())
	}

	result, err := selfupdate.Install(ctx, cfg, opts...)
	if err != nil {
		return err
	}
	if result.Updated {
		_, _ = fmt.Fprintf(cfg.Stdout, "Updated %s to %s\n", displayName(cfg), result.LatestVersion)
	}
	return nil
}

// boolFlag reads a registered boolean flag, naming the flag if the read
// somehow fails.
func boolFlag(cmd *cobra.Command, name string) (bool, error) {
	value, err := cmd.Flags().GetBool(name)
	if err != nil {
		return false, fmt.Errorf("go-selfupdate/cobracmd: read --%s: %w", name, err)
	}
	return value, nil
}

// commandContext returns the command's context, tolerating a command
// invoked directly rather than through Execute.
func commandContext(cmd *cobra.Command) context.Context {
	if cmd != nil {
		if ctx := cmd.Context(); ctx != nil {
			return ctx
		}
	}
	return context.Background()
}

// AttachBanner wires the passive update notice into root: the check
// starts before the command runs and the banner is written after it
// succeeds, so the network round-trip overlaps with the work the user
// actually asked for.
//
// Nothing here can fail a command. A disabled check, a development
// build, a CI environment, an unreachable API, or a check that simply
// has not finished within [bannerDrainTimeout] all produce silence.
//
// Two behaviors are worth knowing. The banner stays quiet during the
// update command itself, since a notice about the version you are
// installing is noise. And cobra runs only the closest persistent hook
// it finds, so a subcommand that defines its own PersistentPreRunE
// shadows this one: either chain it yourself or set
// cobra.EnableTraverseRunHooks. Existing hooks on root are chained, not
// replaced.
func AttachBanner(root *cobra.Command, cfg notify.Config) {
	if root == nil {
		return
	}
	hook := &bannerHook{cfg: cfg}

	prevPreE, prevPre := root.PersistentPreRunE, root.PersistentPreRun
	root.PersistentPreRun = nil
	root.PersistentPreRunE = func(cmd *cobra.Command, args []string) error {
		hook.start(cmd)
		switch {
		case prevPreE != nil:
			return prevPreE(cmd, args)
		case prevPre != nil:
			prevPre(cmd, args)
		}
		return nil
	}

	prevPostE, prevPost := root.PersistentPostRunE, root.PersistentPostRun
	root.PersistentPostRun = nil
	root.PersistentPostRunE = func(cmd *cobra.Command, args []string) error {
		var err error
		switch {
		case prevPostE != nil:
			err = prevPostE(cmd, args)
		case prevPost != nil:
			prevPost(cmd, args)
		}
		if err != nil {
			return err
		}
		hook.show(cmd)
		return nil
	}
}

// bannerHook carries the in-flight check between the pre-run and
// post-run hooks. The mutex is not decoration: a caller may execute the
// same root command from more than one goroutine in a test, and a data
// race in an update notice would be an absurd way to fail a build.
type bannerHook struct {
	cfg notify.Config
	mu  sync.Mutex
	ch  <-chan *notify.Result
}

// start launches the background check unless the update command itself
// is running.
func (h *bannerHook) start(cmd *cobra.Command) {
	if isUpdateCommand(cmd) {
		return
	}
	ch := notify.StartBackgroundCheck(commandContext(cmd), h.cfg)

	h.mu.Lock()
	h.ch = ch
	h.mu.Unlock()
}

// show drains the check and writes the banner, giving up quickly if the
// result is not ready.
func (h *bannerHook) show(cmd *cobra.Command) {
	if isUpdateCommand(cmd) {
		return
	}

	h.mu.Lock()
	ch := h.ch
	h.ch = nil
	h.mu.Unlock()

	if ch == nil {
		return
	}

	timer := time.NewTimer(bannerDrainTimeout)
	defer timer.Stop()

	select {
	case result := <-ch:
		// A closed channel yields nil here, which ShowBanner treats as
		// "nothing to say" — the silent path for a check that was
		// disabled or failed.
		notify.ShowBanner(h.cfg, result)
	case <-timer.C:
	}
}

// isUpdateCommand reports whether cmd, or any command containing it, was
// built by [New].
func isUpdateCommand(cmd *cobra.Command) bool {
	for c := cmd; c != nil; c = c.Parent() {
		if c.Annotations[annotationKey] == annotationValue {
			return true
		}
	}
	return false
}
