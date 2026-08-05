package selfupdate

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mrz1836/go-selfupdate/internal/testutil"
)

func TestVerifyParseChecksum(t *testing.T) {
	t.Parallel()

	const asset = "widget_1.0.0_linux_amd64.tar.gz"
	digest := strings.Repeat("ab", 32)

	t.Run("returns the digest for the named asset", func(t *testing.T) {
		body := fmt.Sprintf("%s  other.tar.gz\n%s  %s\n", strings.Repeat("cd", 32), digest, asset)

		got, err := parseChecksum(body, asset)
		if err != nil {
			t.Fatalf("parseChecksum() = %v, want nil", err)
		}
		if got != digest {
			t.Errorf("parseChecksum() = %q, want %q", got, digest)
		}
	})

	t.Run("lowercases an uppercase digest", func(t *testing.T) {
		got, err := parseChecksum(fmt.Sprintf("%s  %s\n", strings.ToUpper(digest), asset), asset)
		if err != nil {
			t.Fatalf("parseChecksum() = %v, want nil", err)
		}
		if got != digest {
			t.Errorf("parseChecksum() = %q, want the lowercased digest", got)
		}
	})

	t.Run("rejects a weaker hash of the wrong length", func(t *testing.T) {
		for _, weak := range []string{strings.Repeat("a", 32), strings.Repeat("a", 40)} {
			_, err := parseChecksum(fmt.Sprintf("%s  %s\n", weak, asset), asset)
			if !errors.Is(err, ErrChecksumNotFound) {
				t.Errorf("parseChecksum() with a %d-char digest = %v, want ErrChecksumNotFound", len(weak), err)
			}
		}
	})

	t.Run("rejects a non-hexadecimal digest of the right length", func(t *testing.T) {
		_, err := parseChecksum(fmt.Sprintf("%s  %s\n", strings.Repeat("z", 64), asset), asset)
		if !errors.Is(err, ErrChecksumNotFound) {
			t.Fatalf("parseChecksum() = %v, want ErrChecksumNotFound", err)
		}
	})

	t.Run("an absent or malformed entry is a hard failure", func(t *testing.T) {
		for name, body := range map[string]string{
			"empty file":     "",
			"other assets":   fmt.Sprintf("%s  something-else.tar.gz\n", digest),
			"no filename":    digest + "\n",
			"garbage":        "this is not a checksums file\n",
			"blank lines":    "\n\n\n",
			"missing digest": "  " + asset + "\n",
		} {
			t.Run(name, func(t *testing.T) {
				if _, err := parseChecksum(body, asset); !errors.Is(err, ErrChecksumNotFound) {
					t.Errorf("parseChecksum(%q) = %v, want ErrChecksumNotFound", name, err)
				}
			})
		}
	})
}

func TestVerifyFetchChecksum(t *testing.T) {
	t.Parallel()

	const asset = "widget_1.0.0_linux_amd64.tar.gz"
	digest := strings.Repeat("ef", 32)

	t.Run("fetches and parses", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = fmt.Fprintf(w, "%s  %s\n", digest, asset)
		}))
		defer srv.Close()

		got, err := fetchChecksum(t.Context(), srv.Client(), srv.URL, asset)
		if err != nil {
			t.Fatalf("fetchChecksum() = %v, want nil", err)
		}
		if got != digest {
			t.Errorf("fetchChecksum() = %q, want %q", got, digest)
		}
	})

	t.Run("a non-200 status surfaces ErrChecksumFetchFailed", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		}))
		defer srv.Close()

		if _, err := fetchChecksum(t.Context(), srv.Client(), srv.URL, asset); !errors.Is(err, ErrChecksumFetchFailed) {
			t.Fatalf("fetchChecksum() = %v, want ErrChecksumFetchFailed", err)
		}
	})

	t.Run("a transport failure surfaces ErrChecksumFetchFailed", func(t *testing.T) {
		client := &http.Client{Transport: &testutil.CountingTransport{}}

		if _, err := fetchChecksum(t.Context(), client, "https://example.invalid/sums", asset); !errors.Is(err, ErrChecksumFetchFailed) {
			t.Fatalf("fetchChecksum() = %v, want ErrChecksumFetchFailed", err)
		}
	})

	t.Run("a nil client still works", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = fmt.Fprintf(w, "%s  %s\n", digest, asset)
		}))
		defer srv.Close()

		if _, err := fetchChecksum(t.Context(), nil, srv.URL, asset); err != nil {
			t.Fatalf("fetchChecksum() with a nil client = %v, want nil", err)
		}
	})

	t.Run("an oversized body is truncated rather than read forever", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			// Far more than the 1 MiB ceiling, and the real entry sits
			// past it, so truncation must show up as a parse failure.
			_, _ = w.Write([]byte(strings.Repeat("x", int(checksumFileMaxBytes)+1024)))
			_, _ = fmt.Fprintf(w, "\n%s  %s\n", digest, asset)
		}))
		defer srv.Close()

		if _, err := fetchChecksum(t.Context(), srv.Client(), srv.URL, asset); !errors.Is(err, ErrChecksumNotFound) {
			t.Fatalf("fetchChecksum() = %v, want ErrChecksumNotFound after truncation", err)
		}
	})
}

func TestVerifyDownloadAndVerify(t *testing.T) {
	t.Parallel()

	payload := []byte("a plausible release archive")

	newServer := func(body []byte, status int) *httptest.Server {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			if status != http.StatusOK {
				w.WriteHeader(status)
				return
			}
			_, _ = w.Write(body)
		}))
		t.Cleanup(srv.Close)
		return srv
	}

	t.Run("a matching digest writes the file", func(t *testing.T) {
		srv := newServer(payload, http.StatusOK)
		dst := filepath.Join(t.TempDir(), "archive.tar.gz")

		if err := downloadAndVerify(t.Context(), srv.Client(), srv.URL, testutil.SHA256Hex(payload), dst); err != nil {
			t.Fatalf("downloadAndVerify() = %v, want nil", err)
		}

		got, err := os.ReadFile(dst) //nolint:gosec // test-controlled path
		if err != nil {
			t.Fatalf("read downloaded file: %v", err)
		}
		if string(got) != string(payload) {
			t.Errorf("downloaded %q, want %q", got, payload)
		}
	})

	t.Run("a tampered payload fails and leaves nothing on disk", func(t *testing.T) {
		srv := newServer([]byte("tampered contents"), http.StatusOK)
		dst := filepath.Join(t.TempDir(), "archive.tar.gz")

		err := downloadAndVerify(t.Context(), srv.Client(), srv.URL, testutil.SHA256Hex(payload), dst)
		if !errors.Is(err, ErrChecksumMismatch) {
			t.Fatalf("downloadAndVerify() = %v, want ErrChecksumMismatch", err)
		}
		if _, statErr := os.Stat(dst); !errors.Is(statErr, os.ErrNotExist) {
			t.Error("a failed verification left the unverified download on disk")
		}
	})

	t.Run("an uppercase expected digest still matches", func(t *testing.T) {
		srv := newServer(payload, http.StatusOK)
		dst := filepath.Join(t.TempDir(), "archive.tar.gz")

		if err := downloadAndVerify(t.Context(), srv.Client(), srv.URL, strings.ToUpper(testutil.SHA256Hex(payload)), dst); err != nil {
			t.Fatalf("downloadAndVerify() = %v, want nil", err)
		}
	})

	t.Run("an empty expected digest refuses to download", func(t *testing.T) {
		srv := newServer(payload, http.StatusOK)
		dst := filepath.Join(t.TempDir(), "archive.tar.gz")

		if err := downloadAndVerify(t.Context(), srv.Client(), srv.URL, "", dst); !errors.Is(err, ErrChecksumMissing) {
			t.Fatalf("downloadAndVerify() = %v, want ErrChecksumMissing", err)
		}
	})

	t.Run("a non-200 status surfaces ErrDownloadFailed", func(t *testing.T) {
		srv := newServer(nil, http.StatusInternalServerError)
		dst := filepath.Join(t.TempDir(), "archive.tar.gz")

		if err := downloadAndVerify(t.Context(), srv.Client(), srv.URL, testutil.SHA256Hex(payload), dst); !errors.Is(err, ErrDownloadFailed) {
			t.Fatalf("downloadAndVerify() = %v, want ErrDownloadFailed", err)
		}
	})

	t.Run("an unreachable host surfaces ErrDownloadFailed", func(t *testing.T) {
		dst := filepath.Join(t.TempDir(), "archive.tar.gz")

		err := downloadAndVerify(t.Context(), &http.Client{Transport: &testutil.CountingTransport{}}, "https://example.invalid/a", testutil.SHA256Hex(payload), dst)
		if !errors.Is(err, ErrDownloadFailed) {
			t.Fatalf("downloadAndVerify() = %v, want ErrDownloadFailed", err)
		}
	})

	t.Run("a body past the cap is rejected and removed", func(t *testing.T) {
		big := []byte(strings.Repeat("x", 4096))
		srv := newServer(big, http.StatusOK)
		dst := filepath.Join(t.TempDir(), "archive.tar.gz")

		err := downloadAndVerifyWithCap(t.Context(), srv.Client(), srv.URL, testutil.SHA256Hex(big), dst, 1024)
		if !errors.Is(err, ErrFileTooLarge) {
			t.Fatalf("downloadAndVerifyWithCap() = %v, want ErrFileTooLarge", err)
		}
		if _, statErr := os.Stat(dst); !errors.Is(statErr, os.ErrNotExist) {
			t.Error("an oversized download was left on disk")
		}
	})

	t.Run("a body sitting exactly at the cap is accepted", func(t *testing.T) {
		exact := []byte(strings.Repeat("x", 1024))
		srv := newServer(exact, http.StatusOK)
		dst := filepath.Join(t.TempDir(), "archive.tar.gz")

		if err := downloadAndVerifyWithCap(t.Context(), srv.Client(), srv.URL, testutil.SHA256Hex(exact), dst, 1024); err != nil {
			t.Errorf("downloadAndVerifyWithCap() at exactly the cap = %v, want nil", err)
		}
	})

	t.Run("an unwritable destination surfaces ErrDownloadFailed", func(t *testing.T) {
		srv := newServer(payload, http.StatusOK)
		dst := filepath.Join(t.TempDir(), "no-such-dir", "archive.tar.gz")

		if err := downloadAndVerify(t.Context(), srv.Client(), srv.URL, testutil.SHA256Hex(payload), dst); !errors.Is(err, ErrDownloadFailed) {
			t.Fatalf("downloadAndVerify() = %v, want ErrDownloadFailed", err)
		}
	})
}
