// Package atomicfile writes files so that a crash leaves either the old file
// or the new one, never a partial file (docs/requirements.md D54). Temp files
// carry a fixed name pattern so an interrupted run's leftovers can be found
// and removed (D58).
package atomicfile

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	tempPrefix = ".pimvault-"
	tempSuffix = ".tmp"
)

// IsTemp reports whether a file name is one of Write's temp files.
func IsTemp(name string) bool {
	return strings.HasPrefix(name, tempPrefix) && strings.HasSuffix(name, tempSuffix)
}

// CleanDir removes temp files that an interrupted Write left directly in dir
// (D58), those last written at least minAge ago: a younger one may belong to
// a Write still going on in another process. A missing dir has nothing to
// clean.
func CleanDir(dir string, minAge time.Duration) error {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}

	if err != nil {
		return fmt.Errorf("remove temp files in %s: %w", dir, err)
	}

	var errs []error

	for _, e := range entries {
		if !e.Type().IsRegular() || !IsTemp(e.Name()) {
			continue
		}

		if info, infoErr := e.Info(); minAge > 0 && (infoErr != nil || time.Since(info.ModTime()) < minAge) {
			continue
		}

		if removeErr := os.Remove(filepath.Join(dir, e.Name())); removeErr != nil {
			errs = append(errs, removeErr)
		}
	}

	if joined := errors.Join(errs...); joined != nil {
		return fmt.Errorf("remove temp files in %s: %w", dir, joined)
	}

	return nil
}

// Write writes data to a temp file in path's directory, syncs it, renames it
// over path and syncs the directory. The directory must exist.
func Write(path string, data []byte, perm fs.FileMode) (err error) {
	dir := filepath.Dir(path)

	tmp, err := os.CreateTemp(dir, tempPrefix+"*"+tempSuffix)
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}

	tmpName := tmp.Name()
	closed := false

	// On failure, remove the temp file and report any cleanup problem with
	// the original error.
	defer func() {
		if err == nil {
			return
		}

		if !closed {
			if closeErr := tmp.Close(); closeErr != nil {
				err = errors.Join(err, fmt.Errorf("close temp file: %w", closeErr))
			}
		}

		if removeErr := os.Remove(tmpName); removeErr != nil && !errors.Is(removeErr, fs.ErrNotExist) {
			err = errors.Join(err, fmt.Errorf("remove temp file: %w", removeErr))
		}
	}()

	if err := tmp.Chmod(perm); err != nil {
		return fmt.Errorf("set temp file permissions: %w", err)
	}

	if _, err := tmp.Write(data); err != nil {
		return fmt.Errorf("write temp file: %w", err)
	}

	if err := tmp.Sync(); err != nil {
		return fmt.Errorf("sync temp file: %w", err)
	}

	closed = true
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp file: %w", err)
	}

	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("rename temp file: %w", err)
	}

	return SyncDir(dir)
}

// SyncDir makes renames and removals in dir durable across a crash. After a
// rename, a failure here means the change is visible but not yet durable;
// repeating the same write is a harmless retry.
func SyncDir(dir string) error {
	d, err := os.Open(dir) //nolint:gosec // G304: dir is a directory the caller just wrote to.
	if err != nil {
		return fmt.Errorf("open directory for sync: %w", err)
	}

	syncErr := d.Sync()
	closeErr := d.Close()

	if syncErr != nil {
		return fmt.Errorf("sync directory: %w", syncErr)
	}

	if closeErr != nil {
		return fmt.Errorf("close directory: %w", closeErr)
	}

	return nil
}
