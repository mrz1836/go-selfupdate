package selfupdate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
)

// checksumFileMaxBytes caps the read of a checksums.txt body. A checksum
// file for a six-asset matrix is well under a kilobyte; a megabyte is a
// generous ceiling that still refuses an unbounded stream from a hostile
// mirror.
const checksumFileMaxBytes int64 = 1 << 20

// maxDownloadBytes caps the release archive download. It matches the
// per-file extraction ceiling: a body larger than this is not a release,
// it is an attack or a broken mirror.
const maxDownloadBytes int64 = 500 << 20

// sha256HexLen is the exact length of a hex-encoded SHA-256 digest.
// Enforcing it rejects a checksums file that lists MD5 (32) or SHA-1
// (40) digests instead, rather than silently comparing against a weaker
// hash.
const sha256HexLen = 64

// downloadFilePerm is the mode for the archive as it streams to disk.
// Owner-only: the archive lands in a private temp directory and is
// deleted after extraction.
const downloadFilePerm os.FileMode = 0o600

// fetchChecksum downloads a goreleaser checksums file and returns the
// hex digest recorded for assetName.
func fetchChecksum(ctx context.Context, client *http.Client, checksumURL, assetName string) (string, error) {
	resp, err := httpGetOK(ctx, client, checksumURL, ErrChecksumFetchFailed, nil)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()

	data, err := io.ReadAll(io.LimitReader(resp.Body, checksumFileMaxBytes))
	if err != nil {
		return "", fmt.Errorf("%w: read body: %w", ErrChecksumFetchFailed, err)
	}

	return parseChecksum(string(data), assetName)
}

// parseChecksum returns the SHA-256 digest recorded for assetName in a
// goreleaser checksums file, whose lines read "<sha256-hex>  <filename>".
//
// An entry that is absent, malformed, or not a 64-character hex digest
// yields [ErrChecksumNotFound]. There is no permissive branch: an
// unverifiable download must fail, because the alternative is installing
// a binary nobody vouched for.
func parseChecksum(data, assetName string) (string, error) {
	for line := range strings.SplitSeq(data, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 || fields[1] != assetName {
			continue
		}
		digest := strings.ToLower(fields[0])
		if len(digest) == sha256HexLen && isHexString(digest) {
			return digest, nil
		}
	}
	return "", fmt.Errorf("%w: %s", ErrChecksumNotFound, assetName)
}

// isHexString reports whether s consists solely of hexadecimal digits.
func isHexString(s string) bool {
	for _, c := range s {
		isHex := (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
		if !isHex {
			return false
		}
	}
	return true
}

// downloadAndVerify streams url into dst while hashing the bytes in
// flight, then compares the digest against expectedSHA256.
//
// Hashing through an [io.TeeReader] rather than re-reading the finished
// file keeps memory flat regardless of archive size and removes the
// window in which the file on disk could differ from the bytes that were
// hashed. On any mismatch the partial file is deleted and the error is
// returned before extraction is even attempted, so nothing unverified
// ever reaches the install path.
func downloadAndVerify(ctx context.Context, client *http.Client, url, expectedSHA256, dst string) error {
	return downloadAndVerifyWithCap(ctx, client, url, expectedSHA256, dst, maxDownloadBytes)
}

// downloadAndVerifyWithCap is downloadAndVerify with the size ceiling as
// a parameter, so the oversized-body guard can be exercised with a
// kilobyte fixture rather than a half-gigabyte one — the same reason the
// extraction cap is a parameter.
func downloadAndVerifyWithCap(ctx context.Context, client *http.Client, url, expectedSHA256, dst string, maxBytes int64) error {
	if expectedSHA256 == "" {
		return fmt.Errorf("%w: %s", ErrChecksumMissing, dst)
	}

	resp, err := httpGetOK(ctx, client, url, ErrDownloadFailed, nil)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	actual, written, err := streamToFile(resp.Body, dst, maxBytes)
	if err != nil {
		_ = os.Remove(dst)
		return err
	}
	if written > maxBytes {
		_ = os.Remove(dst)
		return fmt.Errorf("%w: download exceeds %d bytes", ErrFileTooLarge, maxBytes)
	}

	if !strings.EqualFold(actual, expectedSHA256) {
		_ = os.Remove(dst)
		return fmt.Errorf("%w: expected %s, got %s", ErrChecksumMismatch, expectedSHA256, actual)
	}
	return nil
}

// streamToFile copies body into dst, returning the hex SHA-256 of
// everything written and the byte count. The count is read back by the
// caller to detect a body that hit the cap.
func streamToFile(body io.Reader, dst string, maxBytes int64) (digest string, written int64, err error) {
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, downloadFilePerm) //nolint:gosec // dst is built inside a private temp dir owned by this package
	if err != nil {
		return "", 0, fmt.Errorf("%w: create download file: %w", ErrDownloadFailed, err)
	}

	hasher := sha256.New()
	// Read one byte past the cap so a body sitting exactly at the limit
	// is distinguishable from one that exceeded it.
	limited := io.LimitReader(body, maxBytes+1)
	written, copyErr := io.Copy(out, io.TeeReader(limited, hasher))

	closeErr := out.Close()
	if copyErr != nil {
		return "", written, fmt.Errorf("%w: %w", ErrDownloadFailed, copyErr)
	}
	if closeErr != nil {
		return "", written, fmt.Errorf("%w: close download file: %w", ErrDownloadFailed, closeErr)
	}

	return hex.EncodeToString(hasher.Sum(nil)), written, nil
}
