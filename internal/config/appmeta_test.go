package config

import "testing"

func TestResolveCarriesAppSource(t *testing.T) {
	p := testProject()
	p.Apps["api"].Source = SourceSpec{Repo: "github.com/acme/api", Revision: "9f3c2a1"}
	rc, err := NewResolver(testConfig()).Resolve([]*Project{p}, 1)
	if err != nil {
		t.Fatal(err)
	}
	app, err := rc.FindApp("my-platform", "production", "api")
	if err != nil {
		t.Fatal(err)
	}
	if app.Source.Repo != "github.com/acme/api" || app.Source.Revision != "9f3c2a1" {
		t.Fatalf("source = %+v", app.Source)
	}
}

func TestSourceConflictsWith(t *testing.T) {
	cases := []struct {
		name string
		a, b SourceSpec
		want bool
	}{
		{"different repos", SourceSpec{Repo: "a"}, SourceSpec{Repo: "b"}, true},
		{"same repo", SourceSpec{Repo: "a"}, SourceSpec{Repo: "a"}, false},
		{"local vs remote", SourceSpec{Repo: "a"}, SourceSpec{}, false},
		{"remote vs local", SourceSpec{}, SourceSpec{Repo: "b"}, false},
		{"both local", SourceSpec{}, SourceSpec{}, false},
	}
	for _, c := range cases {
		if got := c.a.ConflictsWith(c.b); got != c.want {
			t.Errorf("%s: %+v.ConflictsWith(%+v) = %v, want %v", c.name, c.a, c.b, got, c.want)
		}
	}
}

func TestResolveMergesAppSecrets(t *testing.T) {
	p := testProject()
	p.Apps["api"].Secrets = map[string]string{
		"DATABASE_URL": "file:/run/db",
		"CACHE_URL":    "literal:redis://a",
	}
	p.Envs["production"].Apps["api"].Secrets = map[string]string{
		"CACHE_URL": "literal:redis://b",
	}
	rc, err := NewResolver(testConfig()).Resolve([]*Project{p}, 1)
	if err != nil {
		t.Fatal(err)
	}
	app, err := rc.FindApp("my-platform", "production", "api")
	if err != nil {
		t.Fatal(err)
	}
	if app.Secrets["DATABASE_URL"] != "file:/run/db" {
		t.Fatalf("baseline secret lost: %+v", app.Secrets)
	}
	if app.Secrets["CACHE_URL"] != "literal:redis://b" {
		t.Fatalf("override secret not applied: %+v", app.Secrets)
	}
}
