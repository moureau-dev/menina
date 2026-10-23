package worker

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/moureau-dev/menina/internal/config"
	"github.com/moureau-dev/menina/internal/database"
	"github.com/moureau-dev/menina/internal/deployment"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func testDB(t *testing.T) *database.DB {
	t.Helper()
	db, err := database.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	return db
}

type fakeCluster struct {
	status     *deployment.WorkloadStatus
	clusterIP  string
	applied    []deployment.Workload
	services   []deployment.ServiceRef
	workloads  []deployment.WorkloadRef
	deleted    []string
	deletedSvc []string
	scaled     map[string]int
}

func (f *fakeCluster) EnsureNamespace(context.Context, string) error { return nil }
func (f *fakeCluster) ApplyWorkload(_ context.Context, w deployment.Workload) error {
	f.applied = append(f.applied, w)
	return nil
}
func (f *fakeCluster) ApplyService(_ context.Context, w deployment.Workload) error {
	f.applied = append(f.applied, w)
	return nil
}
func (f *fakeCluster) DeleteWorkload(_ context.Context, namespace, name string) error {
	f.deleted = append(f.deleted, namespace+"/"+name)
	return nil
}
func (f *fakeCluster) DeleteService(_ context.Context, namespace, name string) error {
	f.deletedSvc = append(f.deletedSvc, namespace+"/"+name)
	return nil
}
func (f *fakeCluster) DeleteAutoscaler(context.Context, string, string) error { return nil }
func (f *fakeCluster) RestartWorkload(context.Context, string, string) error  { return nil }
func (f *fakeCluster) ScaleWorkload(_ context.Context, namespace, name string, replicas int) error {
	if f.scaled == nil {
		f.scaled = map[string]int{}
	}
	f.scaled[namespace+"/"+name] = replicas
	return nil
}
func (f *fakeCluster) ListWorkloads(context.Context) ([]deployment.WorkloadRef, error) {
	return f.workloads, nil
}
func (f *fakeCluster) WorkloadStatus(context.Context, string, string) (*deployment.WorkloadStatus, error) {
	if f.status == nil {
		return &deployment.WorkloadStatus{}, nil
	}
	return f.status, nil
}
func (f *fakeCluster) ServiceClusterIP(context.Context, string, string) (string, error) {
	return f.clusterIP, nil
}
func (f *fakeCluster) ListServices(context.Context) ([]deployment.ServiceRef, error) {
	return f.services, nil
}
func (f *fakeCluster) PodLogs(context.Context, string, string, deployment.LogOptions) (io.ReadCloser, error) {
	return io.NopCloser(nil), nil
}
func (f *fakeCluster) EnsureSecret(context.Context, string, string, map[string][]byte) error {
	return nil
}
func (f *fakeCluster) EnsureConfigMap(context.Context, string, string, map[string]string) error {
	return nil
}
func (f *fakeCluster) EnsurePVC(context.Context, string, string, string, string) error    { return nil }
func (f *fakeCluster) DeletePVC(context.Context, string, string) error                    { return nil }
func (f *fakeCluster) Ping(context.Context) error                                         { return nil }
func (f *fakeCluster) CreateVolumeSnapshot(context.Context, string, string, string) error { return nil }
func (f *fakeCluster) RunJob(context.Context, deployment.JobRequest) (*deployment.JobResult, error) {
	panic("unexpected RunJob call")
}

type stubResolver struct{ digest string }

func (s stubResolver) ResolveTag(context.Context, string, string) (string, error) {
	return s.digest, nil
}

func testResolved() *config.ResolvedConfig {
	return &config.ResolvedConfig{
		Version: 1,
		Projects: map[string]*config.ResolvedProject{
			"my-platform": {
				Name: "my-platform", Namespace: "my-platform",
				Environments: map[string]*config.ResolvedEnvironment{
					"production": {
						Project: "my-platform", Name: "production",
						Apps: map[string]*config.ResolvedApp{
							"api": {
								Project: "my-platform", Environment: "production", Name: "api", Namespace: "my-platform",
								Enabled: true, Repository: "registry.moureau.dev/my-platform/api", Tag: "production",
								Workload: config.WorkloadSpec{Type: config.WorkloadService, Port: 8080},
								Replicas: config.ReplicasSpec{Min: 2, Max: 2},
								Exposure: config.ExposureSpec{Enabled: true, Hosts: []string{"api.hello.com"}, PathPrefix: "/"},
							},
						},
					},
				},
			},
		},
	}
}
