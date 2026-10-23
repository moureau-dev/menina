package controller

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"time"

	"github.com/moureau-dev/menina/internal/core/cluster"
)

type deferredCluster struct {
	kubeconfig string
	log        *slog.Logger
	mu         sync.Mutex
	inner      k8s.Cluster
	lastTry    time.Time
	lastErr    error
}

func newDeferredCluster(kubeconfig string, log *slog.Logger) *deferredCluster {
	return &deferredCluster{kubeconfig: kubeconfig, log: log}
}

func (d *deferredCluster) get() (k8s.Cluster, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.inner != nil {
		return d.inner, nil
	}
	if time.Since(d.lastTry) < 5*time.Second {
		return nil, d.lastErr
	}
	d.lastTry = time.Now()
	c, err := k8s.NewKubernetesCluster(d.kubeconfig)
	if err != nil {
		d.lastErr = err
		return nil, err
	}
	d.inner = c
	d.lastErr = nil
	if d.log != nil {
		d.log.Info("connected to Kubernetes API")
	}
	return c, nil
}

func (d *deferredCluster) EnsureNamespace(ctx context.Context, name string) error {
	c, err := d.get()
	if err != nil {
		return err
	}
	return c.EnsureNamespace(ctx, name)
}

func (d *deferredCluster) ApplyWorkload(ctx context.Context, w k8s.Workload) error {
	c, err := d.get()
	if err != nil {
		return err
	}
	return c.ApplyWorkload(ctx, w)
}

func (d *deferredCluster) ApplyService(ctx context.Context, w k8s.Workload) error {
	c, err := d.get()
	if err != nil {
		return err
	}
	return c.ApplyService(ctx, w)
}

func (d *deferredCluster) DeleteWorkload(ctx context.Context, namespace, name string) error {
	c, err := d.get()
	if err != nil {
		return err
	}
	return c.DeleteWorkload(ctx, namespace, name)
}

func (d *deferredCluster) DeleteService(ctx context.Context, namespace, name string) error {
	c, err := d.get()
	if err != nil {
		return err
	}
	return c.DeleteService(ctx, namespace, name)
}

func (d *deferredCluster) DeleteAutoscaler(ctx context.Context, namespace, name string) error {
	c, err := d.get()
	if err != nil {
		return err
	}
	return c.DeleteAutoscaler(ctx, namespace, name)
}

func (d *deferredCluster) RestartWorkload(ctx context.Context, namespace, name string) error {
	c, err := d.get()
	if err != nil {
		return err
	}
	return c.RestartWorkload(ctx, namespace, name)
}

func (d *deferredCluster) ScaleWorkload(ctx context.Context, namespace, name string, replicas int) error {
	c, err := d.get()
	if err != nil {
		return err
	}
	return c.ScaleWorkload(ctx, namespace, name, replicas)
}

func (d *deferredCluster) ListWorkloads(ctx context.Context) ([]k8s.WorkloadRef, error) {
	c, err := d.get()
	if err != nil {
		return nil, err
	}
	return c.ListWorkloads(ctx)
}

func (d *deferredCluster) WorkloadStatus(ctx context.Context, namespace, name string) (*k8s.WorkloadStatus, error) {
	c, err := d.get()
	if err != nil {
		return nil, err
	}
	return c.WorkloadStatus(ctx, namespace, name)
}

func (d *deferredCluster) ServiceClusterIP(ctx context.Context, namespace, name string) (string, error) {
	c, err := d.get()
	if err != nil {
		return "", err
	}
	return c.ServiceClusterIP(ctx, namespace, name)
}

func (d *deferredCluster) ListServices(ctx context.Context) ([]k8s.ServiceRef, error) {
	c, err := d.get()
	if err != nil {
		return nil, err
	}
	return c.ListServices(ctx)
}

func (d *deferredCluster) PodLogs(ctx context.Context, namespace, name string, opts k8s.LogOptions) (io.ReadCloser, error) {
	c, err := d.get()
	if err != nil {
		return nil, err
	}
	return c.PodLogs(ctx, namespace, name, opts)
}

func (d *deferredCluster) EnsureSecret(ctx context.Context, namespace, name string, data map[string][]byte) error {
	c, err := d.get()
	if err != nil {
		return err
	}
	return c.EnsureSecret(ctx, namespace, name, data)
}

func (d *deferredCluster) EnsurePVC(ctx context.Context, namespace, name, size, storageClass string) error {
	c, err := d.get()
	if err != nil {
		return err
	}
	return c.EnsurePVC(ctx, namespace, name, size, storageClass)
}

func (d *deferredCluster) DeletePVC(ctx context.Context, namespace, name string) error {
	c, err := d.get()
	if err != nil {
		return err
	}
	return c.DeletePVC(ctx, namespace, name)
}

func (d *deferredCluster) EnsureConfigMap(ctx context.Context, namespace, name string, data map[string]string) error {
	c, err := d.get()
	if err != nil {
		return err
	}
	return c.EnsureConfigMap(ctx, namespace, name, data)
}

func (d *deferredCluster) Ping(ctx context.Context) error {
	c, err := d.get()
	if err != nil {
		return fmt.Errorf("kubernetes unavailable: %w", err)
	}
	return c.Ping(ctx)
}

func (d *deferredCluster) RunJob(ctx context.Context, req k8s.JobRequest) (*k8s.JobResult, error) {
	c, err := d.get()
	if err != nil {
		return nil, fmt.Errorf("kubernetes unavailable: %w", err)
	}
	return c.RunJob(ctx, req)
}

func (d *deferredCluster) CreateVolumeSnapshot(ctx context.Context, namespace, pvcName, snapshotName string) error {
	c, err := d.get()
	if err != nil {
		return fmt.Errorf("kubernetes unavailable: %w", err)
	}
	return c.CreateVolumeSnapshot(ctx, namespace, pvcName, snapshotName)
}
