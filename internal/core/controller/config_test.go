package controller

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/moureau-dev/menina/internal/api"
	"github.com/moureau-dev/menina/internal/core/config"
	configplan "github.com/moureau-dev/menina/internal/core/config/plan"
)

func TestConfigApplyAndRollbackAPI(t *testing.T) {
	svc := serviceWithProject(t)
	svc.cfg.Host.ConfigDir = svc.configDir
	data, err := yaml.Marshal(svc.cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(svc.configDir, "config.yml"), data, 0o600); err != nil {
		t.Fatal(err)
	}

	base, err := configplan.CurrentStateToken(svc.configDir, "p", "production")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(svc.configDir, "projects", "p", "apps", "worker.yml")
	plan := configplan.Plan{
		Project: "p", Environment: "production", BaseState: base,
		Writes: []configplan.Write{{Path: path, Content: []byte("name: worker\n"), BeforeHash: configplan.ContentHash(nil), BeforeExisted: false}},
	}
	plan.ID = configplan.Hash(&plan)
	body, _ := json.Marshal(api.ApplyPlanRequest{Plan: plan, Verify: true})
	rr := httptest.NewRecorder()
	svc.controlMux().ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/config/apply", bytes.NewReader(body)))
	if rr.Code != http.StatusOK {
		t.Fatalf("apply status=%d body=%s", rr.Code, rr.Body.String())
	}
	var result configplan.Result
	if err := json.Unmarshal(rr.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Revision == "" || result.StateToken == "" {
		t.Fatalf("result = %+v", result)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("applied file: %v", err)
	}

	rr = httptest.NewRecorder()
	svc.controlMux().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/config/revisions?project=p", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("revisions status=%d body=%s", rr.Code, rr.Body.String())
	}
	var revisions struct {
		Revisions []config.Revision `json:"revisions"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &revisions); err != nil {
		t.Fatal(err)
	}
	if len(revisions.Revisions) == 0 {
		t.Fatal("expected a revision")
	}

	rollback, _ := json.Marshal(map[string]any{"id": revisions.Revisions[0].ID, "confirm": true})
	rr = httptest.NewRecorder()
	svc.controlMux().ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/config/rollback", bytes.NewReader(rollback)))
	if rr.Code != http.StatusOK {
		t.Fatalf("rollback status=%d body=%s", rr.Code, rr.Body.String())
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("rollback should remove applied file, err=%v", err)
	}
}
