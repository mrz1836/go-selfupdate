package selfupdate

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"
)

// apiTimeout caps every release-metadata request. It is independent of
// the much larger download timeout that wraps tarball transfers.
const apiTimeout = 10 * time.Second

// apiErrorBodyLimit caps how much of a non-200 response body is drained
// before the connection is returned to the pool. GitHub error envelopes
// are a few hundred bytes.
const apiErrorBodyLimit = 4096

// apiBodyLimit caps the successful release-metadata response before it is
// decoded. A release with long notes and many assets is still well under
// a megabyte; 16 MiB is a generous ceiling that stops a hostile or
// misconfigured endpoint from OOMing the process with an unbounded 200
// body — the same fail-closed posture the archive and checksum reads take.
const apiBodyLimit int64 = 16 << 20

// defaultAPIBaseURL is the public GitHub REST endpoint.
const defaultAPIBaseURL = "https://api.github.com"

// ReleaseSource is the seam between the update driver and whatever
// actually produces release metadata. Two implementations ship here: one
// that shells out to the `gh` CLI and one that talks to the GitHub REST
// API. Tests inject a stub.
//
// There is exactly one method. Callers of this library publish plain
// x.y.z releases, so "latest" is the only question worth asking.
type ReleaseSource interface {
	// Latest returns the most recent published, non-draft,
	// non-prerelease release.
	Latest(ctx context.Context) (*Release, error)
}

// commandRunner abstracts exec.Command so the gh-CLI path is testable
// on a machine without the binary. Remove this seam and the fallback
// branch becomes untestable, which is the whole reason it exists.
type commandRunner func(ctx context.Context, name string, args ...string) ([]byte, error)

// defaultCommandRunner shells out to the real binary via os/exec.
func defaultCommandRunner(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).Output() //nolint:gosec // name is always "gh"; args are built from Config, never from remote input
}

// resolveToken returns the first non-empty GitHub token found in the
// caller's preferred variable, then GITHUB_TOKEN, then GH_TOKEN.
//
// The per-application variable comes first so a tool can scope its own
// credential (say FLYWHEEL_GITHUB_TOKEN) without colliding with whatever
// the surrounding shell already exports.
func resolveToken(getenv func(string) string, tokenEnvVar string) string {
	if getenv == nil {
		getenv = os.Getenv
	}
	names := []string{tokenEnvVar, "GITHUB_TOKEN", "GH_TOKEN"}
	for _, name := range names {
		if name == "" {
			continue
		}
		if v := strings.TrimSpace(getenv(name)); v != "" {
			return v
		}
	}
	return ""
}

// ghSource reads releases through the `gh` CLI, inheriting whatever
// credentials the user has already configured for it.
type ghSource struct {
	run   commandRunner
	owner string
	repo  string
}

// NewGHSource returns a [ReleaseSource] backed by the `gh` CLI. Pass a
// nil runner to get the real exec-based one; tests pass a stub.
func NewGHSource(owner, repo string, runner commandRunner) ReleaseSource {
	if runner == nil {
		runner = defaultCommandRunner
	}
	return &ghSource{run: runner, owner: owner, repo: repo}
}

// ghJSONFields is the field set requested from `gh release view`.
const ghJSONFields = "tagName,assets,body,isPrerelease,isDraft,publishedAt,url"

// Latest returns the repository's latest published release.
func (g *ghSource) Latest(ctx context.Context) (*Release, error) {
	out, err := g.run(
		ctx, "gh", "release", "view",
		"--repo", g.owner+"/"+g.repo,
		"--json", ghJSONFields,
	)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrGHCLIFailed, err)
	}

	var r ghRelease
	if jerr := json.Unmarshal(out, &r); jerr != nil {
		return nil, fmt.Errorf("%w: parse gh response: %w", ErrGHCLIFailed, jerr)
	}
	if r.IsDraft || r.IsPrerelease {
		return nil, fmt.Errorf("%w: latest release %q is a draft or prerelease", ErrNoReleasesFound, r.TagName)
	}
	return convertGHReleaseToRelease(&r)
}

// apiSource talks directly to the GitHub REST API. It is the fallback
// when the gh CLI is absent or fails, and the only source in an
// environment that has no CLI at all (CI containers, minimal images).
type apiSource struct {
	httpClient *http.Client
	baseURL    string
	owner      string
	repo       string
	token      string
}

// NewAPISource returns a [ReleaseSource] backed by the GitHub REST API.
// Pass an empty baseURL for the public endpoint (tests point it at a
// local server) and an empty token for unauthenticated access.
func NewAPISource(owner, repo string, httpClient *http.Client, baseURL, token string) ReleaseSource {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: apiTimeout}
	}
	if baseURL == "" {
		baseURL = defaultAPIBaseURL
	}
	return &apiSource{
		httpClient: httpClient,
		baseURL:    strings.TrimRight(baseURL, "/"),
		owner:      owner,
		repo:       repo,
		token:      token,
	}
}

// Latest resolves /repos/<owner>/<repo>/releases/latest, which GitHub
// already defines as the newest published, non-draft, non-prerelease
// release.
func (a *apiSource) Latest(ctx context.Context) (*Release, error) {
	url := fmt.Sprintf("%s/repos/%s/%s/releases/latest", a.baseURL, a.owner, a.repo)

	var r Release
	if err := a.getJSON(ctx, url, &r); err != nil {
		return nil, err
	}
	if r.TagName == "" {
		return nil, fmt.Errorf("%w: %s/%s", ErrNoReleasesFound, a.owner, a.repo)
	}
	if r.Draft || r.Prerelease {
		return nil, fmt.Errorf("%w: latest release %q is a draft or prerelease", ErrNoReleasesFound, r.TagName)
	}
	return &r, nil
}

// getJSON issues an authenticated GET and decodes the response into out.
func (a *apiSource) getJSON(ctx context.Context, url string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		return fmt.Errorf("%w: build request: %w", ErrGitHubAPIFailed, err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	if a.token != "" {
		req.Header.Set("Authorization", "Bearer "+a.token)
	}

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrGitHubAPIFailed, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		// Drain a bounded slice of the body so the connection stays
		// reusable without inviting an unbounded read.
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, apiErrorBodyLimit))
		return fmt.Errorf("%w: status %d", ErrGitHubAPIFailed, resp.StatusCode)
	}

	if derr := json.NewDecoder(io.LimitReader(resp.Body, apiBodyLimit)).Decode(out); derr != nil {
		return fmt.Errorf("%w: decode response: %w", ErrGitHubAPIFailed, derr)
	}
	return nil
}

// fallbackSource tries the gh CLI first and falls back to the REST API
// on any failure. Preferring the CLI means a developer who has already
// authenticated `gh` gets private-repo and higher-rate-limit access for
// free, with no token plumbing.
type fallbackSource struct {
	gh    ReleaseSource
	api   ReleaseSource
	hasGH func() bool
}

// DefaultReleaseSource returns a [ReleaseSource] that prefers the `gh`
// CLI and falls back to the REST API. The runner, client, baseURL, and
// token arguments exist so tests can drive both branches hermetically;
// production callers get these filled in by Config.normalize.
func DefaultReleaseSource(owner, repo string, runner commandRunner, httpClient *http.Client, baseURL, token string) ReleaseSource {
	return &fallbackSource{
		gh:  NewGHSource(owner, repo, runner),
		api: NewAPISource(owner, repo, httpClient, baseURL, token),
		// Evaluated lazily so a test with an empty PATH still exercises
		// the API branch.
		hasGH: func() bool {
			_, err := exec.LookPath("gh")
			return err == nil
		},
	}
}

// Latest tries the gh CLI, then the REST API. When both fail the API
// error is returned: it is the more actionable of the two, since a
// missing or unauthenticated CLI is an expected condition while an API
// failure names the actual network or permission problem.
func (f *fallbackSource) Latest(ctx context.Context) (*Release, error) {
	if f.hasGH != nil && f.hasGH() {
		if r, err := f.gh.Latest(ctx); err == nil {
			return r, nil
		}
	}
	return f.api.Latest(ctx)
}
