package config

import "testing"

func TestStateTokenChangesWithConfig(t *testing.T) {
	cfg := Default()
	p := &Project{Name: "p", Apps: map[string]*AppFile{
		"web": {Name: "web", Image: ImageSpec{Repository: "registry.test/p/web", Tag: "production"}},
	}, Envs: map[string]*EnvironmentFile{
		"production": {Name: "production", Apps: map[string]*AppOverride{}},
	}, EnvOrder: []string{"production"}}
	a, err := StateToken(&cfg, []*Project{p}, "p", "production")
	if err != nil {
		t.Fatal(err)
	}
	if a == "" {
		t.Fatal("empty state token")
	}
	p.Apps["web"].Image.Tag = "staging"
	b, err := StateToken(&cfg, []*Project{p}, "p", "production")
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Fatal("state token did not change")
	}
}
