package backup

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/moureau-dev/menina/internal/deployment"
)

// VolumeSpec identifies a PersistentVolumeClaim to capture or restore.
type VolumeSpec struct {
	Namespace    string
	Workload     string // Deployment to quiesce while capturing (may be empty)
	PVC          string
	Size         string
	StorageClass string
	RelPath      string // path within the archive (e.g. volumes/my-app/production/api)
}

// VolumeManager captures and restores PVC contents.
type VolumeManager interface {
	// Capture copies the contents of spec's PVC into dstDir. When consistent is
	// true and spec.Workload is set, the workload is scaled to zero for the copy
	// and restored afterwards.
	Capture(ctx context.Context, spec VolumeSpec, dstDir string, consistent bool) error
	// Restore ensures the PVC exists and copies srcDir's contents into it. When
	// consistent is true and spec.Workload is set, the workload is scaled down
	// during the copy and back afterwards.
	Restore(ctx context.Context, spec VolumeSpec, srcDir string, consistent bool) error
}

// LocalPathVolumeManager reads PVC data directly from the k3s local-path
// provisioner's storage directory on the node (requires root). This is the
// fast path; it only works with the local-path storage class / single node.
type LocalPathVolumeManager struct {
	StorageDir string
	Cluster    deployment.Cluster
	Log        interface{ Warn(string, ...any) }
}

// DefaultLocalPathStorageDir is where k3s's local-path provisioner stores PVCs.
const DefaultLocalPathStorageDir = "/var/lib/rancher/k3s/storage"

func (m *LocalPathVolumeManager) dir() string {
	if m.StorageDir != "" {
		return m.StorageDir
	}
	return DefaultLocalPathStorageDir
}

func (m *LocalPathVolumeManager) find(spec VolumeSpec) (string, error) {
	pattern := filepath.Join(m.dir(), "pvc-*_"+spec.Namespace+"_"+spec.PVC)
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return "", err
	}
	switch len(matches) {
	case 1:
		return matches[0], nil
	case 0:
		return "", fmt.Errorf("no local-path volume for %s/%s (is the storage class local-path?)", spec.Namespace, spec.PVC)
	default:
		return "", fmt.Errorf("multiple local-path volumes match %s/%s", spec.Namespace, spec.PVC)
	}
}

func (m *LocalPathVolumeManager) Capture(ctx context.Context, spec VolumeSpec, dstDir string, consistent bool) error {
	dir, err := m.find(spec)
	if err != nil {
		return err
	}
	restore := m.quiesce(ctx, spec, consistent)
	defer restore()
	return CopyDirContents(dir, dstDir)
}

func (m *LocalPathVolumeManager) Restore(ctx context.Context, spec VolumeSpec, srcDir string, consistent bool) error {
	restore := m.quiesce(ctx, spec, consistent)
	defer restore()
	if m.Cluster != nil {
		if err := m.Cluster.EnsurePVC(ctx, spec.Namespace, spec.PVC, spec.Size, spec.StorageClass); err != nil {
			return fmt.Errorf("ensure pvc %s/%s: %w", spec.Namespace, spec.PVC, err)
		}
	}
	dir, err := m.find(spec)
	if err != nil {
		return err
	}
	return CopyDirContents(srcDir, dir)
}

// quiesce scales spec.Workload to zero (when consistent) and returns a function
// that scales it back. With consistent=false it is a no-op (crash-consistent).
func (m *LocalPathVolumeManager) quiesce(ctx context.Context, spec VolumeSpec, consistent bool) func() {
	if !consistent || spec.Workload == "" || m.Cluster == nil {
		return func() {}
	}
	replicas := 1
	if st, err := m.Cluster.WorkloadStatus(ctx, spec.Namespace, spec.Workload); err == nil && st.Replicas > 0 {
		replicas = st.Replicas
	}
	if err := m.Cluster.ScaleWorkload(ctx, spec.Namespace, spec.Workload, 0); err != nil {
		m.warn("could not scale "+spec.Workload+" to zero for a consistent copy; capturing live", "error", err)
		return func() {}
	}
	return func() {
		if err := m.Cluster.ScaleWorkload(context.Background(), spec.Namespace, spec.Workload, replicas); err != nil {
			m.warn("could not scale "+spec.Workload+" back up after the copy", "error", err)
		}
	}
}

func (m *LocalPathVolumeManager) warn(msg string, args ...any) {
	if m.Log != nil {
		m.Log.Warn(msg, args...)
	}
}

// CopyDirContents recursively copies the contents of src into dst (dst is
// created if needed), preserving modes.
func CopyDirContents(src, dst string) error {
	if err := os.MkdirAll(dst, 0o750); err != nil {
		return err
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, e := range entries {
		s := filepath.Join(src, e.Name())
		d := filepath.Join(dst, e.Name())
		info, err := e.Info()
		if err != nil {
			return err
		}
		switch {
		case info.Mode()&os.ModeSymlink != 0:
			link, err := os.Readlink(s)
			if err != nil {
				return err
			}
			_ = os.Remove(d)
			if err := os.Symlink(link, d); err != nil {
				return err
			}
		case e.IsDir():
			if err := CopyDirContents(s, d); err != nil {
				return err
			}
		default:
			if err := copyFile(s, d, info.Mode().Perm()); err != nil {
				return err
			}
		}
	}
	return nil
}

func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
