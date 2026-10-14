package deployment

import (
	"context"
	"errors"
	"io"
	"sync"
	"testing"
	"time"
)

type stubCluster struct {
	mu      sync.Mutex
	status  *WorkloadStatus
	err     error
	calls   int
	failN   int
	applied []Workload
}

func (s *stubCluster) statusNow() (*WorkloadStatus, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	if s.err != nil {
		return nil, s.err
	}
	if s.failN > 0 && s.calls <= s.failN {
		return &WorkloadStatus{Replicas: 1, ReadyReplicas: 0, UpdatedReplicas: 0}, nil
	}
	return s.status, nil
}

func (s *stubCluster) EnsureNamespace(context.Context, string) error { return nil }
func (s *stubCluster) ApplyWorkload(_ context.Context, w Workload) error {
	s.mu.Lock()
	s.applied = append(s.applied, w)
	s.mu.Unlock()
	return nil
}
func (s *stubCluster) ApplyService(context.Context, Workload) error { return nil }
func (s *stubCluster) DeleteWorkload(context.Context, string, string) error {
	return nil
}
func (s *stubCluster) DeleteService(context.Context, string, string) error { return nil }
func (s *stubCluster) DeleteAutoscaler(context.Context, string, string) error {
	return nil
}
func (s *stubCluster) RestartWorkload(context.Context, string, string) error { return nil }
func (s *stubCluster) ListWorkloads(context.Context) ([]WorkloadRef, error)  { return nil, nil }
func (s *stubCluster) WorkloadStatus(context.Context, string, string) (*WorkloadStatus, error) {
	return s.statusNow()
}
func (s *stubCluster) ServiceClusterIP(context.Context, string, string) (string, error) {
	return "10.43.0.10", nil
}
func (s *stubCluster) ListServices(context.Context) ([]ServiceRef, error) { return nil, nil }
func (s *stubCluster) PodLogs(context.Context, string, string, LogOptions) (io.ReadCloser, error) {
	return io.NopCloser(nil), nil
}
func (s *stubCluster) EnsureSecret(context.Context, string, string, map[string][]byte) error {
	return nil
}
func (s *stubCluster) EnsureConfigMap(context.Context, string, string, map[string]string) error {
	return nil
}
func (s *stubCluster) EnsurePVC(context.Context, string, string, string, string) error { return nil }
func (s *stubCluster) Ping(context.Context) error                                      { return nil }

func TestWaitForRolloutSucceeds(t *testing.T) {
	c := &stubCluster{status: &WorkloadStatus{Replicas: 2, ReadyReplicas: 2, UpdatedReplicas: 2}}
	st, err := WaitForRollout(context.Background(), c, "ns", "api", RolloutOptions{Timeout: 2 * time.Second, Poll: 5 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	if st.ReadyReplicas != 2 {
		t.Fatalf("status = %+v", st)
	}
}

func TestWaitForRolloutTimesOut(t *testing.T) {
	c := &stubCluster{status: &WorkloadStatus{Replicas: 2, ReadyReplicas: 1, UpdatedReplicas: 1}}
	_, err := WaitForRollout(context.Background(), c, "ns", "api", RolloutOptions{Timeout: 60 * time.Millisecond, Poll: 10 * time.Millisecond})
	if err == nil {
		t.Fatal("expected timeout error")
	}
}

func TestWaitForRolloutPropagatesError(t *testing.T) {
	c := &stubCluster{err: errors.New("api down")}
	if _, err := WaitForRollout(context.Background(), c, "ns", "api", RolloutOptions{Timeout: time.Second}); err == nil {
		t.Fatal("expected error")
	}
}

func TestWaitForRolloutRespectsContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	c := &stubCluster{status: &WorkloadStatus{Replicas: 1, ReadyReplicas: 0}}
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()
	if _, err := WaitForRollout(ctx, c, "ns", "api", RolloutOptions{Timeout: time.Minute, Poll: 5 * time.Millisecond}); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}
