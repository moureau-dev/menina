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
		{"my-project/api", false},
		{"api", false},
		{"redis", false},
		{"docker.io/library/redis", false},
		{"library/redis", false},
		{"traefik/whoami", false},
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
	for _, repo := range []string{"docker.io/library/redis", "redis", "traefik/whoami"} {
		digest, err := r.ResolveTag(context.Background(), repo, "alpine")
		if err != nil {
			t.Fatalf("%s: foreign image must not error (no digest resolution): %v", repo, err)
		}
		if digest != "" {
			t.Fatalf("%s digest = %q, want empty (deploy by tag)", repo, digest)
		}
	}
}
