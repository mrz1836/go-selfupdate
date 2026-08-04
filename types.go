package selfupdate

import "time"

// Release is the source-agnostic representation of a GitHub release.
// Both the gh CLI source and the direct REST API source converge on this
// shape; downstream code never sees a wire-format struct.
//
// There is deliberately no release-channel concept: the library resolves
// /releases/latest and nothing else. Every tool that consumes this
// package ships plain x.y.z releases, so beta/edge selection would be
// machinery with no caller.
type Release struct {
	TagName     string         `json:"tag_name"`
	Name        string         `json:"name"`
	Prerelease  bool           `json:"prerelease"`
	Draft       bool           `json:"draft"`
	PublishedAt time.Time      `json:"published_at"`
	Body        string         `json:"body"`
	HTMLURL     string         `json:"html_url"`
	Assets      []ReleaseAsset `json:"assets"`
}

// ReleaseAsset is one downloadable file attached to a [Release].
type ReleaseAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
	Size               int64  `json:"size"`
}

// ghRelease mirrors the JSON shape emitted by `gh release view --json …`.
// It is converted to [Release] by convertGHReleaseToRelease before it
// leaves the source layer.
type ghRelease struct {
	TagName      string          `json:"tagName"`
	Body         string          `json:"body"`
	IsPrerelease bool            `json:"isPrerelease"`
	IsDraft      bool            `json:"isDraft"`
	PublishedAt  string          `json:"publishedAt"`
	URL          string          `json:"url"`
	Assets       []ghReleaseFile `json:"assets"`
}

// ghReleaseFile mirrors one asset entry inside a `gh release view` JSON
// document.
type ghReleaseFile struct {
	Name string `json:"name"`
	URL  string `json:"url"`
	Size int64  `json:"size"`
}
