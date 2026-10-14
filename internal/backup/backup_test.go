package backup

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/moureau-dev/menina/internal/config"
	"github.com/moureau-dev/menina/internal/database"
)

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func setupConfig(t *testing.T, dir string, withStorage bool) *config.Config {
	t.Helper()
	cfg := config.Default()
	cfg.Host.ConfigDir = dir
	cfg.Host.DataDir = dir
	cfg.Platform.DBPath = filepath.Join(dir, "menina.db")
	write(t, filepath.Join(dir, "config.yml"), "host:\n  configDir: "+dir+"\n")
	write(t, filepath.Join(dir, "projects", "p", "project.yml"), "name: p\n")
	app := "name: api\nimage:\n  repository: r/api\nworkload:\n  type: service\n  port: 80\n"
	if withStorage {
		app += "storage:\n  enabled: true\n  size: 1Gi\n  mountPath: /data\n"
	}
	write(t, filepath.Join(dir, "projects", "p", "apps", "api.yml"), app)
	write(t, filepath.Join(dir, "projects", "p", "environments", "production.yml"), "name: production\napps:\n  api:\n    enabled: true\n")
	return &cfg
}

func openDB(t *testing.T, path string) *database.DB {
	t.Helper()
	db, err := database.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestCreateRestoreRoundTrip(t *testing.T) {
	ctx := context.Background()
	src := t.TempDir()
	cfg := setupConfig(t, src, false)
	db := openDB(t, cfg.Platform.DBPath)
	if err := db.SetMeta(ctx, "hello", "world"); err != nil {
		t.Fatal(err)
	}
	db.Close()

	db = openDB(t, cfg.Platform.DBPath)
	out := filepath.Join(t.TempDir(), "bundle.tar.gz")
	_, m, err := Create(ctx, CreateOptions{
		ConfigDir: src, DataDir: src, DBPath: cfg.Platform.DBPath, Out: out,
		MeninaVersion: "test", DB: db, Config: cfg,
	})
	if err != nil {
		t.Fatal(err)
	}
	db.Close()
	if _, err := os.Stat(out); err != nil {
		t.Fatalf("archive not written: %v", err)
	}
	if len(m.Checksums) == 0 {
		t.Fatal("manifest has no checksums")
	}

	// Restore into a fresh tree.
	dst := t.TempDir()
	dstCfg := config.Default()
	dstCfg.Host.ConfigDir = dst
	dstCfg.Host.DataDir = dst
	dstCfg.Platform.DBPath = filepath.Join(dst, "menina.db")
	if _, err := Restore(ctx, RestoreOptions{
		Archive: out, ConfigDir: dst, DataDir: dst, DBPath: dstCfg.Platform.DBPath, Force: true,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dst, "projects", "p", "apps", "api.yml")); err != nil {
		t.Fatalf("config not restored: %v", err)
	}
	rdb := openDB(t, dstCfg.Platform.DBPath)
	defer rdb.Close()
	if v, ok, _ := rdb.Meta(ctx, "hello"); !ok || v != "world" {
		t.Fatalf("db meta not restored: %q %v", v, ok)
	}
}

type fakeVols struct {
	captured []VolumeSpec
	restored []VolumeSpec
}

func (f *fakeVols) Capture(_ context.Context, spec VolumeSpec, dst string, _ bool) error {
	f.captured = append(f.captured, spec)
	if err := os.MkdirAll(dst, 0o750); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dst, "data.txt"), []byte(spec.RelPath), 0o600)
}

func (f *fakeVols) Restore(_ context.Context, spec VolumeSpec, src string, _ bool) error {
	f.restored = append(f.restored, spec)
	_, err := os.Stat(filepath.Join(src, "data.txt"))
	return err
}

func TestCreateAndRestoreVolumes(t *testing.T) {
	ctx := context.Background()
	src := t.TempDir()
	cfg := setupConfig(t, src, true)
	db := openDB(t, cfg.Platform.DBPath)
	defer db.Close()

	vols := &fakeVols{}
	out := filepath.Join(t.TempDir(), "v.tar.gz")
	_, m, err := Create(ctx, CreateOptions{
		ConfigDir: src, DataDir: src, DBPath: cfg.Platform.DBPath, Out: out,
		DB: db, Config: cfg, Volumes: vols, WithVolumes: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(vols.captured) != 1 || vols.captured[0].RelPath != "volumes/p/production/api" || vols.captured[0].PVC != "api-data" {
		t.Fatalf("captured = %+v", vols.captured)
	}
	if len(m.Volumes) != 1 {
		t.Fatalf("manifest volumes = %+v", m.Volumes)
	}

	dst := t.TempDir()
	rvols := &fakeVols{}
	if _, err := Restore(ctx, RestoreOptions{
		Archive: out, ConfigDir: dst, DataDir: dst, DBPath: filepath.Join(dst, "menina.db"),
		Force: true, Volumes: rvols, WithVolumes: true,
	}); err != nil {
		t.Fatal(err)
	}
	if len(rvols.restored) != 1 || rvols.restored[0].PVC != "api-data" {
		t.Fatalf("restored = %+v", rvols.restored)
	}
}

func TestEncryptDecryptRoundTrip(t *testing.T) {
	dir := t.TempDir()
	plain := filepath.Join(dir, "in")
	write(t, plain, "some backup bytes \x00\x01\x02 and more")
	enc := filepath.Join(dir, "in.enc")
	if err := EncryptFile(plain, enc, "hunter2"); err != nil {
		t.Fatal(err)
	}
	if !isEncrypted(enc) {
		t.Fatal("encrypted file not detected")
	}
	out := filepath.Join(dir, "out")
	if err := DecryptFile(enc, out, "hunter2"); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(out)
	want, _ := os.ReadFile(plain)
	if string(got) != string(want) {
		t.Fatalf("round trip mismatch: %q != %q", got, want)
	}
	if err := DecryptFile(enc, filepath.Join(dir, "bad"), "wrong"); err == nil {
		t.Fatal("wrong passphrase should fail")
	}
}
