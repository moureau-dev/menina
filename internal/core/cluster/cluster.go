package k8s

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
	// SecretEnvName is a Kubernetes Secret whose keys are injected into the
	// container as environment variables via valueFrom.secretKeyRef.
	// SecretEnv maps an environment variable name to the key within that Secret.
	SecretEnvName string
	SecretEnv     map[string]string
	// SecretHash is a fingerprint of the injected secret values. It is
	// annotated on the workload so stale rollouts can be detected after a
	// rotation without storing the values.
	SecretHash string
	Ports      []PortMapping
}

// PortMapping is a raw TCP/UDP port exposed on the node (hostPort), bypassing
// the HTTP proxy for L4 protocols (game servers, TURN, ...).
type PortMapping struct {
	Port     int
	Protocol string // "TCP" or "UDP"
}

type WorkloadStatus struct {
	Replicas          int
	ReadyReplicas     int
	AvailableReplicas int
	UpdatedReplicas   int
	Image             string
	SecretHash        string
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

// JobRequest runs a one-off command in an app image. It is used for migration
// hooks and verification smoke tests. Jobs are timeout-bounded and never
// exposed as long-running workloads.
type JobRequest struct {
	Namespace     string
	Name          string
	Project       string
	Environment   string
	App           string
	Image         string
	Command       []string
	Args          []string
	Env           map[string]string
	SecretEnvName string
	SecretEnv     map[string]string
	Timeout       time.Duration
	PullSecret    string
	PullPolicy    string
}

// JobResult is the outcome of a one-off job.
type JobResult struct {
	Name     string
	Logs     string
	ExitCode int
	Message  string
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
	DeletePVC(ctx context.Context, namespace, name string) error
	CreateVolumeSnapshot(ctx context.Context, namespace, pvcName, snapshotName string) error
	RunJob(ctx context.Context, req JobRequest) (*JobResult, error)
	Ping(ctx context.Context) error
}
