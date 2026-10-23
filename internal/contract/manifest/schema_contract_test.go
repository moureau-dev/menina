package manifest

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/moureau-dev/menina/internal/config"
	"github.com/moureau-dev/menina/internal/contract/schema"
)

func schemaProps(t *testing.T, node map[string]any) map[string]bool {
	t.Helper()
	props, _ := node["properties"].(map[string]any)
	out := map[string]bool{}
	for key := range props {
		out[key] = true
	}
	return out
}

func yamlFields(v any) map[string]bool {
	out := map[string]bool{}
	t := reflect.TypeOf(v)
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	for i := 0; i < t.NumField(); i++ {
		tag := t.Field(i).Tag.Get("yaml")
		name := strings.Split(tag, ",")[0]
		if name == "" || name == "-" {
			continue
		}
		out[name] = true
	}
	return out
}

func checkContract(t *testing.T, name string, schemaNode map[string]any, goValue any) {
	t.Helper()
	want := yamlFields(goValue)
	got := schemaProps(t, schemaNode)
	for field := range want {
		if !got[field] {
			t.Errorf("%s: YAML field %q is missing from the schema", name, field)
		}
	}
	for field := range got {
		if !want[field] {
			t.Errorf("%s: schema field %q has no YAML counterpart", name, field)
		}
	}
}

func TestManifestSchemaMatchesStructs(t *testing.T) {
	var root map[string]any
	if err := json.Unmarshal(schema.Manifest, &root); err != nil {
		t.Fatal(err)
	}
	defs, _ := root["$defs"].(map[string]any)
	cases := []struct {
		name   string
		node   map[string]any
		goType any
	}{
		{"File", root, File{}},
		{"Defaults", defs["defaults"].(map[string]any), Defaults{}},
		{"Storage", defs["storage"].(map[string]any), Storage{}},
		{"StorageRef", defs["storageEntry"].(map[string]any), StorageRef{}},
		{"Workload", defs["workload"].(map[string]any), Workload{}},
		{"ServiceSpec", defs["service"].(map[string]any), ServiceSpec{}},
		{"Exposure", defs["exposure"].(map[string]any), Exposure{}},
		{"HTTPExposure", defs["httpExposure"].(map[string]any), HTTPExposure{}},
		{"SourceSpec", defs["source"].(map[string]any), config.SourceSpec{}},
	}
	for _, tc := range cases {
		checkContract(t, tc.name, tc.node, tc.goType)
	}
}

func TestSchemaContractInvariants(t *testing.T) {
	var root map[string]any
	if err := json.Unmarshal(schema.Manifest, &root); err != nil {
		t.Fatal(err)
	}
	if root["$id"] != schema.ManifestURL {
		t.Fatalf("$id = %v, want %s", root["$id"], schema.ManifestURL)
	}
	if root["additionalProperties"] != false {
		t.Fatal("root must set additionalProperties: false")
	}
	required, _ := root["required"].([]any)
	want := map[string]bool{"project": true, "environment": true}
	for _, item := range required {
		if s, ok := item.(string); ok {
			delete(want, s)
		}
	}
	if len(want) != 0 {
		t.Fatalf("missing required fields: %v", want)
	}
	defs, _ := root["$defs"].(map[string]any)
	workload, _ := defs["workload"].(map[string]any)
	if workload == nil || workload["additionalProperties"] != false {
		t.Fatal("workload must set additionalProperties: false")
	}
	props, _ := workload["properties"].(map[string]any)
	for _, field := range []string{"port", "host", "service", "exposure"} {
		if _, ok := props[field]; !ok {
			t.Fatalf("workload schema missing %q", field)
		}
	}
	for _, def := range []string{"service", "exposure", "httpExposure", "source"} {
		if _, ok := defs[def]; !ok {
			t.Fatalf("schema missing $defs/%s", def)
		}
	}
}
