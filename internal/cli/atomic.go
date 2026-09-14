package cli

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

// writeFile writes a file so that the destination is either absent or whole.
//
// The content goes to a temporary file beside the destination, is synced and
// closed, and only then takes the destination's name. An interrupted write
// leaves at most a ".partial" temporary behind, never a file with the name the
// user asked for and half a snapshot in it. Check runs on the finished
// temporary before it is named, so a response that ended early is caught before
// it looks like a result.
//
// Without force an existing destination is never replaced. The name is taken
// with a hard link, which fails if the name exists, so there is no gap between
// checking and naming for another process to write into. On a filesystem
// without hard links it falls back to checking and renaming. With force the
// rename replaces the destination; os.Rename does that atomically on Unix and
// replaces the file on Windows.
//
// The temporary is created with mode 0600, and so the destination is too on
// Unix: a snapshot holds directory data, and a world-readable default would be
// the wrong one.
func writeFile(path string, force bool, write func(io.Writer) error, check func(*os.File) error) (err error) {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".*.partial")
	if err != nil {
		return failf("output", "cannot create a file in %s: %v", dir, err)
	}
	tmpName := tmp.Name()
	closed := false
	defer func() {
		if !closed {
			_ = tmp.Close()
		}
		if err != nil {
			_ = os.Remove(tmpName)
		}
	}()

	if err = write(tmp); err != nil {
		return err
	}
	if err = tmp.Sync(); err != nil {
		return failf("output", "cannot write %s: %v", path, err)
	}
	if check != nil {
		if _, err = tmp.Seek(0, io.SeekStart); err != nil {
			return failf("output", "cannot read back %s: %v", tmpName, err)
		}
		if err = check(tmp); err != nil {
			return err
		}
	}
	if err = tmp.Close(); err != nil {
		return failf("output", "cannot write %s: %v", path, err)
	}
	closed = true

	if force {
		if err = os.Rename(tmpName, path); err != nil {
			return failf("output", "cannot replace %s: %v", path, err)
		}
		return nil
	}
	linkErr := os.Link(tmpName, path)
	switch {
	case linkErr == nil:
		_ = os.Remove(tmpName)
		return nil
	case errors.Is(linkErr, fs.ErrExist):
		err = existsError(path)
		return err
	}
	// No hard links here. Check and rename, which leaves a narrow window rather
	// than none.
	if _, statErr := os.Lstat(path); statErr == nil {
		err = existsError(path)
		return err
	}
	if err = os.Rename(tmpName, path); err != nil {
		return failf("output", "cannot create %s: %v", path, err)
	}
	return nil
}

func existsError(path string) *ExitError {
	return &ExitError{Code: ExitUsage, Local: "output_exists",
		Message: fmt.Sprintf("%s already exists; pass --force to replace it", path)}
}

// refuseExisting fails early, before any work, when an output would be refused
// at the end anyway. writeFile still checks again when it names the file.
func refuseExisting(path string, force bool) error {
	if force || path == "-" {
		return nil
	}
	if _, err := os.Lstat(path); err == nil {
		return existsError(path)
	}
	return nil
}
