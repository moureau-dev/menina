package schema

import (
	"encoding/json"
	"testing"
)

func TestEmbeddedManifestSchemaIsValidJSON(t *testing.T) {
	var doc map[string]any
	if err := json.Unmarshal(Manifest, &doc); err != nil {
		t.Fatalf("embedded schema is not valid JSON: %v", err)
	}
	if id, _ := doc["$id"].(string); id != ManifestURL {
		t.Fatalf("$id = %q, want %q", id, ManifestURL)
	}
}
