package registry

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestResolveTagStripsRegistryHost(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Docker-Content-Digest", "sha256:abc")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	host := strings.TrimPrefix(srv.URL, "http://")
	c := NewClient(srv.URL, "", "", false)

	digest, err := c.ResolveTag(context.Background(), host+"/project/api", "production")
	if err != nil {
		t.Fatal(err)
	}
	if digest != "sha256:abc" {
		t.Fatalf("digest = %q", digest)
	}
	if gotPath != "/v2/project/api/manifests/production" {
		t.Fatalf("request path = %q, want /v2/project/api/manifests/production", gotPath)
	}
}

func TestResolveTagWithoutRegistryHost(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Docker-Content-Digest", "sha256:def")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	c := NewClient(srv.URL, "", "", false)
	if _, err := c.ResolveTag(context.Background(), "project/api", "dev"); err != nil {
		t.Fatal(err)
	}
	if gotPath != "/v2/project/api/manifests/dev" {
		t.Fatalf("request path = %q", gotPath)
	}
}

func TestListTagsStripsRegistryHost(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Write([]byte(`{"name":"project/api","tags":["a","b"]}`))
	}))
	defer srv.Close()
	host := strings.TrimPrefix(srv.URL, "http://")
	c := NewClient(srv.URL, "", "", false)
	tags, err := c.ListTags(context.Background(), host+"/project/api")
	if err != nil || len(tags) != 2 {
		t.Fatalf("tags = %v err=%v", tags, err)
	}
	if gotPath != "/v2/project/api/tags/list" {
		t.Fatalf("request path = %q", gotPath)
	}
}
