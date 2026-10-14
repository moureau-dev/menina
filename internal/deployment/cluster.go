package deployment

import (
	"context"
	"io"
	"time"
)

type LogOptions struct {
	Container string
	Follow    bool
	TailLines *int64
	Since     time.Duration
}

type Workload struct {
	Project       string
	Environment   string
	App           string
	Namespace     string
	Name          string
	Image         string
	Type          string
	Replicas      int
	MaxReplicas   int
	TargetCPU     int
	Port          int
	Command       []string
	Args          []string
	Env           map[string]string
	CPU           string
	Memory        string
	HealthPath    string
	StorageSize   string
	MountPath     string
	PullSecret    string
	PullPolicy    string
	ServiceName   string
	ImageChanged  bool
	ConfigMap     string
	ConfigMount   string
	ContainerPort int
	SecretName    string
	SecretMount   string
}

type WorkloadStatus struct {
	Replicas          int
	ReadyReplicas     int
	AvailableReplicas int
	UpdatedReplicas   int
	Image             string
	Conditions        []string
}

type ServiceRef struct {
	Namespace string
	Name      string
	ClusterIP string
	Ports     []int
	Managed   bool
}

type WorkloadRef struct {
	Namespace   string
	Name        string
	Project     string
	Environment string
	App         string
}

type Cluster interface {
	EnsureNamespace(ctx context.Context, name string) error
	ApplyWorkload(ctx context.Context, w Workload) error
	ApplyService(ctx context.Context, w Workload) error
	DeleteWorkload(ctx context.Context, namespace, name string) error
	DeleteService(ctx context.Context, namespace, name string) error
	DeleteAutoscaler(ctx context.Context, namespace, name string) error
	RestartWorkload(ctx context.Context, namespace, name string) error
	ScaleWorkload(ctx context.Context, namespace, name string, replicas int) error
	ListWorkloads(ctx context.Context) ([]WorkloadRef, error)
	WorkloadStatus(ctx context.Context, namespace, name string) (*WorkloadStatus, error)
	ServiceClusterIP(ctx context.Context, namespace, name string) (string, error)
	ListServices(ctx context.Context) ([]ServiceRef, error)
	PodLogs(ctx context.Context, namespace, name string, opts LogOptions) (io.ReadCloser, error)
	EnsureSecret(ctx context.Context, namespace, name string, data map[string][]byte) error
	EnsureConfigMap(ctx context.Context, namespace, name string, data map[string]string) error
	EnsurePVC(ctx context.Context, namespace, name, size, storageClass string) error
	Ping(ctx context.Context) error
}
