package backup

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/crypto/scrypt"

	"github.com/moureau-dev/menina/internal/core/config"
	"github.com/moureau-dev/menina/internal/core/state/sqlite"
)

// Archive entry names.
const (
	fileDB       = "menina.db"
	fileManifest = "manifest.json"
	dirConfig    = "config"
	dirCerts     = "certs"
	dirSecrets   = "secrets"
	dirVolumes   = "volumes"
	dirRegistry  = "registry"
)

// FormatMagic is prepended to encrypted archives.
var FormatMagic = []byte("MNABK001")

// Manifest describes an archive's contents.
type Manifest struct {
	Format        string            `json:"format"`
	FormatVersion int               `json:"formatVersion"`
	CreatedAt     time.Time         `json:"createdAt"`
	Hostname      string            `json:"hostname"`
	MeninaVersion string            `json:"meninaVersion"`
	Components    []string          `json:"components"`
	Checksums     map[string]string `json:"checksums,omitempty"`
	Volumes       []VolumeSpec      `json:"volumes,omitempty"`
	Registry      *VolumeSpec       `json:"registry,omitempty"`
	Encrypted     bool              `json:"encrypted,omitempty"`
}

const formatVersion = 1

// CreateOptions configures Create.
type CreateOptions struct {
	ConfigDir     string
	DataDir       string
	DBPath        string
	Out           string
	WithVolumes   bool
	WithImages    bool
	WithSecrets   bool
	Consistent    bool
	Passphrase    string
	MeninaVersion string
	DB            *sqlite.DB
	Config        *config.Config
	Volumes       VolumeManager
	Log           *slog.Logger
}

// Create builds a tar.gz backup bundle and returns its path and manifest.
func Create(ctx context.Context, o CreateOptions) (string, *Manifest, error) {
	staging, err := os.MkdirTemp("", "menina-backup-*")
	if err != nil {
		return "", nil, err
	}
	defer os.RemoveAll(staging)

	m := &Manifest{
		Format:        "menina-backup",
		FormatVersion: formatVersion,
		CreatedAt:     time.Now().UTC(),
		MeninaVersion: o.MeninaVersion,
		Checksums:     map[string]string{},
	}
	if h, err := os.Hostname(); err == nil {
		m.Hostname = h
	}

	// Database (consistent online snapshot).
	if o.DB != nil {
		if err := o.DB.Backup(ctx, filepath.Join(staging, fileDB)); err != nil {
			return "", nil, fmt.Errorf("snapshot database: %w", err)
		}
		m.Components = append(m.Components, "db")
	}

	// Config: config.yml + projects/, plus secrets/ only when requested.
	cfgDir := filepath.Join(staging, dirConfig)
	if err := os.MkdirAll(cfgDir, 0o750); err != nil {
		return "", nil, err
	}
	if err := copyIfExists(filepath.Join(o.ConfigDir, "config.yml"), filepath.Join(cfgDir, "config.yml")); err != nil {
		return "", nil, err
	}
	if err := copyTreeIfExists(filepath.Join(o.ConfigDir, "projects"), filepath.Join(cfgDir, "projects")); err != nil {
		return "", nil, err
	}
	if err := copyTreeIfExists(filepath.Join(o.ConfigDir, ".revisions"), filepath.Join(cfgDir, ".revisions")); err != nil {
		return "", nil, err
	}
	m.Components = append(m.Components, "config")
	if o.WithSecrets {
		if err := copyTreeIfExists(filepath.Join(o.ConfigDir, dirSecrets), filepath.Join(cfgDir, dirSecrets)); err != nil {
			return "", nil, err
		}
		m.Components = append(m.Components, "secrets")
	}

	// TLS material (small; re-issued by ACME if absent).
	if err := copyTreeIfExists(filepath.Join(o.DataDir, dirCerts), filepath.Join(staging, dirCerts)); err != nil {
		return "", nil, err
	}
	if _, err := os.Stat(filepath.Join(o.DataDir, dirCerts)); err == nil {
		m.Components = append(m.Components, "certs")
	}

	// Volumes (app PVCs) and registry images (Zot PVC).
	if o.WithVolumes && o.Volumes != nil && o.Config != nil {
		specs, err := appVolumeSpecs(o.Config)
		if err != nil {
			return "", nil, err
		}
		for _, spec := range specs {
			dst := filepath.Join(staging, filepath.FromSlash(spec.RelPath))
			if err := o.Volumes.Capture(ctx, spec, dst, o.Consistent); err != nil {
				return "", nil, fmt.Errorf("capture volume %s: %w", spec.RelPath, err)
			}
			m.Volumes = append(m.Volumes, spec)
		}
		m.Components = append(m.Components, "volumes")
	}
	if o.WithImages && o.Volumes != nil && o.Config != nil {
		spec := registryVolumeSpec(o.Config)
		dst := filepath.Join(staging, filepath.FromSlash(spec.RelPath))
		if err := o.Volumes.Capture(ctx, spec, dst, o.Consistent); err != nil {
			return "", nil, fmt.Errorf("capture registry: %w", err)
		}
		m.Registry = &spec
		m.Components = append(m.Components, "registry")
	}

	// Checksums for the cheap, essential files.
	for _, rel := range []string{fileDB, dirConfig} {
		if err := checksumTree(filepath.Join(staging, rel), rel, m.Checksums); err != nil {
			return "", nil, err
		}
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return "", nil, err
	}
	if err := os.WriteFile(filepath.Join(staging, fileManifest), data, 0o640); err != nil {
		return "", nil, err
	}

	out := o.Out
	if out == "" {
		out = filepath.Join(o.DataDir, "backups", fmt.Sprintf("menina-%s.tar.gz", m.CreatedAt.Format("20060102T150405Z")))
	}
	if err := os.MkdirAll(filepath.Dir(out), 0o750); err != nil {
		return "", nil, err
	}
	if err := tarGz(staging, out); err != nil {
		return "", nil, fmt.Errorf("archive: %w", err)
	}
	if o.Passphrase != "" {
		enc := out + ".enc"
		if err := EncryptFile(out, enc, o.Passphrase); err != nil {
			return "", nil, err
		}
		_ = os.Remove(out)
		out = enc
		m.Encrypted = true
	}
	return out, m, nil
}

// RestoreOptions configures Restore.
type RestoreOptions struct {
	Archive     string
	ConfigDir   string
	DataDir     string
	DBPath      string
	WithVolumes bool
	WithImages  bool
	Consistent  bool
	Passphrase  string
	Force       bool
	Volumes     VolumeManager
	Config      *config.Config
	Log         *slog.Logger
}

// Restore extracts an archive and applies it, returning the manifest.
func Restore(ctx context.Context, o RestoreOptions) (*Manifest, error) {
	if !o.Force {
		if _, err := os.Stat(o.DBPath); err == nil {
			return nil, fmt.Errorf("%s already exists; re-run with --force to overwrite", o.DBPath)
		}
		if _, err := os.Stat(filepath.Join(o.ConfigDir, "config.yml")); err == nil {
			return nil, fmt.Errorf("%s/config.yml already exists; re-run with --force to overwrite", o.ConfigDir)
		}
	}

	staging, err := os.MkdirTemp("", "menina-restore-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(staging)

	archive := o.Archive
	encrypted := isEncrypted(o.Archive)
	if encrypted {
		if o.Passphrase == "" {
			return nil, fmt.Errorf("archive is encrypted; provide the passphrase")
		}
		plain := filepath.Join(staging, "backup.tar.gz")
		if err := DecryptFile(o.Archive, plain, o.Passphrase); err != nil {
			return nil, err
		}
		archive = plain
	}
	extract := filepath.Join(staging, "x")
	if err := os.MkdirAll(extract, 0o750); err != nil {
		return nil, err
	}
	if err := untarGz(archive, extract); err != nil {
		return nil, fmt.Errorf("extract: %w", err)
	}

	raw, err := os.ReadFile(filepath.Join(extract, fileManifest))
	if err != nil {
		return nil, fmt.Errorf("archive has no manifest.json: %w", err)
	}
	var m Manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("parse manifest: %w", err)
	}
	if m.Format != "menina-backup" {
		return nil, fmt.Errorf("unrecognized archive format %q", m.Format)
	}

	// Database.
	if _, err := os.Stat(filepath.Join(extract, fileDB)); err == nil {
		if err := os.MkdirAll(filepath.Dir(o.DBPath), 0o750); err != nil {
			return nil, err
		}
		if err := copyFile(filepath.Join(extract, fileDB), o.DBPath, 0o640); err != nil {
			return nil, err
		}
	}

	// Config (+ secrets).
	cfgSrc := filepath.Join(extract, dirConfig)
	if err := copyIfExists(filepath.Join(cfgSrc, "config.yml"), filepath.Join(o.ConfigDir, "config.yml")); err != nil {
		return nil, err
	}
	if err := copyTreeIfExists(filepath.Join(cfgSrc, "projects"), filepath.Join(o.ConfigDir, "projects")); err != nil {
		return nil, err
	}
	if err := copyTreeIfExists(filepath.Join(cfgSrc, ".revisions"), filepath.Join(o.ConfigDir, ".revisions")); err != nil {
		return nil, err
	}
	if err := copyTreeIfExists(filepath.Join(cfgSrc, dirSecrets), filepath.Join(o.ConfigDir, dirSecrets)); err != nil {
		return nil, err
	}

	// TLS material.
	if err := copyTreeIfExists(filepath.Join(extract, dirCerts), filepath.Join(o.DataDir, dirCerts)); err != nil {
		return nil, err
	}

	// Volumes.
	if o.WithVolumes && o.Volumes != nil {
		for _, spec := range m.Volumes {
			src := filepath.Join(extract, filepath.FromSlash(spec.RelPath))
			if err := o.Volumes.Restore(ctx, spec, src, o.Consistent); err != nil {
				return nil, fmt.Errorf("restore volume %s: %w", spec.RelPath, err)
			}
		}
	}
	if o.WithImages && o.Volumes != nil && m.Registry != nil {
		src := filepath.Join(extract, filepath.FromSlash(m.Registry.RelPath))
		if err := o.Volumes.Restore(ctx, *m.Registry, src, o.Consistent); err != nil {
			return nil, fmt.Errorf("restore registry: %w", err)
		}
	}
	m.Encrypted = encrypted
	return &m, nil
}

// --- helpers ---

func appVolumeSpecs(cfg *config.Config) ([]VolumeSpec, error) {
	projects, err := config.LoadAllProjects(config.ProjectsDir(cfg.Host.ConfigDir))
	if err != nil {
		return nil, err
	}
	resolved, err := config.NewResolver(cfg).Resolve(projects, 0)
	if err != nil {
		return nil, err
	}
	var out []VolumeSpec
	for _, rp := range resolved.Projects {
		for _, re := range rp.Environments {
			for _, app := range re.Apps {
				if !app.Enabled || app.Storage == nil || !app.Storage.Enabled {
					continue
				}
				size := app.Storage.Size
				if size == "" {
					size = "1Gi"
				}
				out = append(out, VolumeSpec{
					Namespace: app.Namespace,
					Workload:  app.Name,
					PVC:       app.Name + "-data",
					Size:      size,
					RelPath:   filepath.ToSlash(filepath.Join(dirVolumes, app.Project, app.Environment, app.Name)),
				})
			}
		}
	}
	return out, nil
}

func registryVolumeSpec(cfg *config.Config) VolumeSpec {
	ns := cfg.Registry.Namespace
	if ns == "" {
		ns = "menina-registry"
	}
	return VolumeSpec{
		Namespace:    ns,
		Workload:     "zot",
		PVC:          "zot-data",
		Size:         orDefault(cfg.Registry.PVCSize, "10Gi"),
		StorageClass: cfg.Registry.StorageClass,
		RelPath:      dirRegistry + "/zot-data",
	}
}

func orDefault(v, def string) string {
	if v != "" {
		return v
	}
	return def
}

func copyIfExists(src, dst string) error {
	if _, err := os.Stat(src); err != nil {
		return nil
	}
	return copyFile(src, dst, 0o640)
}

func copyTreeIfExists(src, dst string) error {
	if _, err := os.Stat(src); err != nil {
		return nil
	}
	return CopyDirContents(src, dst)
}

func checksumTree(path, prefix string, sums map[string]string) error {
	info, err := os.Stat(path)
	if err != nil {
		return nil
	}
	if !info.IsDir() {
		sum, err := sha256File(path)
		if err != nil {
			return err
		}
		sums[filepath.ToSlash(prefix)] = sum
		return nil
	}
	return filepath.Walk(path, func(p string, fi os.FileInfo, err error) error {
		if err != nil || fi.IsDir() {
			return err
		}
		sum, err := sha256File(p)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(filepath.Dir(path), p)
		sums[filepath.ToSlash(rel)] = sum
		return nil
	})
}

func sha256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil)), nil
}

func tarGz(srcDir, out string) error {
	f, err := os.Create(out)
	if err != nil {
		return err
	}
	defer f.Close()
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	err = filepath.Walk(srcDir, func(path string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(srcDir, path)
		if err != nil || rel == "." {
			return err
		}
		hdr, err := tar.FileInfoHeader(fi, "")
		if err != nil {
			return err
		}
		hdr.Name = filepath.ToSlash(rel)
		if fi.IsDir() {
			hdr.Name += "/"
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		if fi.Mode().IsRegular() {
			in, err := os.Open(path)
			if err != nil {
				return err
			}
			_, err = io.Copy(tw, in)
			in.Close()
			return err
		}
		return nil
	})
	if err != nil {
		return err
	}
	if err := tw.Close(); err != nil {
		return err
	}
	return gz.Close()
}

func untarGz(archive, dst string) error {
	f, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		clean := filepath.Clean(hdr.Name)
		if clean == "." || strings.HasPrefix(clean, "..") || filepath.IsAbs(clean) {
			return fmt.Errorf("unsafe path in archive: %q", hdr.Name)
		}
		target := filepath.Join(dst, clean)
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, os.FileMode(hdr.Mode)&os.ModePerm); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
				return err
			}
			out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(hdr.Mode)&os.ModePerm)
			if err != nil {
				return err
			}
			if _, err := io.Copy(out, tr); err != nil {
				out.Close()
				return err
			}
			out.Close()
		case tar.TypeSymlink:
			_ = os.Remove(target)
			if err := os.Symlink(hdr.Linkname, target); err != nil {
				return err
			}
		}
	}
}

// --- optional passphrase encryption (chunked AES-GCM) ---

func isEncrypted(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	buf := make([]byte, len(FormatMagic))
	if _, err := io.ReadFull(f, buf); err != nil {
		return false
	}
	return string(buf) == string(FormatMagic)
}

const encChunk = 64 * 1024

// EncryptFile encrypts src to dst with a passphrase (scrypt-derived key).
func EncryptFile(src, dst, passphrase string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	salt := make([]byte, 16)
	baseNonce := make([]byte, 12)
	if _, err := rand.Read(salt); err != nil {
		return err
	}
	if _, err := rand.Read(baseNonce); err != nil {
		return err
	}
	key, err := scrypt.Key([]byte(passphrase), salt, 1<<15, 8, 1, 32)
	if err != nil {
		return err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return err
	}
	if _, err := out.Write(FormatMagic); err != nil {
		return err
	}
	if _, err := out.Write(salt); err != nil {
		return err
	}
	if _, err := out.Write(baseNonce); err != nil {
		return err
	}
	buf := make([]byte, encChunk)
	var counter uint32
	for {
		n, rerr := io.ReadFull(in, buf)
		if n > 0 {
			nonce := chunkNonce(baseNonce, counter)
			sealed := gcm.Seal(nil, nonce, buf[:n], nil)
			var lenBuf [4]byte
			binary.BigEndian.PutUint32(lenBuf[:], uint32(len(sealed)))
			if _, err := out.Write(lenBuf[:]); err != nil {
				return err
			}
			if _, err := out.Write(sealed); err != nil {
				return err
			}
			counter++
		}
		if rerr == io.EOF || rerr == io.ErrUnexpectedEOF {
			return out.Sync()
		}
		if rerr != nil {
			return rerr
		}
	}
}

// DecryptFile decrypts an archive produced by EncryptFile.
func DecryptFile(src, dst, passphrase string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	magic := make([]byte, len(FormatMagic))
	if _, err := io.ReadFull(in, magic); err != nil {
		return err
	}
	if string(magic) != string(FormatMagic) {
		return fmt.Errorf("not an encrypted Menina archive")
	}
	salt := make([]byte, 16)
	baseNonce := make([]byte, 12)
	if _, err := io.ReadFull(in, salt); err != nil {
		return err
	}
	if _, err := io.ReadFull(in, baseNonce); err != nil {
		return err
	}
	key, err := scrypt.Key([]byte(passphrase), salt, 1<<15, 8, 1, 32)
	if err != nil {
		return err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return err
	}
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	var counter uint32
	for {
		var lenBuf [4]byte
		if _, err := io.ReadFull(in, lenBuf[:]); err != nil {
			if err == io.EOF {
				return out.Sync()
			}
			return err
		}
		sealed := make([]byte, binary.BigEndian.Uint32(lenBuf[:]))
		if _, err := io.ReadFull(in, sealed); err != nil {
			return err
		}
		plain, err := gcm.Open(nil, chunkNonce(baseNonce, counter), sealed, nil)
		if err != nil {
			return fmt.Errorf("decrypt failed (wrong passphrase?): %w", err)
		}
		if _, err := out.Write(plain); err != nil {
			return err
		}
		counter++
	}
}

func chunkNonce(base []byte, counter uint32) []byte {
	nonce := make([]byte, len(base))
	copy(nonce, base)
	binary.BigEndian.PutUint32(nonce[len(nonce)-4:], binary.BigEndian.Uint32(base[len(base)-4:])+counter)
	return nonce
}
