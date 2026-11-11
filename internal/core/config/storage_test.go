package config

import (
	"encoding/json"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestStorageDecodesLegacyYAMLObject(t *testing.T) {
	var app AppFile
	doc := `name: api
storage:
  enabled: true
  size: 1Gi
  mountPath: /data
`
	if err := yaml.Unmarshal([]byte(doc), &app); err != nil {
		t.Fatal(err)
	}
	if len(app.Storage) != 1 {
		t.Fatalf("storage = %+v", app.Storage)
	}
	got := app.Storage[0]
	if got.Name != DefaultStorageName || !got.Enabled || got.Size != "1Gi" || got.MountPath != "/data" {
		t.Fatalf("storage = %+v", got)
	}
}

func TestStorageDecodesNamedYAMLList(t *testing.T) {
	var app AppFile
	doc := `name: api
storage:
  - name: data
    size: 1Gi
    mountPath: /data
  - name: cache
    size: 512Mi
    mountPath: /cache
`
	if err := yaml.Unmarshal([]byte(doc), &app); err != nil {
		t.Fatal(err)
	}
	if len(app.Storage) != 2 {
		t.Fatalf("storage = %+v", app.Storage)
	}
	for _, storage := range app.Storage {
		if !storage.Enabled {
			t.Fatalf("storage %q should default to enabled", storage.Name)
		}
	}
	if app.Storage[1].Name != "cache" || app.Storage[1].MountPath != "/cache" {
		t.Fatalf("storage = %+v", app.Storage[1])
	}
}

func TestStorageYAMLListRequiresNames(t *testing.T) {
	var app AppFile
	doc := "name: api\nstorage:\n  - name: data\n    size: 1Gi\n    mountPath: /data\n  - size: 512Mi\n    mountPath: /cache\n"
	err := yaml.Unmarshal([]byte(doc), &app)
	if err == nil || !strings.Contains(err.Error(), "needs a name") {
		t.Fatalf("expected a naming error, got %v", err)
	}
}

func TestStorageSingleEntryYAMLListDefaultsName(t *testing.T) {
	var app AppFile
	doc := "name: api\nstorage:\n  - size: 1Gi\n    mountPath: /data\n"
	if err := yaml.Unmarshal([]byte(doc), &app); err != nil {
		t.Fatal(err)
	}
	if len(app.Storage) != 1 || app.Storage[0].Name != DefaultStorageName {
		t.Fatalf("storage = %+v", app.Storage)
	}
}

func TestStorageSingleYAMLWritesLegacyShape(t *testing.T) {
	app := AppFile{Name: "api", Storage: Storages{{Name: DefaultStorageName, Enabled: true, Size: "1Gi", MountPath: "/data"}}}
	data, err := yaml.Marshal(app)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "- name:") {
		t.Fatalf("single data storage should stay an object:\n%s", data)
	}
}

func TestStorageDecodesLegacyJSONSnapshot(t *testing.T) {
	var app ResolvedApp
	doc := `{"Name":"api","Storage":{"Enabled":true,"Size":"1Gi","MountPath":"/data"}}`
	if err := json.Unmarshal([]byte(doc), &app); err != nil {
		t.Fatal(err)
	}
	if len(app.Storage) != 1 || app.Storage[0].Name != DefaultStorageName || app.Storage[0].MountPath != "/data" {
		t.Fatalf("storage = %+v", app.Storage)
	}
}

func TestStorageDecodesJSONList(t *testing.T) {
	var app ResolvedApp
	doc := `{"Name":"api","Storage":[{"Name":"data","Enabled":true,"Size":"1Gi","MountPath":"/data"},{"Name":"cache","Enabled":true,"Size":"512Mi","MountPath":"/cache"}]}`
	if err := json.Unmarshal([]byte(doc), &app); err != nil {
		t.Fatal(err)
	}
	if len(app.Storage) != 2 || app.Storage[1].Name != "cache" {
		t.Fatalf("storage = %+v", app.Storage)
	}
}

func TestStorageActiveSkipsDisabled(t *testing.T) {
	storage := Storages{
		{Name: "data", Enabled: true},
		{Name: "old", Enabled: false},
	}
	active := storage.Active()
	if len(active) != 1 || active[0].Name != "data" {
		t.Fatalf("active = %+v", active)
	}
}

func TestStoragePVCName(t *testing.T) {
	if got := StoragePVCName("api", ""); got != "api-data" {
		t.Fatalf("pvc = %q", got)
	}
	if got := StoragePVCName("api", "cache"); got != "api-cache" {
		t.Fatalf("pvc = %q", got)
	}
}

func TestNormalizeStoragesRejectsDuplicates(t *testing.T) {
	storage := Storages{
		{Name: "data", Enabled: true},
		{Name: "data", Enabled: true},
	}
	if err := normalizeStorages(storage); err == nil || !strings.Contains(err.Error(), "more than once") {
		t.Fatalf("expected a duplicate-name error, got %v", err)
	}

	storage = Storages{
		{Name: "data", Enabled: true, MountPath: "/data"},
		{Name: "cache", Enabled: true, MountPath: "/data"},
	}
	if err := normalizeStorages(storage); err == nil || !strings.Contains(err.Error(), "same mount path") {
		t.Fatalf("expected a duplicate-path error, got %v", err)
	}
}

func TestNormalizeStoragesDefaultsLegacyName(t *testing.T) {
	storage := Storages{{Enabled: true, MountPath: "/data"}}
	if err := normalizeStorages(storage); err != nil {
		t.Fatal(err)
	}
	if storage[0].Name != DefaultStorageName {
		t.Fatalf("name = %q", storage[0].Name)
	}
}
