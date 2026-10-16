package platform

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/moureau-dev/menina/internal/config"
	"github.com/moureau-dev/menina/internal/database"
)

func serviceWithProject(t *testing.T) *Service {
	t.Helper()
	dir := t.TempDir()
	tokenPath := filepath.Join(dir, "notify.token")
	if err := os.WriteFile(tokenPath, []byte("tok"), 0o600); err != nil {
		t.Fatal(err)
	}
	write := func(rel, content string) {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("projects/p/project.yml", "name: p\nnamespace: p\n")
	write("projects/p/apps/api.yml", "name: api\nimage:\n  repository: registry.moureau.dev/p/api\nworkload:\n  type: service\n  port: 8080\n")
	write("projects/p/environments/production.yml", "name: production\napps:\n  api:\n    enabled: true\n")

	cfg := config.Default()
	cfg.Registry.Enabled = false
	cfg.Registry.Notifications.TokenRef = "file:" + tokenPath
	cfg.Platform.DBPath = filepath.Join(dir, "menina.db")
	svc, err := NewService(&cfg, dir, testLogger())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.watcher.Load(); err != nil {
		t.Fatal(err)
	}
	return svc
}

func TestControlDeployReturnsSnapshotVersion(t *testing.T) {
	svc := serviceWithProject(t)
	rr := httptest.NewRecorder()
	body := `{"project":"p","environment":"production","app":"api","confirm":true}`
	svc.controlMux().ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/deploy", strings.NewReader(body)))
	if rr.Code != http.StatusAccepted {
		t.Fatalf("code = %d, body = %s", rr.Code, rr.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if v, ok := resp["snapshotVersion"].(float64); !ok || v < 1 {
		t.Fatalf("snapshotVersion = %v (want >= 1)", resp["snapshotVersion"])
	}
	if resp["jobId"] == "" {
		t.Fatalf("jobId missing: %v", resp)
	}
}

func TestControlJobsListAndCancel(t *testing.T) {
	svc := serviceWithProject(t)
	ctx := context.Background()
	if _, err := svc.db.IngestRegistryEvent(ctx, &database.Event{DedupKey: "e", Repository: "p/api", Tag: "dev"},
		[]database.Job{{DedupKey: "j", Kind: "manual", Project: "p", Environment: "dev", App: "api", Repository: "p/api", Tag: "dev"}}); err != nil {
		t.Fatal(err)
	}
	mux := svc.controlMux()

	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/jobs", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("list code = %d", rr.Code)
	}
	var list struct {
		Jobs []database.Job `json:"jobs"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Jobs) != 1 {
		t.Fatalf("jobs = %+v", list.Jobs)
	}
	id := list.Jobs[0].ID

	rr = httptest.NewRecorder()
	mux.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/jobs/cancel", strings.NewReader(fmt.Sprintf(`{"id":%q}`, id))))
	if rr.Code != http.StatusOK {
		t.Fatalf("cancel code = %d, body = %s", rr.Code, rr.Body.String())
	}
	j, found, err := svc.db.JobByID(ctx, id)
	if err != nil || !found || j.Status != database.JobStatusCancelled {
		t.Fatalf("job = %+v found=%v err=%v", j, found, err)
	}
}
