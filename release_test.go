package selfupdate

import (
	"errors"
	"testing"

	"github.com/mrz1836/go-selfupdate/internal/testutil"
)

func TestReleaseConvert(t *testing.T) {
	t.Parallel()

	t.Run("maps every field and reuses the tag as the name", func(t *testing.T) {
		gh := &ghRelease{
			TagName:     "v1.2.3",
			Body:        "release notes",
			PublishedAt: "2026-03-04T05:06:07Z",
			URL:         "https://github.com/acme/widget/releases/tag/v1.2.3",
			Assets: []ghReleaseFile{
				{Name: "widget_1.2.3_linux_amd64.tar.gz", URL: "https://example.test/a", Size: 1024},
			},
		}

		rel, err := convertGHReleaseToRelease(gh)
		if err != nil {
			t.Fatalf("convertGHReleaseToRelease() = %v, want nil", err)
		}
		if rel.TagName != "v1.2.3" || rel.Name != "v1.2.3" {
			t.Errorf("TagName/Name = %q/%q, want v1.2.3 for both", rel.TagName, rel.Name)
		}
		if rel.HTMLURL != gh.URL || rel.Body != gh.Body {
			t.Errorf("URL/body not carried across: %q / %q", rel.HTMLURL, rel.Body)
		}
		if len(rel.Assets) != 1 {
			t.Fatalf("got %d assets, want 1", len(rel.Assets))
		}
		if rel.Assets[0].BrowserDownloadURL != "https://example.test/a" || rel.Assets[0].Size != 1024 {
			t.Errorf("asset not converted: %+v", rel.Assets[0])
		}
		if rel.PublishedAt.Year() != 2026 {
			t.Errorf("PublishedAt = %v, want a 2026 timestamp", rel.PublishedAt)
		}
	})

	t.Run("an unparseable timestamp is an error, not a zero time", func(t *testing.T) {
		_, err := convertGHReleaseToRelease(&ghRelease{TagName: "v1.0.0", PublishedAt: "yesterday"})
		if !errors.Is(err, ErrGHCLIFailed) {
			t.Fatalf("convertGHReleaseToRelease() = %v, want ErrGHCLIFailed", err)
		}
	})
}

func TestReleaseSelectAsset(t *testing.T) {
	t.Parallel()

	platformAsset := ReleaseAsset{Name: testutil.CurrentAssetName("widget", "1.2.3"), BrowserDownloadURL: "https://example.test/platform"}
	otherAsset := ReleaseAsset{Name: "widget_1.2.3_plan9_mips.tar.gz", BrowserDownloadURL: "https://example.test/other"}

	t.Run("picks this platform's archive and the exact checksums file", func(t *testing.T) {
		rel := &Release{
			TagName: "v1.2.3",
			Assets: []ReleaseAsset{
				otherAsset,
				platformAsset,
				{Name: "widget_1.2.3_checksums.txt", BrowserDownloadURL: "https://example.test/sums"},
			},
		}

		asset, checksumURL, err := selectAsset(rel, "widget", "v1.2.3")
		if err != nil {
			t.Fatalf("selectAsset() = %v, want nil", err)
		}
		if asset.Name != platformAsset.Name {
			t.Errorf("asset = %q, want %q", asset.Name, platformAsset.Name)
		}
		if checksumURL != "https://example.test/sums" {
			t.Errorf("checksumURL = %q, want the exact-name asset", checksumURL)
		}
	})

	t.Run("falls back to any checksums.txt when the name does not match the template", func(t *testing.T) {
		rel := &Release{
			TagName: "v1.2.3",
			Assets: []ReleaseAsset{
				platformAsset,
				{Name: "checksums.txt", BrowserDownloadURL: "https://example.test/bare"},
			},
		}

		_, checksumURL, err := selectAsset(rel, "widget", "v1.2.3")
		if err != nil {
			t.Fatalf("selectAsset() = %v, want nil", err)
		}
		if checksumURL != "https://example.test/bare" {
			t.Errorf("checksumURL = %q, want the fallback asset", checksumURL)
		}
	})

	t.Run("prefers the exact template name over a fallback", func(t *testing.T) {
		rel := &Release{
			TagName: "v1.2.3",
			Assets: []ReleaseAsset{
				platformAsset,
				{Name: "other_checksums.txt", BrowserDownloadURL: "https://example.test/fallback"},
				{Name: "widget_1.2.3_checksums.txt", BrowserDownloadURL: "https://example.test/exact"},
			},
		}

		_, checksumURL, err := selectAsset(rel, "widget", "v1.2.3")
		if err != nil {
			t.Fatalf("selectAsset() = %v, want nil", err)
		}
		if checksumURL != "https://example.test/exact" {
			t.Errorf("checksumURL = %q, want the exact-name asset", checksumURL)
		}
	})

	t.Run("no archive for this platform reports ErrAssetNotFound", func(t *testing.T) {
		rel := &Release{
			TagName: "v1.2.3",
			Assets:  []ReleaseAsset{otherAsset, {Name: "widget_1.2.3_checksums.txt", BrowserDownloadURL: "https://example.test/sums"}},
		}

		_, checksumURL, err := selectAsset(rel, "widget", "v1.2.3")
		if !errors.Is(err, ErrAssetNotFound) {
			t.Fatalf("selectAsset() = %v, want ErrAssetNotFound", err)
		}
		if checksumURL == "" {
			t.Error("the checksum URL should still be reported so callers can render a useful message")
		}
	})

	t.Run("no assets at all reports ErrAssetNotFound", func(t *testing.T) {
		if _, _, err := selectAsset(&Release{TagName: "v1.2.3"}, "widget", "v1.2.3"); !errors.Is(err, ErrAssetNotFound) {
			t.Fatalf("selectAsset() = %v, want ErrAssetNotFound", err)
		}
	})
}
