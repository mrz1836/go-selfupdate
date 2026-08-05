package selfupdate

import (
	"fmt"
	"runtime"
	"strings"
	"time"
)

// checksumSuffix is the tail every goreleaser checksum asset shares,
// whatever the configured name template.
const checksumSuffix = "checksums.txt"

// convertGHReleaseToRelease lifts a gh-CLI shaped release into the
// canonical [Release]. The CLI emits no separate name field, so the tag
// stands in for both. PublishedAt is RFC 3339 — the only format gh emits.
func convertGHReleaseToRelease(gh *ghRelease) (*Release, error) {
	publishedAt, err := time.Parse(time.RFC3339, gh.PublishedAt)
	if err != nil {
		return nil, fmt.Errorf("%w: parse publishedAt: %w", ErrGHCLIFailed, err)
	}

	assets := make([]ReleaseAsset, len(gh.Assets))
	for i, a := range gh.Assets {
		assets[i] = ReleaseAsset{
			Name:               a.Name,
			BrowserDownloadURL: a.URL,
			Size:               a.Size,
		}
	}

	return &Release{
		TagName:     gh.TagName,
		Name:        gh.TagName,
		Prerelease:  gh.IsPrerelease,
		Draft:       gh.IsDraft,
		PublishedAt: publishedAt,
		Body:        gh.Body,
		HTMLURL:     gh.URL,
		Assets:      assets,
	}, nil
}

// assetSuffix returns the archive suffix goreleaser appends for the
// running platform, e.g. "_darwin_arm64.tar.gz".
//
// Only tar.gz is recognized: every goreleaser configuration in the tools
// this library serves emits tar.gz for all operating systems, including
// Windows, so zip handling would be untested code guarding a case that
// does not occur.
func assetSuffix() string {
	return fmt.Sprintf("_%s_%s.tar.gz", runtime.GOOS, runtime.GOARCH)
}

// selectAsset walks a release's assets and returns the archive matching
// the running platform along with the URL of the checksums file.
//
// Checksum-asset naming is matched leniently and the digest strictly.
// Most projects pin the goreleaser template
// "<project>_<version>_checksums.txt", but a project that omits the
// checksum block entirely still gets goreleaser's default name — so the
// exact name is preferred and any "*checksums.txt" asset is accepted as
// a fallback. What is never lenient is the verification itself: a
// missing or unmatched digest is a hard failure downstream, never a
// warning.
func selectAsset(release *Release, repo, version string) (asset ReleaseAsset, checksumURL string, err error) {
	suffix := assetSuffix()
	preferredChecksum := fmt.Sprintf("%s_%s_%s", repo, strings.TrimPrefix(version, "v"), checksumSuffix)

	var fallbackChecksumURL string
	for _, a := range release.Assets {
		switch {
		case strings.HasSuffix(a.Name, suffix):
			asset = a
		case a.Name == preferredChecksum:
			checksumURL = a.BrowserDownloadURL
		case strings.HasSuffix(a.Name, checksumSuffix):
			fallbackChecksumURL = a.BrowserDownloadURL
		}
	}

	if checksumURL == "" {
		checksumURL = fallbackChecksumURL
	}
	if asset.BrowserDownloadURL == "" {
		return ReleaseAsset{}, checksumURL, fmt.Errorf("%w: %s/%s in %s",
			ErrAssetNotFound, runtime.GOOS, runtime.GOARCH, release.TagName)
	}
	return asset, checksumURL, nil
}
