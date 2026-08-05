<div align="center">

# 🔄&nbsp;&nbsp;go-selfupdate

**Self-update and version notifications for Go CLIs — checksum-verified, atomically installed, wired in one call**

<br/>

<a href="https://github.com/mrz1836/go-selfupdate/releases"><img src="https://img.shields.io/github/release-pre/mrz1836/go-selfupdate?include_prereleases&style=flat-square&logo=github&color=black" alt="Release"></a>
<a href="https://golang.org/"><img src="https://img.shields.io/github/go-mod/go-version/mrz1836/go-selfupdate?style=flat-square&logo=go&color=00ADD8" alt="Go Version"></a>
<a href="https://github.com/mrz1836/go-selfupdate/blob/master/LICENSE"><img src="https://img.shields.io/github/license/mrz1836/go-selfupdate?style=flat-square&color=blue" alt="License"></a>

<br/>

<table align="center" border="0">
  <tr>
    <td align="right">
       <code>CI / CD</code> &nbsp;&nbsp;
    </td>
    <td align="left">
       <a href="https://github.com/mrz1836/go-selfupdate/actions"><img src="https://img.shields.io/github/actions/workflow/status/mrz1836/go-selfupdate/fortress.yml?branch=master&label=build&logo=github&style=flat-square" alt="Build"></a>
       <a href="https://github.com/mrz1836/go-selfupdate/actions"><img src="https://img.shields.io/github/last-commit/mrz1836/go-selfupdate?style=flat-square&logo=git&logoColor=white&label=last%20update" alt="Last Commit"></a>
    </td>
    <td align="right">
       &nbsp;&nbsp;&nbsp;&nbsp; <code>Quality</code> &nbsp;&nbsp;
    </td>
    <td align="left">
       <a href="https://codecov.io/gh/mrz1836/go-selfupdate"><img src="https://codecov.io/gh/mrz1836/go-selfupdate/branch/master/graph/badge.svg?style=flat-square" alt="Coverage"></a>
    </td>
  </tr>

  <tr>
    <td align="right">
       <code>Security</code> &nbsp;&nbsp;
    </td>
    <td align="left">
       <a href="https://scorecard.dev/viewer/?uri=github.com/mrz1836/go-selfupdate"><img src="https://api.scorecard.dev/projects/github.com/mrz1836/go-selfupdate/badge?style=flat-square" alt="Scorecard"></a>
       <a href=".github/SECURITY.md"><img src="https://img.shields.io/badge/policy-active-success?style=flat-square&logo=security&logoColor=white" alt="Security"></a>
    </td>
    <td align="right">
       &nbsp;&nbsp;&nbsp;&nbsp; <code>Docs</code> &nbsp;&nbsp;
    </td>
    <td align="left">
       <a href="https://pkg.go.dev/github.com/mrz1836/go-selfupdate"><img src="https://img.shields.io/badge/godoc-reference-blue?style=flat-square&logo=go&logoColor=white" alt="Go Reference"></a>
       <a href="https://mrz1818.com/"><img src="https://img.shields.io/badge/donate-bitcoin-ff9900?style=flat-square&logo=bitcoin" alt="Bitcoin"></a>
    </td>
  </tr>
</table>

</div>

<br/>
<br/>

<div align="center">

### <code>Project Navigation</code>

</div>

<table align="center">
  <tr>
    <td align="center" width="33%">
       📦&nbsp;<a href="#-installation"><code>Installation</code></a>
    </td>
    <td align="center" width="33%">
       ⚡&nbsp;<a href="#-quick-start"><code>Quick&nbsp;Start</code></a>
    </td>
    <td align="center" width="33%">
       🧪&nbsp;<a href="#-examples--tests"><code>Examples&nbsp;&&nbsp;Tests</code></a>
    </td>
  </tr>
  <tr>
    <td align="center">
       📚&nbsp;<a href="#-documentation"><code>Documentation</code></a>
    </td>
    <td align="center">
      🛠️&nbsp;<a href="#-code-standards"><code>Code&nbsp;Standards</code></a>
    </td>
    <td align="center">
      🧭&nbsp;<a href="#-design-notes"><code>Design&nbsp;Notes</code></a>
    </td>
  </tr>
  <tr>
    <td align="center">
      🤖&nbsp;<a href="#-ai-usage--assistant-guidelines"><code>AI&nbsp;Usage</code></a>
    </td>
    <td align="center">
       ⚖️&nbsp;<a href="#-license"><code>License</code></a>
    </td>
    <td align="center">
       👥&nbsp;<a href="#-maintainers"><code>Maintainers</code></a>
    </td>
  </tr>
</table>
<br/>

## 🧩 About

Every CLI eventually grows a self-updater — and a lot of them grow it *badly*: no checksum,
a half-written binary after a flaky download, a `../../etc` surprise hiding in a tarball.
**go-selfupdate** is that feature written once, carefully, so you never have to write it
again: resolve the latest GitHub release, download the right asset, **verify its SHA-256**,
extract it safely, and atomically swap the running binary — plus the passive "a new version
is available" banner, and an optional supervised upgrade for tools that run as a service.

Adopting it is a diff that *deletes* code. A tool with its own updater carries two to five
hundred lines of release lookup, archive extraction, and binary replacement. The
replacement is one function call.

- **One-call adoption** — `cobracmd.Attach` registers `update` (and `upgrade`, its alias) with `--check`, `--force`, and `--verbose`, *and* wires the passive banner, all from a single config. Drop to `New` + `AttachBanner` when you want the command and the notice configured separately.
- **Checksum-verified, always** — every archive is hashed as it streams and matched against the release's `checksums.txt`. A mismatch aborts before a single byte reaches your binary; a release with **no** checksums file is refused outright, never installed unverified.
- **Refuses before it touches the network** — an unsupported platform, a binary another installer owns (Homebrew, `go install`), and a non-writable install directory are each caught *before* anything is downloaded, so the answer is instant and the message says what to do.
- **Guarded extraction** — path traversal (`../`) is rejected, exotic file modes are normalized away (no setuid surprises), and a size cap defuses a decompression bomb.
- **Atomic replacement** — stage a sibling `<target>.new`, `fsync` it, `chmod` it, then `rename` it over the target in the same directory. The rename is atomic and a running process keeps working: the binary on disk is always either the old one or the new one, never a half-written nothing.
- **Errors you can act on** — every failure maps to exactly one `errors.Is`-matchable sentinel, wrapped with the concrete path or asset, so the message alone tells a user their next move.
- **Passive, never pushy** — the "new version" notice is opt-out, TTL-cached, CI-silent, and swallows every error (including panics). An update check can never be the reason a command fails.
- **Supervised upgrades** — the `managed/` sub-package defers inside a drain window, runs a post-upgrade health check, and reports a rollback outcome when it fails — for tools that run as a long-lived service.
- **Almost no dependencies** — the core update path imports only the standard library; `cobra` enters solely through the optional `cobracmd/` sub-package. Pure Go, nothing exotic.

> **Why it matters:** an unverified self-updater is a remote-code-execution primitive wearing
> a convenience feature's clothes. go-selfupdate refuses to write anything it has not hashed
> against a published checksum, and refuses to guess when it can't — so the one code path that
> replaces your users' binary is the one path you never have to re-audit per project.

> **Platforms:** macOS and Linux get the full self-update today. On Windows, `Check`,
> `--check`, and the update banner already work; `Install` is **coming soon** — until then it
> returns a clear message pointing at the releases page instead of failing halfway.

<br/>

## 📦 Installation

**go-selfupdate** requires a [supported release of Go](https://golang.org/doc/devel/release.html#policy).
```shell script
go get -u github.com/mrz1836/go-selfupdate
```

Get the [MAGE-X](https://github.com/mrz1836/mage-x) build tool for development:
```shell script
go install github.com/mrz1836/mage-x/cmd/magex@latest
```

<br/>

## ⚡ Quick Start

### 1. Wire it into a cobra CLI

One call adds the whole feature — the active `update` command **and** the passive banner —
from a single config:

```go
import (
    selfupdate "github.com/mrz1836/go-selfupdate"
    "github.com/mrz1836/go-selfupdate/cobracmd"
)

// Registers `widget update` (and `widget upgrade`, its alias) with
// --check, --force, and --verbose, and wires the "new version available"
// banner. State the tool's identity once.
cobracmd.Attach(root, selfupdate.Config{
    Owner:          "acme",
    Repo:           "widget",
    BinaryName:     "widget",
    CurrentVersion: version, // usually stamped via -ldflags "-X main.version=..."
})
```

Want the command and the banner configured separately — a custom cache directory, a
different banner stream? Use the two pieces `Attach` is built from:

```go
root.AddCommand(cobracmd.New(selfupdate.Config{ /* … */ }))
cobracmd.AttachBanner(root, notify.Config{ /* … */ })
```

A complete, buildable program is in [`examples/minimal`](examples/minimal/main.go).

### 2. Or drive it programmatically

Prefer to own the control flow? The core API is two functions — `Check` never writes,
`Install` runs the full pipeline:

```go
info, err := selfupdate.Check(ctx, cfg)                       // never writes
result, err := selfupdate.Install(ctx, cfg, selfupdate.WithForce())
```

`Check` is safe to call from anywhere — a doctor command, a status line, a test. It is the
same call `--check` makes.

### 3. What happens during an update

The order matters, and it is part of the contract:

1. **Platform guard** — an unsupported `GOOS`/`GOARCH` is refused before an HTTP client is even constructed, so a user on a platform you do not publish for pays no network round-trip and gets a straight answer.
2. **Managed-install detection** — a binary another installer owns (a package manager's cellar, a toolchain `bin` directory) is refused with the command that *does* own it, rather than silently overwritten.
3. **Writable-directory probe** — `install dir not writable: <path>` arrives *before* the download, not after it.
4. **Release resolution** — the `gh` CLI first when it is present and authenticated, falling back to the GitHub REST API.
5. **Checksum-verified download** — the archive is hashed as it streams and compared against the release's `checksums.txt`. A mismatch aborts before anything is written to the install path. A release with no checksums file is refused outright.
6. **Guarded extraction** — path traversal (`../`) is rejected, file modes are normalized, and a size cap stops a decompression bomb.
7. **Atomic replace** — stage a sibling `<target>.new`, `fsync`, `chmod`, then `rename` it over the target. The rename is same-directory, so it is atomic and never leaves the command missing (and the running process keeps reading the old inode).

Every stage returns its own sentinel error (`ErrUnsupportedPlatform`, `ErrManagedInstall`,
`ErrInstallDirNotWritable`, `ErrAssetNotFound`, `ErrChecksumMismatch`, …) wrapped with the
concrete path or asset that failed, so `errors.Is` works and the message alone tells a user
what to do next.

> **Windows:** the write path (step 7) needs a rename-aside dance rather than the POSIX
> atomic rename, so `Install` is gated on Windows for now — it returns `ErrWindowsNotSupported`
> with a link to the releases page instead of failing halfway through. `Check`, `--check`, and
> the passive banner work on Windows today; full self-update is coming soon.

<br/>

<details>
<summary><strong><code>Check vs. Install</code></strong></summary>
<br/>

`Check` answers a question; `Install` acts on it. `Check` writes nothing, ever — it is the
same call the `--check` flag makes.

| | `Check` | `Install` |
|---|---|---|
| Network | release metadata only | metadata + archive + checksums |
| Writes | none, ever | the target binary, atomically |
| Backing flag | `--check` | the bare command |

An absent platform asset is reported as an error, but `Check` still returns a populated
`Info` alongside it — so `--check` output can show *which* version exists even when this
platform has nothing to download.

</details>

<details>
<summary><strong><code>Configuration reference</code></strong></summary>
<br/>

`selfupdate.Config` requires only `Owner`, `Repo`, and `BinaryName`; everything else has a
production default applied on normalization (your `Config` is never mutated):

| Field | Default | Notes |
|---|---|---|
| `CurrentVersion` | `dev` | A development build is never replaced without `--force`. |
| `TargetPath` | `os.Executable()` with symlinks resolved | Replaces the real file, not a link to it. |
| `Client` | 5-minute timeout | Any `*http.Client`. |
| `TokenEnvVar` | none | Consulted before `GITHUB_TOKEN` and `GH_TOKEN`. |
| `Source` | `gh` CLI, then REST | Any `ReleaseSource` implementation; tests inject a stub. |
| `Platforms` | linux/darwin/windows × amd64/arm64 | Narrow it when you publish fewer. |
| `Stdout` | `os.Stdout` | Progress and the version-transition line; the command wires this to cobra's stream. |
| `Logger` | `slog.Default()` | |

The passive banner's own stream, cache, and style live on `notify.Config`, not here — the
two packages keep their configuration separate.

Per-call switches are `Option` values: `WithForce()` installs even when not newer,
`WithVerbose()` narrates each step, and `WithCheckOnly()` reports what an install would do
without writing anything.

</details>

<details>
<summary><strong><code>Environment variables</code></strong></summary>
<br/>

Names are derived from the application, so two tools built on this library never fight over
one another's settings. `<APP>` is the uppercased application name.

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

</details>

<details>
<summary><strong><code>Sub-packages</code></strong></summary>
<br/>

**`notify/` — the passive notice.** A TTL-cached check plus the banner that reports it. It
is built to be ignorable: `StartBackgroundCheck` swallows every error, including panics, so
an update check can never be the reason a CLI fails. The cache is written atomically and
lives under `os.UserConfigDir()/<app>` by default — pass `CacheDir` to keep a location your
tool already uses.

```go
result := notify.Check(ctx, cfg)   // cached
notify.ShowBanner(cfg, result)     // silent unless an update exists
```

**`managed/` — supervised upgrades.** For a tool that runs as a long-lived service, where
"replace the binary now" is the wrong answer. `RunManaged` defers inside a caller-supplied
drain window, runs a post-upgrade health check, and reports a rollback outcome when that
check fails. A deferral is not an error: it returns `OutcomeDeferred` with a nil error when
the clock is inside the window, so a supervisor can treat it as success and retry later.

```go
window, err := managed.NewDrainWindow("22:00", "02:00") // wraps midnight

outcome, err := managed.RunManaged(ctx, managed.ManagedConfig{
    Window:      window,
    Upgrade:     func(context.Context) error { /* … */ },
    HealthCheck: func(context.Context) error { /* … */ },
    Rollback:    func(context.Context) error { /* … */ },
})
```

The core package does not import `managed/`; the dependency runs one way only.

**`cobracmd/` — the drop-in command.** `Attach` does both halves in one call; `New` builds
just the command and `AttachBanner` wires just the notice when you need them apart. All
three are shown in step 1 above.

</details>

<details>
<summary><strong><code>Release conventions</code></strong></summary>
<br/>

The library expects the layout [GoReleaser](https://github.com/goreleaser/goreleaser)
produces by default:

- Archives named `<project>_<version>_<os>_<arch>.tar.gz`, for **every** OS including Windows.
- A SHA-256 checksums asset, conventionally `<project>_<version>_checksums.txt`, in the standard `<hex>  <filename>` format.
- Plain `x.y.z` releases published as *latest*.

The checksum **filename** is matched leniently — any asset ending in `checksums.txt` will
do, since a project that leaves the checksum block at its default still produces one. The
checksum **value** is not lenient: missing, unparseable, or absent-for-this-asset is a hard
failure, never a warning.

</details>

<br/>

## 📚 Documentation

- **API Reference** – Dive into the godocs at [pkg.go.dev/github.com/mrz1836/go-selfupdate](https://pkg.go.dev/github.com/mrz1836/go-selfupdate)
- **Design Notes** – The choices worth knowing about are in the [Design Notes](#-design-notes) section
- **Test Suite** – Review the [unit tests](selfupdate_test.go) and [fuzz tests](fuzz_test.go), written against the standard library `testing` package
- **Examples** – Browse the runnable CLI in [`examples/minimal`](examples/minimal/main.go)

<br/>

<details>
<summary><strong><code>Repository Features</code></strong></summary>
<br/>

This repository includes 25+ built-in features covering CI/CD, security, code quality, developer experience, and community tooling.

**[View the full Repository Features list →](.github/docs/repository-features.md)**

</details>

<details>
<summary><strong><code>Library Deployment</code></strong></summary>
<br/>

This project uses [goreleaser](https://github.com/goreleaser/goreleaser) for streamlined binary and library deployment to GitHub. To get started, install it via:

```bash
brew install goreleaser
```

The release process is defined in the [.goreleaser.yml](.goreleaser.yml) configuration file.


Then create and push a new Git tag using:

```bash
magex version:bump push=true bump=patch branch=master
```

This process ensures consistent, repeatable releases with properly versioned artifacts and metadata.

</details>

<details>
<summary><strong><code>Pre-commit Hooks</code></strong></summary>
<br/>

Set up the Go-Pre-commit System to run the same formatting, linting, and tests defined in [AGENTS.md](.github/AGENTS.md) before every commit:

```bash
go install github.com/mrz1836/go-pre-commit/cmd/go-pre-commit@latest
go-pre-commit install
```

The system is configured via modular env files in [`.github/env/`](.github/env/README.md) and provides 17x faster execution than traditional Python-based pre-commit hooks. See the [complete documentation](http://github.com/mrz1836/go-pre-commit) for details.

</details>

<details>
<summary><strong><code>GitHub Workflows</code></strong></summary>
<br/>

All workflows are driven by modular configuration in [`.github/env/`](.github/env/README.md) — no YAML editing required.

**[View all workflows and the control center →](.github/docs/workflows.md)**

</details>

<details>
<summary><strong><code>Updating Dependencies</code></strong></summary>
<br/>

To update all dependencies (Go modules, linters, and related tools), run:

```bash
magex deps:update
```

This command ensures all dependencies are brought up to date in a single step, including Go modules and any tools managed by [MAGE-X](https://github.com/mrz1836/mage-x). It is the recommended way to keep your development environment and CI in sync with the latest versions.

</details>

<details>
<summary><strong><code>Build Commands</code></strong></summary>
<br/>

View all build commands

```bash script
magex help
```

</details>

<br/>

## 🧪 Examples & Tests

All unit tests run via [GitHub Actions](https://github.com/mrz1836/go-selfupdate/actions) and use [Go version 1.25.x](https://go.dev/doc/go1.25). View the [configuration file](.github/workflows/fortress.yml).

The [`examples/minimal`](examples/minimal/main.go) directory contains a runnable CLI — an
`update` command with the standard flags plus the passive banner — wired end to end:

```bash script
go build ./examples/minimal
./minimal version
./minimal update --check
```

The example points at a repository that does not exist, so `update` reports that it cannot
resolve a release. That is the intended outcome — the value is the wiring, not the download.
Point `Owner` and `Repo` at your own project and it works.

The suite is written against the standard library `testing` package — table-driven unit
tests plus [fuzz tests](fuzz_test.go) over the parsing and extraction paths, with a stub
`ReleaseSource` so no test touches the network.

Run all tests (fast):

```bash script
magex test
```

Run all tests with race detector (slower):
```bash script
magex test:race
```

<br/>

## 🧭 Design Notes

A few decisions shape how this library behaves. The ones worth knowing about:

**Release-binary install only — there is no toolchain fallback.** This is the one decision
that changes behavior for a tool adopting the library, so it is stated plainly: a `go
install …@vX` route is not offered, and no flag re-enables it. The reason is mechanical
rather than stylistic — a single `replace` directive in a consuming module makes a
versioned module query impossible, so for exactly the projects that would need a fallback,
the fallback cannot fire. It was dead code posing as a safety net. A guard test fails the
build if the route ever reappears.

The consequence is that the single remaining route has to be legible when it fails, which is
why the three pre-network gates above are mandatory and why every stage has its own error.
It also means **the first install is a release-asset download** — fetch the archive for your
platform from the releases page, verify it against `checksums.txt`, and put the binary on
your `PATH`. After that, the tool updates itself.

**Atomic `.new` + rename, not a `.backup` dance.** Some implementations move the current
binary aside and then write the new one, which leaves a window where the command does not
exist at all. Writing a sibling file and renaming it over the target closes that window: the
binary is either the old one or the new one, never missing.

**Conservative version comparison.** A version that does not parse is treated as *not
newer* — the failure mode of the alternative is a tool that reinstalls itself forever. A
purely numeric string (a build number, CalVer like `20240101`) is a version, not a commit
hash, so it is never mistaken for a development marker. And when two versions share a
numeric core, a prerelease sorts below its final release, so someone on `v1.2.0-rc2` is
correctly offered `v1.2.0`.

**A development build is never replaced without `--force`.** An unstamped build (`dev`, an
empty version, a bare commit hash) has no version to compare and is usually the machine of
the person writing the code. `update` reports the release that exists and stops; `--force`
installs it deliberately.

**Windows self-update is coming soon.** Replacing a running `.exe` on Windows needs a
rename-aside dance rather than the POSIX atomic rename, so `Install` is gated there for now.
`Check` and the passive banner already work on Windows, and gating the write path keeps a
Windows user from a half-finished download that fails at the last step — they get a clear
pointer to the releases page instead.

**Latest only — no release channels.** No `stable`/`beta`/`edge` selection, because carrying
channel machinery for projects that only ever publish `x.y.z` is complexity with no caller.
Adding it later is additive and non-breaking.

**`tar.gz` only.** No `.zip` handling, for the same reason: nothing in scope produces one.

**Every seam is a `Config` field, not a package-level variable.** A library is imported by
whoever wants it, so process-global mutable state would let one consumer's tests reach into
another's cache.

<br/>

## 🛠️ Code Standards
Read more about this Go project's [code standards](.github/CODE_STANDARDS.md).

<br/>

## 🤖 AI Usage & Assistant Guidelines
Read the [AI Usage & Assistant Guidelines](.github/tech-conventions/ai-compliance.md) for details on how AI is used in this project and how to interact with the AI assistants.

<br/>

## 👥 Maintainers
| [<img src="https://github.com/mrz1836.png" height="50" width="50" alt="MrZ" />](https://github.com/mrz1836) |
|:-----------------------------------------------------------------------------------------------------------:|
|                                      [MrZ](https://github.com/mrz1836)                                      |

<br/>

## 🤝 Contributing
View the [contributing guidelines](.github/CONTRIBUTING.md) and please follow the [code of conduct](.github/CODE_OF_CONDUCT.md).

### How can I help?
All kinds of contributions are welcome :raised_hands:!
The most basic way to show your support is to star :star2: the project, or to raise issues :speech_balloon:.
You can also support this project by [becoming a sponsor on GitHub](https://github.com/sponsors/mrz1836) :clap:
or by making a [**bitcoin donation**](https://mrz1818.com/?tab=tips&utm_source=github&utm_medium=sponsor-link&utm_campaign=go-selfupdate&utm_term=go-selfupdate&utm_content=go-selfupdate) to ensure this journey continues indefinitely! :rocket:

[![Stars](https://img.shields.io/github/stars/mrz1836/go-selfupdate?label=Please%20like%20us&style=social&v=1)](https://github.com/mrz1836/go-selfupdate/stargazers)

<br/>

## 📝 License

[![License](https://img.shields.io/github/license/mrz1836/go-selfupdate.svg?style=flat&v=1)](LICENSE)
</content>
</invoke>
