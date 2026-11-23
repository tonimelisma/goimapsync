package state

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// lockFile is the run lock's file in the state directory.
const lockFile = "pimvault.lock"

// ErrLocked means another pimvault run holds the state directory (D186).
var ErrLocked = errors.New("another pimvault run is using the state directory")

// Lock takes the run lock of a state directory, creating the directory when
// missing, so that runs using it never overlap (D186). Runs with different
// state directories are not kept apart, even when they share an archive. The
// lock is an flock on a file holding the process ID; the kernel drops it
// when the process ends, so the lock of a crashed run is taken over by the
// next one. A held lock returns ErrLocked naming the process that holds it.
// unlock releases the lock.
func Lock(dir string) (func() error, error) {
	if err := os.MkdirAll(dir, dirPerm); err != nil {
		return nil, fmt.Errorf("lock state directory: %w", err)
	}

	path := filepath.Join(dir, lockFile)

	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, filePerm) //nolint:gosec // G304: the path is the state directory's lock file.
	if err != nil {
		return nil, fmt.Errorf("lock state directory: %w", err)
	}

	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close() //nolint:errcheck // the lock error is the one to report.

		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, fmt.Errorf("lock state directory %s, held by process %s: %w", dir, holder(path), ErrLocked)
		}

		return nil, fmt.Errorf("lock state directory: %w", err)
	}

	if err := writePID(f); err != nil {
		_ = f.Close() //nolint:errcheck // closing drops the lock; the write error is the one to report.

		return nil, err
	}

	return func() error {
		// Closing the file drops the lock; the file stays for the next run.
		if err := f.Close(); err != nil {
			return fmt.Errorf("unlock state directory: %w", err)
		}

		return nil
	}, nil
}

// writePID records the running process in the lock file, for a second run's
// message. The PID is written before the file is cut to its length, so a
// second run never reads an empty file.
func writePID(f *os.File) error {
	pid := []byte(strconv.Itoa(os.Getpid()) + "\n")

	if _, err := f.WriteAt(pid, 0); err != nil {
		return fmt.Errorf("lock state directory: %w", err)
	}

	if err := f.Truncate(int64(len(pid))); err != nil {
		return fmt.Errorf("lock state directory: %w", err)
	}

	return nil
}

// holder returns the process ID recorded in a lock file, or "unknown".
func holder(path string) string {
	data, err := os.ReadFile(path) //nolint:gosec // G304: as in Lock.
	if pid := strings.TrimSpace(string(data)); err == nil && pid != "" {
		return pid
	}

	return "unknown"
}
