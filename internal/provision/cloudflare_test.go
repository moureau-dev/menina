package provision

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCloudflareUpsertRefusesConflictingRecordType(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"success": true,
				"result": []map[string]any{
					{"id": "rec1", "type": "CNAME", "name": "api.example.com", "content": "other.example.net"},
				},
			})
			return
		}
		t.Fatalf("unexpected write request %s %s (must not clobber the CNAME)", r.Method, r.URL.Path)
	}))
	defer srv.Close()

	c := NewCloudflareClient("token")
	c.BaseURL = srv.URL
	err := c.UpsertDNSRecord(context.Background(), "zone1", "A", "api.example.com", "1.2.3.4", false)
	if err == nil {
		t.Fatal("expected a conflict error when an unrelated record type already owns the name")
	}
}

func TestCloudflareUpsertCreatesWhenAbsent(t *testing.T) {
	var wrote bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "result": []map[string]any{}})
			return
		}
		if r.Method == http.MethodPost {
			wrote = true
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "result": map[string]any{}})
	}))
	defer srv.Close()

	c := NewCloudflareClient("token")
	c.BaseURL = srv.URL
	if err := c.UpsertDNSRecord(context.Background(), "zone1", "A", "api.example.com", "1.2.3.4", false); err != nil {
		t.Fatal(err)
	}
	if !wrote {
		t.Fatal("expected a POST to create the missing record")
	}
}
