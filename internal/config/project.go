package config

type ProjectFile struct {
	Name        string `yaml:"name"`
	Namespace   string `yaml:"namespace"`
	Description string `yaml:"description"`
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
	Image     ImageSpec         `yaml:"image"`
	Workload  WorkloadSpec      `yaml:"workload"`
	Resources ResourceSpec      `yaml:"resources"`
	Exposure  ExposureSpec      `yaml:"exposure"`
	Storage   *StorageSpec      `yaml:"storage"`
	Replicas  *int              `yaml:"replicas"`
	Health    *HealthSpec       `yaml:"health"`
	Env       map[string]string `yaml:"env"`
}

type AppOverride struct {
	Enabled   *bool             `yaml:"enabled"`
	Image     *ImageSpec        `yaml:"image"`
	Workload  *WorkloadSpec     `yaml:"workload"`
	Resources *ResourceSpec     `yaml:"resources"`
	Exposure  *ExposureSpec     `yaml:"exposure"`
	Storage   *StorageSpec      `yaml:"storage"`
	Replicas  *int              `yaml:"replicas"`
	Health    *HealthSpec       `yaml:"health"`
	Env       map[string]string `yaml:"env"`
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
	Apps        map[string]*AppFile
	EnvOrder    []string
	Envs        map[string]*EnvironmentFile
}

type ResolvedProject struct {
	Name         string
	Namespace    string
	Description  string
	Environments map[string]*ResolvedEnvironment
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
	Repository    string
	Tag           string
	PullPolicy    string
	DesiredDigest string
	Workload      WorkloadSpec
	Resources     ResourceSpec
	Exposure      ExposureSpec
	Storage       *StorageSpec
	Replicas      int
	Health        *HealthSpec
	Env           map[string]string
}

func (a *ResolvedApp) Image() string {
	if a.Tag == "" {
		return a.Repository
	}
	return a.Repository + ":" + a.Tag
}
