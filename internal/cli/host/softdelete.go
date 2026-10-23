package host

import (
	"fmt"
	"os"
	"path/filepath"
)

// moveToTrash moves a definition aside (soft delete). The parent directory is
// created with the platform's group-writable permissions, umask-independent.
func moveToTrash(src, dst string) error {
	dir := filepath.Dir(dst)
	if err := os.MkdirAll(dir, 0o770|os.ModeSetgid); err != nil {
		return err
	}
	if err := os.Chmod(dir, 0o770|os.ModeSetgid); err != nil {
		return err
	}
	return os.Rename(src, dst)
}

// restoreFromTrash moves a soft-deleted definition back into place.
func restoreFromTrash(src, dst string) error {
	if _, err := os.Stat(dst); err == nil {
		return fmt.Errorf("%s already exists", dst)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o770|os.ModeSetgid); err != nil {
		return err
	}
	return os.Rename(src, dst)
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
