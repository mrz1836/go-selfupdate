# go-selfupdate

Shared self-update and version-notification for Go command-line tools.

One audited implementation of the thing every CLI ends up writing twice: resolve the
latest GitHub release, download the right asset, **verify its SHA-256**, extract it
safely, and atomically replace the running binary — plus the passive "a new version is
available" banner, and an optional supervised upgrade for a tool that runs as a service.

## Install

```bash
go get github.com/mrz1836/go-selfupdate
```

Requires Go 1.25 or newer.

## Quick start

Two calls wire the whole feature into a cobra CLI:

```go
import (
    selfupdate "github.com/mrz1836/go-selfupdate"
    "github.com/mrz1836/go-selfupdate/cobracmd"
    "github.com/mrz1836/go-selfupdate/notify"
)

// `widget update` — and `widget upgrade`, registered as an alias —
// with --check, --force and --verbose.
root.AddCommand(cobracmd.New(selfupdate.Config{
    Owner:          "acme",
    Repo:           "widget",
    BinaryName:     "widget",
    CurrentVersion: version,
}))

// The passive notice, shown after a command succeeds.
cobracmd.AttachBanner(root, notify.Config{
    Owner:          "acme",
    Repo:           "widget",
    BinaryName:     "widget",
    CurrentVersion: version,
})
```

A complete, buildable program is in [`examples/minimal`](examples/minimal/main.go).

Prefer to drive it yourself? The programmatic API is two functions:

```go
info, err := selfupdate.Check(ctx, cfg)                       // never writes
result, err := selfupdate.Install(ctx, cfg, selfupdate.WithForce())
```

## What happens during an update

The order matters, and it is part of the contract:

1. **Platform guard** — an unsupported `GOOS`/`GOARCH` is refused before an HTTP client
   is even constructed, so a user on a platform you do not publish for pays no network
   round-trip and gets a straight answer.
2. **Managed-install detection** — a binary another installer owns (a package manager's
   cellar, a toolchain `bin` directory) is refused with the command that *does* own it,
   rather than silently overwritten.
3. **Writable-directory probe** — `install dir not writable: <path>` arrives before the
   download, not after it.
4. **Release resolution** — the `gh` CLI first when it is present and authenticated,
   falling back to the GitHub REST API.
5. **Checksum-verified download** — the archive is hashed as it streams and compared
   against the release's `checksums.txt`. A mismatch aborts before anything is written to
   the install path. A release with no checksums file is refused outright.
6. **Guarded extraction** — path traversal (`../`) is rejected, file modes are
   normalized, and a size cap stops a decompression bomb.
7. **Atomic replace** — write `<target>.new`, `fsync`, `chmod`, `rename`, with a
   cross-device copy fallback.

Every stage returns its own sentinel error (`ErrUnsupportedPlatform`, `ErrManagedInstall`,
`ErrInstallDirNotWritable`, `ErrAssetNotFound`, `ErrChecksumMismatch`, …) wrapped with the
concrete path or asset that failed, so `errors.Is` works and the message alone tells a
user what to do next.

## Check vs. Install

| | `Check` | `Install` |
|---|---|---|
| Network | release metadata only | metadata + archive + checksums |
| Writes | none, ever | the target binary, atomically |
| Backing flag | `--check` | the bare command |

`Check` is safe to call from anywhere — a doctor command, a status line, a test. It is
the same call `--check` makes.

## Configuration

`selfupdate.Config` requires only `Owner`, `Repo` and `BinaryName`; everything else has a
production default:

| Field | Default | Notes |
|---|---|---|
| `CurrentVersion` | `dev` | A development build is never replaced without `--force`. |
| `TargetPath` | `os.Executable()` with symlinks resolved | Replaces the real file, not a link to it. |
| `Client` | 5-minute timeout | |
| `TokenEnvVar` | none | Consulted before `GITHUB_TOKEN` and `GH_TOKEN`. |
| `Source` | `gh` CLI, then REST | Any `ReleaseSource` implementation; tests inject a stub. |
| `Platforms` | linux/darwin/windows × amd64/arm64 | Narrow it when you publish fewer. |
| `Stdout` | `os.Stdout` | The command wires this to cobra's stream. |
| `BannerOut` | `os.Stderr` | So a notice never contaminates piped output. |
| `Logger` | `slog.Default()` | |

### Environment variables

Names are derived from the application, so two tools built on this library never fight
over one another's settings. `<APP>` is the uppercased application name.

| Variable | Effect |
|---|---|
| `<APP>_GITHUB_TOKEN`, then `GITHUB_TOKEN`, then `GH_TOKEN` | Authenticates release lookups; raises the rate limit and reaches private repositories. |
| `<APP>_NO_UPDATE_CHECK` | Silences the passive notice for this tool. |
| `NO_UPDATE_CHECK` | Silences it for every tool built on this library. |
| `<APP>_UPDATE_CHECK_INTERVAL` | Overrides the cache TTL, clamped to `[1h, 720h]`. |
| `CI` | Any truthy value disables the passive notice entirely. |
| `NO_COLOR` | Renders the banner without color. |

The notice is **passive-only**. Nothing here ever downloads or installs on its own; an
update happens when a user asks for one.

## Sub-packages

### `notify/` — the passive notice

A TTL-cached check plus the banner that reports it. It is built to be ignorable:
`StartBackgroundCheck` swallows every error, including panics, so an update check can
never be the reason a CLI fails. The cache is written atomically and lives under
`os.UserConfigDir()/<app>` by default — pass `CacheDir` to keep a location your tool
already uses.

```go
result := notify.Check(ctx, cfg)   // cached
notify.ShowBanner(cfg, result)     // silent unless an update exists
```

### `managed/` — supervised upgrades

For a tool that runs as a long-lived service, where "replace the binary now" is the wrong
answer. `RunManaged` defers inside a caller-supplied drain window, runs a post-upgrade
health check, and reports a rollback outcome when that check fails.

```go
window, err := managed.NewDrainWindow("22:00", "02:00") // wraps midnight

outcome, err := managed.RunManaged(ctx, managed.ManagedConfig{
    Window:      window,
    Upgrade:     func(context.Context) error { /* … */ },
    HealthCheck: func(context.Context) error { /* … */ },
    Rollback:    func(context.Context) error { /* … */ },
})
```

A deferral is not an error: `RunManaged` returns `OutcomeDeferred` with a nil error when
the clock is inside the window, so a supervisor can treat it as success and retry later.

The core package does not import `managed/`; the dependency runs one way only.

### `cobracmd/` — the drop-in command

`New` builds the command, `AttachBanner` wires the notice. Both are shown in
[Quick start](#quick-start).

## Release conventions

The library expects the layout GoReleaser produces by default:

- Archives named `<project>_<version>_<os>_<arch>.tar.gz`, for **every** OS including
  Windows.
- A SHA-256 checksums asset, conventionally `<project>_<version>_checksums.txt`, in the
  standard `<hex>  <filename>` format.
- Plain `x.y.z` releases published as *latest*.

The checksum **filename** is matched leniently — any asset ending in `checksums.txt` will
do, since a project that leaves the checksum block at its default still produces one. The
checksum **value** is not lenient: missing, unparseable, or absent-for-this-asset is a
hard failure, never a warning.

## Design decisions

Where the implementations this library replaces disagreed, the stronger one won. The
choices worth knowing about:

**Release-binary install only — there is no toolchain fallback.** This is the one
decision that changes behavior for a tool adopting the library, so it is stated plainly:
a `go install …@vX` route is not offered, and no flag re-enables it. The reason is
mechanical rather than stylistic — a single `replace` directive in a consuming module
makes a versioned module query impossible, so for exactly the projects that would need a
fallback, the fallback cannot fire. It was dead code posing as a safety net. A guard test
fails the build if the route ever reappears.

The consequence is that the single remaining route has to be legible when it fails, which
is why the three pre-network gates above are mandatory and why every stage has its own
error. It also means **the first install is a release-asset download** — fetch the archive
for your platform from the releases page, verify it against `checksums.txt`, and put the
binary on your `PATH`. After that, the tool updates itself.

**Atomic `.new` + rename, not a `.backup` dance.** Some implementations move the current
binary aside and then write the new one, which leaves a window where the command does not
exist at all. Writing a sibling file and renaming it over the target closes that window:
the binary is either the old one or the new one, never missing.

**Conservative version comparison.** A version that does not parse is treated as *not
newer*. The failure mode of the alternative is a tool that reinstalls itself forever.

**Latest only — no release channels.** No `stable`/`beta`/`edge` selection, because
carrying channel machinery for projects that only ever publish `x.y.z` is complexity with
no caller. Adding it later is additive and non-breaking.

**`tar.gz` only.** No `.zip` handling, for the same reason: nothing in scope produces one.

**Every seam is a `Config` field, not a package-level variable.** A library is imported by
whoever wants it, so process-global mutable state would let one consumer's tests reach
into another's cache.

## Contributing

Issues and pull requests are welcome. Run `go test ./... -race` before opening one.
