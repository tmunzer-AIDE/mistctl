// Package fsutil holds small file helpers shared by the config and cache.
package fsutil

import (
	"os"
	"path/filepath"
)

// WriteFileAtomic writes data to path through a uniquely named temporary
// file in the same directory, then renames it into place. The file is
// created with mode 0600 (enforced on the open descriptor, so a pre-existing
// file or umask cannot widen it), every write and close error is checked, and
// the temporary file is removed on failure. An existing symlink at path is
// replaced, not followed.
func WriteFileAtomic(path string, data []byte) (err error) {
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer func() {
		if err != nil {
			f.Close()
			os.Remove(tmp)
		}
	}()
	if err = f.Chmod(0o600); err != nil {
		return err
	}
	if _, err = f.Write(data); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
