package registry

import (
	"context"
	"testing"
)

func TestClientHandles(t *testing.T) {
	c := &Client{BaseURL: "https://registry.moureau.dev"}
	cases := []struct {
		repo string
		want bool
	}{
		{"registry.moureau.dev/my-project/api", true},
		{"registry.moureau.dev:443/my-project/api", true},
		{"my-project/api", true},
		{"api", true},
		{"docker.io/library/redis", false},
		{"library/redis", true},
		{"ghcr.io/foo/bar", false},
		{"other.registry.io/project/app", false},
	}
	for _, tc := range cases {
		if got := c.Handles(tc.repo); got != tc.want {
			t.Errorf("Handles(%q) = %v, want %v", tc.repo, got, tc.want)
		}
	}
}

func TestBundledResolverPassesThroughForeignImages(t *testing.T) {
	r := BundledResolver{Client: &Client{BaseURL: "https://registry.moureau.dev"}}
	digest, err := r.ResolveTag(context.Background(), "docker.io/library/redis", "alpine")
	if err != nil {
		t.Fatalf("foreign image must not error (no digest resolution): %v", err)
	}
	if digest != "" {
		t.Fatalf("foreign image digest = %q, want empty (deploy by tag)", digest)
	}
}
