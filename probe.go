package selfupdate

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// probeFilePerm is the mode used for the throwaway probe file. Owner
// read/write only — the file exists for microseconds and never holds
// content.
const probeFilePerm os.FileMode = 0o600

// probeFileName is the basename of the throwaway probe file created
// inside the install directory. The leading dot keeps it out of casual
// directory listings should a crash ever leave one behind.
const probeFileName = ".go-selfupdate-probe"

// notWritableGuidance is the actionable half of a permission-denied
// [ErrInstallDirNotWritable] message. The seamless fix is to own the
// install directory, not to elevate: running the updater under sudo just
// re-creates the binary root-owned and pushes the same wall to the next
// release. sudo is therefore deliberately absent here — the guidance
// points only at a directory the user already writes.
const notWritableGuidance = "move the binary to a user-writable directory on your PATH " +
	"(for example ~/.local/bin) to enable self-update"

// probeInstallDirWritable proves that the directory holding the install
// target can actually be written to, before a single byte is downloaded.
// Failures surface as [ErrInstallDirNotWritable], whose message reads
// "install dir not writable: <path>" with the resolved directory
// appended, so the operator can act on it directly.
//
// The check creates and removes a real file rather than inspecting mode
// bits: permission bits lie on network filesystems, under ACLs, and
// inside containers with mapped users, and the rename that installs the
// new binary needs write access to the *directory*, not to the existing
// binary. Non-permission failures — a missing directory, a full disk, a
// read-only mount — are equally install-blocking and surface under the
// same sentinel so callers render one consistent message.
func probeInstallDirWritable(installPath string) error {
	dir := filepath.Dir(installPath)
	probe := filepath.Join(dir, probeFileName)

	// O_TRUNC rather than O_EXCL: a probe file left behind by a crashed
	// run must not lock the tool out of every future upgrade, and O_EXCL
	// would buy no security here — anyone able to pre-plant a symlink in
	// this directory can already overwrite the binary outright.
	f, err := os.OpenFile(probe, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, probeFilePerm) //nolint:gosec // probe path is derived from the install target, not from user input
	if err != nil {
		if errors.Is(err, os.ErrPermission) {
			return fmt.Errorf("%w: %s: %s", ErrInstallDirNotWritable, dir, notWritableGuidance)
		}
		return fmt.Errorf("%w: %s: %w", ErrInstallDirNotWritable, dir, err)
	}

	closeErr := f.Close()
	_ = os.Remove(probe)
	if closeErr != nil {
		return fmt.Errorf("%w: %s: %w", ErrInstallDirNotWritable, dir, closeErr)
	}
	return nil
}
