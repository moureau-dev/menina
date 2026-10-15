package config

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

// ReplicasSpec accepts either a fixed count (`replicas: 3`) or a min/max range
// with an optional CPU-utilization target (`replicas: {min: 2, max: 10, targetCPU: 70}`),
// which Menina realizes as a HorizontalPodAutoscaler.
type ReplicasSpec struct {
	Min       int `yaml:"min"`
	Max       int `yaml:"max"`
	TargetCPU int `yaml:"targetCPU,omitempty"`
}

func (r *ReplicasSpec) UnmarshalYAML(value *yaml.Node) error {
	switch value.Kind {
	case yaml.ScalarNode:
		var n int
		if err := value.Decode(&n); err != nil {
			return err
		}
		r.Min, r.Max = n, n
		return nil
	case yaml.MappingNode:
		type raw ReplicasSpec
		var v raw
		if err := value.Decode(&v); err != nil {
			return err
		}
		*r = ReplicasSpec(v)
		return nil
	default:
		return fmt.Errorf("replicas must be an integer or a mapping with min/max")
	}
}

// Autoscaling reports whether the spec requests a range (max above min).
func (r ReplicasSpec) Autoscaling() bool { return r.Max > r.Min }

// MarshalYAML emits a scalar for a fixed, non-autoscaling count so the file
// stays terse (`replicas: 3`).
func (r ReplicasSpec) MarshalYAML() (any, error) {
	if !r.Autoscaling() && r.TargetCPU == 0 {
		return r.Min, nil
	}
	type raw ReplicasSpec
	return raw(r), nil
}

type ProjectFile struct {
	Name        string          `yaml:"name"`
	Namespace   string          `yaml:"namespace"`
	Description string          `yaml:"description"`
	Webhooks    ProjectWebhooks `yaml:"webhooks"`
}

// ProjectWebhooks lets a project override the platform-level deploy webhook.
type ProjectWebhooks struct {
	Deploy *DeployWebhook `yaml:"deploy"`
}

// SourceSpec records which repository owns an app definition. A blank Repo
// means the app is locally/human-owned (it is never claimed or pruned by a
// deploying repository). It lets multiple repositories contribute apps to the
// same project without overwriting each other.
type SourceSpec struct {
	Repo     string `yaml:"repo,omitempty"`
	Revision string `yaml:"revision,omitempty"`
}

// ConflictsWith reports whether two sources are distinct repositories. A blank
// source on either side never conflicts (local ownership is compatible).
func (s SourceSpec) ConflictsWith(other SourceSpec) bool {
	return s.Repo != "" && other.Repo != "" && s.Repo != other.Repo
}

type ImageSpec struct {
	Repository string `yaml:"repository"`
	Tag        string `yaml:"tag"`
	PullPolicy string `yaml:"pullPolicy"`
}

type WorkloadSpec struct {
	Type    string   `yaml:"type"`
	Port    int      `yaml:"port"`
	Command []string `yaml:"command"`
	Args    []string `yaml:"args"`
}

type ResourceSpec struct {
	CPU    string `yaml:"cpu"`
	Memory string `yaml:"memory"`
}

type RouteTimeouts struct {
	Read  string `yaml:"read"`
	Write string `yaml:"write"`
	Idle  string `yaml:"idle"`
}

type ExposureSpec struct {
	Enabled      bool              `yaml:"enabled"`
	Hosts        []string          `yaml:"hosts"`
	PathPrefix   string            `yaml:"pathPrefix"`
	RedirectHTTP bool              `yaml:"redirectHttp"`
	Headers      map[string]string `yaml:"headers"`
	Compression  bool              `yaml:"compression"`
	H2C          bool              `yaml:"h2c"`
	Streaming    bool              `yaml:"streaming"`
	MaxBodyBytes int64             `yaml:"maxBodyBytes"`
	Timeouts     RouteTimeouts     `yaml:"timeouts"`
	Ports        PortsSpec         `yaml:"ports,omitempty"`
}

type StorageSpec struct {
	Enabled   bool   `yaml:"enabled"`
	Size      string `yaml:"size"`
	MountPath string `yaml:"mountPath"`
}

type HealthSpec struct {
	Readiness string `yaml:"readiness"`
	Liveness  string `yaml:"liveness"`
}

type AppFile struct {
	Name      string            `yaml:"name"`
	Source    SourceSpec        `yaml:"source,omitempty"`
	Image     ImageSpec         `yaml:"image"`
	Workload  WorkloadSpec      `yaml:"workload"`
	Resources ResourceSpec      `yaml:"resources"`
	Exposure  ExposureSpec      `yaml:"exposure"`
	Storage   *StorageSpec      `yaml:"storage"`
	Replicas  *ReplicasSpec     `yaml:"replicas"`
	Health    *HealthSpec       `yaml:"health"`
	Env       map[string]string `yaml:"env"`
	// Secrets maps an environment variable name to a secret reference
	// (file:..., env:..., literal:...). Only the reference is stored in
	// configuration; the resolved value never enters the config tree.
	Secrets map[string]string `yaml:"secrets,omitempty"`
}

type AppOverride struct {
	Enabled   *bool             `yaml:"enabled"`
	Image     *ImageSpec        `yaml:"image"`
	Workload  *WorkloadSpec     `yaml:"workload"`
	Resources *ResourceSpec     `yaml:"resources"`
	Exposure  *ExposureSpec     `yaml:"exposure"`
	Storage   *StorageSpec      `yaml:"storage"`
	Replicas  *ReplicasSpec     `yaml:"replicas"`
	Health    *HealthSpec       `yaml:"health"`
	Env       map[string]string `yaml:"env"`
	Secrets   map[string]string `yaml:"secrets,omitempty"`
}

type EnvironmentFile struct {
	Name      string                  `yaml:"name"`
	Namespace string                  `yaml:"namespace"`
	Apps      map[string]*AppOverride `yaml:"apps"`
}

type Project struct {
	Name        string
	Namespace   string
	Description string
	Webhooks    ProjectWebhooks
	Apps        map[string]*AppFile
	EnvOrder    []string
	Envs        map[string]*EnvironmentFile
}

type ResolvedProject struct {
	Name          string
	Namespace     string
	Description   string
	DeployWebhook *DeployWebhook
	Environments  map[string]*ResolvedEnvironment
}

type ResolvedEnvironment struct {
	Project string
	Name    string
	Apps    map[string]*ResolvedApp
}

type ResolvedApp struct {
	Project       string
	Environment   string
	Name          string
	Namespace     string
	Enabled       bool
	Source        SourceSpec
	Repository    string
	Tag           string
	PullPolicy    string
	DesiredDigest string
	Workload      WorkloadSpec
	Resources     ResourceSpec
	Exposure      ExposureSpec
	Storage       *StorageSpec
	Replicas      ReplicasSpec
	Health        *HealthSpec
	Env           map[string]string
	// Secrets holds secret references (never resolved values).
	Secrets map[string]string
	Webhook *DeployWebhook
}

func (a *ResolvedApp) Image() string {
	if a.Tag == "" {
		return a.Repository
	}
	return a.Repository + ":" + a.Tag
}
