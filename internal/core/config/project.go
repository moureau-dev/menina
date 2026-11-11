package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"time"

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

// DefaultStorageName is the name assigned to a single unnamed storage. It keeps
// the historical PersistentVolumeClaim name `<app>-data` for existing apps.
const DefaultStorageName = "data"

type StorageSpec struct {
	// Name identifies the volume within the app. The PVC is named
	// `<app>-<name>`.
	Name      string `yaml:"name,omitempty"`
	Enabled   bool   `yaml:"enabled"`
	Size      string `yaml:"size"`
	MountPath string `yaml:"mountPath"`
	// SnapshotBeforeDeploy asks the cluster's CSI driver for a VolumeSnapshot
	// before a release changes the workload. It is opt-in and fails the release
	// explicitly when the storage class has no snapshot support.
	SnapshotBeforeDeploy bool `yaml:"snapshotBeforeDeploy,omitempty"`
}

// Storages is a workload's list of persistent volumes. It accepts either a
// single storage object (the legacy shape) or a list of objects.
type Storages []StorageSpec

// Active returns the enabled storages.
func (s Storages) Active() Storages {
	if len(s) == 0 {
		return nil
	}
	out := make(Storages, 0, len(s))
	for _, storage := range s {
		if storage.Enabled {
			out = append(out, storage)
		}
	}
	return out
}

// StoragePVCName returns the PersistentVolumeClaim name for a workload volume.
func StoragePVCName(app, storage string) string {
	if storage == "" {
		storage = DefaultStorageName
	}
	return app + "-" + storage
}

func (s *Storages) UnmarshalYAML(value *yaml.Node) error {
	switch value.Kind {
	case yaml.MappingNode:
		storage, err := decodeStorageYAML(value)
		if err != nil {
			return err
		}
		if storage.Name == "" {
			storage.Name = DefaultStorageName
		}
		*s = Storages{storage}
		return nil
	case yaml.SequenceNode:
		out := make(Storages, 0, len(value.Content))
		for i, item := range value.Content {
			if item.Kind != yaml.MappingNode {
				return fmt.Errorf("storage entry %d must be a mapping", i+1)
			}
			storage, err := decodeStorageYAML(item)
			if err != nil {
				return err
			}
			if storage.Name == "" {
				if len(value.Content) > 1 {
					return fmt.Errorf("storage entry %d needs a name", i+1)
				}
				storage.Name = DefaultStorageName
			}
			out = append(out, storage)
		}
		*s = out
		return nil
	case yaml.ScalarNode:
		var raw any
		if err := value.Decode(&raw); err != nil {
			return err
		}
		if raw == nil {
			*s = nil
			return nil
		}
		return fmt.Errorf("storage must be a mapping or a list of mappings")
	default:
		return fmt.Errorf("storage must be a mapping or a list of mappings")
	}
}

func decodeStorageYAML(node *yaml.Node) (StorageSpec, error) {
	type raw StorageSpec
	var spec raw
	if err := node.Decode(&spec); err != nil {
		return StorageSpec{}, err
	}
	out := StorageSpec(spec)
	if !yamlMappingHasKey(node, "enabled") {
		out.Enabled = true
	}
	return out, nil
}

func yamlMappingHasKey(node *yaml.Node, key string) bool {
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			return true
		}
	}
	return false
}

func (s *Storages) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || string(trimmed) == "null" {
		*s = nil
		return nil
	}
	if trimmed[0] == '[' {
		var out []StorageSpec
		if err := json.Unmarshal(trimmed, &out); err != nil {
			return err
		}
		for i := range out {
			if out[i].Name == "" {
				if len(out) > 1 {
					return fmt.Errorf("storage entry %d needs a name", i+1)
				}
				out[i].Name = DefaultStorageName
			}
		}
		*s = out
		return nil
	}
	var spec StorageSpec
	if err := json.Unmarshal(trimmed, &spec); err != nil {
		return err
	}
	if spec.Name == "" {
		spec.Name = DefaultStorageName
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &fields); err == nil {
		if _, ok := fields["Enabled"]; !ok {
			if _, ok := fields["enabled"]; !ok {
				spec.Enabled = true
			}
		}
	}
	*s = Storages{spec}
	return nil
}

// MarshalYAML keeps the legacy single-storage shape when the app has one volume
// named `data`, so existing configuration stays readable.
func (s Storages) MarshalYAML() (any, error) {
	if len(s) == 0 {
		return nil, nil
	}
	type raw StorageSpec
	if len(s) == 1 && (s[0].Name == "" || s[0].Name == DefaultStorageName) {
		storage := raw(s[0])
		storage.Name = ""
		return storage, nil
	}
	out := make([]raw, len(s))
	for i, storage := range s {
		out[i] = raw(storage)
	}
	return out, nil
}

type HealthSpec struct {
	Readiness string `yaml:"readiness"`
	Liveness  string `yaml:"liveness"`
}

// VerificationSpec declares checks that run after a successful rollout.
// Verification is not reachability: it asserts the app actually works.
type VerificationSpec struct {
	Enabled       *bool             `yaml:"enabled,omitempty"`
	Path          string            `yaml:"path,omitempty"`
	Method        string            `yaml:"method,omitempty"`
	ExpectStatus  []int             `yaml:"expectStatus,omitempty"`
	ExpectBody    string            `yaml:"expectBody,omitempty"`
	ExpectHeader  map[string]string `yaml:"expectHeader,omitempty"`
	Timeout       time.Duration     `yaml:"timeout,omitempty"`
	SmokeTest     *HookSpec         `yaml:"smokeTest,omitempty"`
	FailurePolicy string            `yaml:"failurePolicy,omitempty"`
}

const (
	// VerificationFailureRecover hands the failure to the app's recovery policy.
	VerificationFailureRecover = "recover"
	// VerificationFailureKeep records the failed release and does not restore.
	VerificationFailureKeep = "keep"
)

// HookSpec is a one-off command run in the app's image. Hooks are opt-in,
// permissioned, timeout-bounded and never assumed reversible.
type HookSpec struct {
	Command []string      `yaml:"command"`
	Timeout time.Duration `yaml:"timeout,omitempty"`
}

// HooksSpec declares app lifecycle hooks. A migration hook is not reversed by
// an image rollback.
type HooksSpec struct {
	PreDeploy  *HookSpec `yaml:"preDeploy,omitempty"`
	Migrate    *HookSpec `yaml:"migrate,omitempty"`
	PostDeploy *HookSpec `yaml:"postDeploy,omitempty"`
}

// RecoverySpec is the explicit per-app recovery policy after qualifying
// failures. It never implies a data rollback.
type RecoverySpec struct {
	Policy   string   `yaml:"policy,omitempty"`
	Triggers []string `yaml:"triggers,omitempty"`
}

type AppFile struct {
	Name         string            `yaml:"name"`
	Source       SourceSpec        `yaml:"source,omitempty"`
	Image        ImageSpec         `yaml:"image"`
	Workload     WorkloadSpec      `yaml:"workload"`
	Resources    ResourceSpec      `yaml:"resources"`
	Exposure     ExposureSpec      `yaml:"exposure"`
	Storage      Storages          `yaml:"storage"`
	Replicas     *ReplicasSpec     `yaml:"replicas"`
	Health       *HealthSpec       `yaml:"health"`
	Verification *VerificationSpec `yaml:"verification,omitempty"`
	Hooks        *HooksSpec        `yaml:"hooks,omitempty"`
	Recovery     *RecoverySpec     `yaml:"recovery,omitempty"`
	DependsOn    []string          `yaml:"dependsOn,omitempty"`
	Env          map[string]string `yaml:"env"`
	// Secrets maps an environment variable name to a secret reference
	// (file:..., env:..., literal:...). Only the reference is stored in
	// configuration; the resolved value never enters the config tree.
	Secrets map[string]string `yaml:"secrets,omitempty"`
}

type AppOverride struct {
	Enabled      *bool             `yaml:"enabled"`
	Image        *ImageSpec        `yaml:"image"`
	Workload     *WorkloadSpec     `yaml:"workload"`
	Resources    *ResourceSpec     `yaml:"resources"`
	Exposure     *ExposureSpec     `yaml:"exposure"`
	Storage      Storages          `yaml:"storage"`
	Replicas     *ReplicasSpec     `yaml:"replicas"`
	Health       *HealthSpec       `yaml:"health"`
	Verification *VerificationSpec `yaml:"verification,omitempty"`
	Hooks        *HooksSpec        `yaml:"hooks,omitempty"`
	Recovery     *RecoverySpec     `yaml:"recovery,omitempty"`
	DependsOn    []string          `yaml:"dependsOn,omitempty"`
	Env          map[string]string `yaml:"env"`
	Secrets      map[string]string `yaml:"secrets,omitempty"`
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
	Storage       Storages
	Replicas      ReplicasSpec
	Health        *HealthSpec
	Verification  *VerificationSpec
	Hooks         *HooksSpec
	Recovery      *RecoverySpec
	DependsOn     []string
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
