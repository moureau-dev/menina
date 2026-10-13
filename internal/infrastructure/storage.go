package infrastructure

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"
)

type Storage struct {
	DataDir string
}

func NewStorage(dataDir string) *Storage {
	return &Storage{DataDir: dataDir}
}

func (s *Storage) EnsureDirs() error {
	for _, d := range []string{s.DataDir, filepath.Join(s.DataDir, "registry"), filepath.Join(s.DataDir, "releases"), filepath.Join(s.DataDir, "backups"), filepath.Join(s.DataDir, "certs")} {
		if err := os.MkdirAll(d, 0o750); err != nil {
			return fmt.Errorf("create %s: %w", d, err)
		}
	}
	return nil
}

type DiskStats struct {
	TotalBytes uint64
	FreeBytes  uint64
}

func (s *Storage) DiskUsage(path string) (DiskStats, error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return DiskStats{}, err
	}
	return DiskStats{
		TotalBytes: stat.Blocks * uint64(stat.Bsize),
		FreeBytes:  stat.Bavail * uint64(stat.Bsize),
	}, nil
}

func (s *Storage) CheckSpace(path string, minFreeBytes uint64) error {
	stats, err := s.DiskUsage(path)
	if err != nil {
		return err
	}
	if stats.FreeBytes < minFreeBytes {
		return fmt.Errorf("insufficient disk space at %s: %d bytes free, need at least %d", path, stats.FreeBytes, minFreeBytes)
	}
	return nil
}

func CopyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o750); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Sync()
}

func (s *Storage) BackupDatabase(ctx context.Context, backupPath string) error {
	_ = ctx
	return nil
}
