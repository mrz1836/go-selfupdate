package selfupdate

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"runtime"
	"strings"
	"testing"
)

// errStubRunner is returned by a command runner stubbed to fail.
var errStubRunner = errors.New("gh is not installed")

// stubRunner returns a commandRunner that yields out and err, recording
// the arguments it was called with.
func stubRunner(out string, err error, captured *[]string) commandRunner {
	return func(_ context.Context, name string, args ...string) ([]byte, error) {
		if captured != nil {
			*captured = append([]string{name}, args...)
		}
		return []byte(out), err
	}
}

const ghReleaseJSON = `{
  "tagName": "v1.4.0",
  "body": "notes",
  "isPrerelease": false,
  "isDraft": false,
  "publishedAt": "2026-01-02T03:04:05Z",
  "url": "https://github.com/acme/widget/releases/tag/v1.4.0",
  "assets": [
    {"name": "widget_1.4.0_linux_amd64.tar.gz", "url": "https://example.test/a.tar.gz", "size": 42}
  ]
}`

func TestSourceGHLatest(t *testing.T) {
	t.Run("parses a gh release and targets the right repository", func(t *testing.T) {
		var captured []string
		src := NewGHSource("acme", "widget", stubRunner(ghReleaseJSON, nil, &captured))

		rel, err := src.Latest(t.Context())
		if err != nil {
			t.Fatalf("Latest() = %v, want nil", err)
		}
		if rel.TagName != "v1.4.0" {
			t.Errorf("TagName = %q, want %q", rel.TagName, "v1.4.0")
		}
		if len(rel.Assets) != 1 || rel.Assets[0].BrowserDownloadURL != "https://example.test/a.tar.gz" {
			t.Errorf("assets not converted: %+v", rel.Assets)
		}
		if rel.PublishedAt.IsZero() {
			t.Error("PublishedAt was not parsed")
		}
		if !strings.Contains(strings.Join(captured, " "), "acme/widget") {
			t.Errorf("gh invocation %v does not target acme/widget", captured)
		}
	})

	t.Run("a failing CLI surfaces ErrGHCLIFailed", func(t *testing.T) {
		src := NewGHSource("acme", "widget", stubRunner("", errStubRunner, nil))

		if _, err := src.Latest(t.Context()); !errors.Is(err, ErrGHCLIFailed) {
			t.Fatalf("Latest() = %v, want ErrGHCLIFailed", err)
		}
	})

	t.Run("unparseable output surfaces ErrGHCLIFailed", func(t *testing.T) {
		src := NewGHSource("acme", "widget", stubRunner("{not json", nil, nil))

		if _, err := src.Latest(t.Context()); !errors.Is(err, ErrGHCLIFailed) {
			t.Fatalf("Latest() = %v, want ErrGHCLIFailed", err)
		}
	})

	t.Run("a draft or prerelease is refused", func(t *testing.T) {
		for _, field := range []string{`"isDraft": true`, `"isPrerelease": true`} {
			body := strings.Replace(ghReleaseJSON, `"isDraft": false`, field, 1)
			body = strings.Replace(body, `"isPrerelease": false`, field, 1)

			src := NewGHSource("acme", "widget", stubRunner(body, nil, nil))
			if _, err := src.Latest(t.Context()); !errors.Is(err, ErrNoReleasesFound) {
				t.Errorf("Latest() with %s = %v, want ErrNoReleasesFound", field, err)
			}
		}
	})
}

func TestSourceAPILatest(t *testing.T) {
	t.Run("resolves the latest release", func(t *testing.T) {
		var gotPath, gotAuth string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotPath = r.URL.Path
			gotAuth = r.Header.Get("Authorization")
			_, _ = fmt.Fprint(w, `{"tag_name":"v2.0.0","assets":[{"name":"a.tar.gz","browser_download_url":"https://example.test/a"}]}`)
		}))
		defer srv.Close()

		src := NewAPISource("acme", "widget", srv.Client(), srv.URL, "s3cret")
		rel, err := src.Latest(t.Context())
		if err != nil {
			t.Fatalf("Latest() = %v, want nil", err)
		}
		if rel.TagName != "v2.0.0" {
			t.Errorf("TagName = %q, want v2.0.0", rel.TagName)
		}
		if gotPath != "/repos/acme/widget/releases/latest" {
			t.Errorf("requested %q, want the latest-release endpoint for acme/widget", gotPath)
		}
		if gotAuth != "Bearer s3cret" {
			t.Errorf("Authorization = %q, want the bearer token", gotAuth)
		}
	})

	t.Run("no token means no Authorization header", func(t *testing.T) {
		var hadAuth bool
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, hadAuth = r.Header["Authorization"]
			_, _ = fmt.Fprint(w, `{"tag_name":"v1.0.0"}`)
		}))
		defer srv.Close()

		if _, err := NewAPISource("acme", "widget", srv.Client(), srv.URL, "").Latest(t.Context()); err != nil {
			t.Fatalf("Latest() = %v, want nil", err)
		}
		if hadAuth {
			t.Error("an unauthenticated source sent an Authorization header")
		}
	})

	t.Run("a non-200 status surfaces ErrGitHubAPIFailed", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusForbidden)
			_, _ = fmt.Fprint(w, `{"message":"rate limited"}`)
		}))
		defer srv.Close()

		_, err := NewAPISource("acme", "widget", srv.Client(), srv.URL, "").Latest(t.Context())
		if !errors.Is(err, ErrGitHubAPIFailed) {
			t.Fatalf("Latest() = %v, want ErrGitHubAPIFailed", err)
		}
		if !strings.Contains(err.Error(), "403") {
			t.Errorf("error %q does not report the status code", err)
		}
	})

	t.Run("an unparseable body surfaces ErrGitHubAPIFailed", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = fmt.Fprint(w, `{"tag_name": `)
		}))
		defer srv.Close()

		if _, err := NewAPISource("acme", "widget", srv.Client(), srv.URL, "").Latest(t.Context()); !errors.Is(err, ErrGitHubAPIFailed) {
			t.Fatalf("Latest() = %v, want ErrGitHubAPIFailed", err)
		}
	})

	t.Run("a nil client and empty base URL fall back to production defaults", func(t *testing.T) {
		src, ok := NewAPISource("acme", "widget", nil, "", "").(*apiSource)
		if !ok {
			t.Fatal("NewAPISource did not return an *apiSource")
		}
		if src.httpClient == nil {
			t.Error("NewAPISource left the client nil")
		}
		if src.baseURL != defaultAPIBaseURL {
			t.Errorf("baseURL = %q, want %q", src.baseURL, defaultAPIBaseURL)
		}
	})

	t.Run("an empty document reports no releases", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = fmt.Fprint(w, `{}`)
		}))
		defer srv.Close()

		if _, err := NewAPISource("acme", "widget", srv.Client(), srv.URL, "").Latest(t.Context()); !errors.Is(err, ErrNoReleasesFound) {
			t.Fatalf("Latest() = %v, want ErrNoReleasesFound", err)
		}
	})

	t.Run("a draft or prerelease is refused", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = fmt.Fprint(w, `{"tag_name":"v3.0.0-rc1","prerelease":true}`)
		}))
		defer srv.Close()

		if _, err := NewAPISource("acme", "widget", srv.Client(), srv.URL, "").Latest(t.Context()); !errors.Is(err, ErrNoReleasesFound) {
			t.Fatalf("Latest() = %v, want ErrNoReleasesFound", err)
		}
	})
}

func TestSourceFallback(t *testing.T) {
	apiRelease := &Release{TagName: "v9.9.9"}

	t.Run("the CLI wins when it is present and succeeds", func(t *testing.T) {
		src := &fallbackSource{
			gh:    &stubSource{release: &Release{TagName: "v1.0.0"}},
			api:   &stubSource{release: apiRelease},
			hasGH: func() bool { return true },
		}

		rel, err := src.Latest(t.Context())
		if err != nil {
			t.Fatalf("Latest() = %v, want nil", err)
		}
		if rel.TagName != "v1.0.0" {
			t.Errorf("TagName = %q, want the gh result v1.0.0", rel.TagName)
		}
	})

	t.Run("a missing CLI falls through to the API without invoking it", func(t *testing.T) {
		gh := &stubSource{release: &Release{TagName: "v1.0.0"}}
		src := &fallbackSource{gh: gh, api: &stubSource{release: apiRelease}, hasGH: func() bool { return false }}

		rel, err := src.Latest(t.Context())
		if err != nil {
			t.Fatalf("Latest() = %v, want nil", err)
		}
		if rel.TagName != apiRelease.TagName {
			t.Errorf("TagName = %q, want the API result %q", rel.TagName, apiRelease.TagName)
		}
		if gh.calls != 0 {
			t.Errorf("gh source was invoked %d time(s) despite being absent", gh.calls)
		}
	})

	t.Run("a failing CLI falls back to the API", func(t *testing.T) {
		gh := &stubSource{err: ErrGHCLIFailed}
		src := &fallbackSource{gh: gh, api: &stubSource{release: apiRelease}, hasGH: func() bool { return true }}

		rel, err := src.Latest(t.Context())
		if err != nil {
			t.Fatalf("Latest() = %v, want nil", err)
		}
		if rel.TagName != apiRelease.TagName {
			t.Errorf("TagName = %q, want the API result", rel.TagName)
		}
		if gh.calls != 1 {
			t.Errorf("gh source was invoked %d time(s), want 1", gh.calls)
		}
	})

	t.Run("both failing surfaces the API error, which is the actionable one", func(t *testing.T) {
		src := &fallbackSource{
			gh:    &stubSource{err: ErrGHCLIFailed},
			api:   &stubSource{err: ErrGitHubAPIFailed},
			hasGH: func() bool { return true },
		}

		if _, err := src.Latest(t.Context()); !errors.Is(err, ErrGitHubAPIFailed) {
			t.Fatalf("Latest() = %v, want ErrGitHubAPIFailed", err)
		}
	})

	t.Run("the constructed default wires both sources", func(t *testing.T) {
		src := DefaultReleaseSource("acme", "widget", nil, nil, "", "")
		fb, ok := src.(*fallbackSource)
		if !ok {
			t.Fatalf("DefaultReleaseSource returned %T, want *fallbackSource", src)
		}
		if fb.gh == nil || fb.api == nil || fb.hasGH == nil {
			t.Fatal("DefaultReleaseSource left a seam unwired")
		}

		// The probe must be callable and must agree with the real PATH,
		// whichever way this machine is set up.
		_, lookErr := exec.LookPath("gh")
		if got := fb.hasGH(); got != (lookErr == nil) {
			t.Errorf("hasGH() = %v, but exec.LookPath(\"gh\") error = %v", got, lookErr)
		}
	})
}

func TestSourceDefaultCommandRunner(t *testing.T) {
	t.Run("a missing binary returns an error rather than hanging", func(t *testing.T) {
		_, err := defaultCommandRunner(t.Context(), "go-selfupdate-no-such-binary-9f3a")
		if err == nil {
			t.Fatal("defaultCommandRunner() on a missing binary = nil, want an error")
		}
	})

	t.Run("a real binary's stdout is returned", func(t *testing.T) {
		// The Go toolchain is present by definition inside `go test`,
		// which makes it the one binary safe to shell out to here.
		out, err := defaultCommandRunner(t.Context(), "go", "env", "GOOS")
		if err != nil {
			t.Fatalf("defaultCommandRunner() = %v, want nil", err)
		}
		if strings.TrimSpace(string(out)) != runtime.GOOS {
			t.Errorf("defaultCommandRunner() = %q, want %q", strings.TrimSpace(string(out)), runtime.GOOS)
		}
	})
}

func TestSourceResolveToken(t *testing.T) {
	env := func(pairs map[string]string) func(string) string {
		return func(k string) string { return pairs[k] }
	}

	tests := []struct {
		name   string
		envVar string
		values map[string]string
		want   string
	}{
		{
			name:   "the application variable wins",
			envVar: "WIDGET_GITHUB_TOKEN",
			values: map[string]string{"WIDGET_GITHUB_TOKEN": "app", "GITHUB_TOKEN": "generic", "GH_TOKEN": "cli"},
			want:   "app",
		},
		{
			name:   "GITHUB_TOKEN is next",
			envVar: "WIDGET_GITHUB_TOKEN",
			values: map[string]string{"GITHUB_TOKEN": "generic", "GH_TOKEN": "cli"},
			want:   "generic",
		},
		{
			name:   "GH_TOKEN is last",
			envVar: "WIDGET_GITHUB_TOKEN",
			values: map[string]string{"GH_TOKEN": "cli"},
			want:   "cli",
		},
		{
			name:   "an unset application variable is skipped",
			envVar: "",
			values: map[string]string{"GITHUB_TOKEN": "generic"},
			want:   "generic",
		},
		{
			name:   "whitespace-only values are ignored",
			envVar: "WIDGET_GITHUB_TOKEN",
			values: map[string]string{"WIDGET_GITHUB_TOKEN": "   ", "GITHUB_TOKEN": "generic"},
			want:   "generic",
		},
		{
			name:   "nothing set yields an empty token",
			envVar: "WIDGET_GITHUB_TOKEN",
			values: map[string]string{},
			want:   "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := resolveToken(env(tc.values), tc.envVar); got != tc.want {
				t.Errorf("resolveToken() = %q, want %q", got, tc.want)
			}
		})
	}

	t.Run("a nil getenv falls back to the process environment", func(t *testing.T) {
		t.Setenv("GITHUB_TOKEN", "from-process")
		if got := resolveToken(nil, ""); got != "from-process" {
			t.Errorf("resolveToken(nil, \"\") = %q, want %q", got, "from-process")
		}
	})
}
