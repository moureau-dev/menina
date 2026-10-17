// Package manifest parses and renders repository-owned Menina manifests
// (.menina.yml). A manifest is a client-side, portable description of a
// project's apps and workers; it is rendered into the same YAML files that
// `menina app create` writes under /etc/menina so the CLI and the control plane
// keep a single source of truth.
package manifest

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/moureau-dev/menina/internal/config"
)

const (
	// CurrentVersion is the manifest schema version this package understands.
	CurrentVersion = 1

	AccessSingleWriter = "single-writer"
	AccessMultiWriter  = "multi-writer"
)

// File is the top-level repository manifest.
type File struct {
	Version     int                 `yaml:"version,omitempty"`
	Project     string              `yaml:"project"`
	Environment string              `yaml:"environment"`
	Vars        map[string]string   `yaml:"vars,omitempty"`
	Defaults    Defaults            `yaml:"defaults,omitempty"`
	Storages    map[string]Storage  `yaml:"storages,omitempty"`
	Apps        map[string]Workload `yaml:"apps,omitempty"`
	Workers     map[string]Workload `yaml:"workers,omitempty"`

	// BaseDir is the directory manifest paths (context, dockerfile) are
	// resolved against. It is not part of the YAML document.
	BaseDir string `yaml:"-"`
}

type Defaults struct {
	Context string `yaml:"context,omitempty"`
	Tag     string `yaml:"tag,omitempty"`
	// Registry is the image registry host used for built apps when the
	// manifest is rendered outside a host with a config.yml (e.g. CI).
	Registry string `yaml:"registry,omitempty"`
	// BuildArgs are passed to docker build (--build-arg). They are not
	// secrets and are not injected into the running container.
	BuildArgs map[string]string `yaml:"buildArgs,omitempty"`
}

type Storage struct {
	Size   string `yaml:"size"`
	Path   string `yaml:"path"`
	Access string `yaml:"access,omitempty"`
}

// StorageRef references a named storage, or declares an inline one when Use is
// empty.
type StorageRef struct {
	Use  string `yaml:"use,omitempty"`
	Size string `yaml:"size,omitempty"`
	Path string `yaml:"path,omitempty"`
}

type StorageRefs []StorageRef

func (s *StorageRefs) UnmarshalYAML(value *yaml.Node) error {
	switch value.Kind {
	case yaml.ScalarNode:
		var name string
		if err := value.Decode(&name); err != nil {
			return err
		}
		*s = StorageRefs{{Use: name}}
		return nil
	case yaml.MappingNode:
		var ref StorageRef
		if err := value.Decode(&ref); err != nil {
			return err
		}
		*s = StorageRefs{ref}
		return nil
	case yaml.SequenceNode:
		out := make(StorageRefs, 0, len(value.Content))
		for _, item := range value.Content {
			var ref StorageRef
			if err := item.Decode(&ref); err != nil {
				return err
			}
			out = append(out, ref)
		}
		*s = out
		return nil
	default:
		return fmt.Errorf("storage must be a name, a mapping, or a list of them")
	}
}

func (r *StorageRef) UnmarshalYAML(value *yaml.Node) error {
	switch value.Kind {
	case yaml.ScalarNode:
		var name string
		if err := value.Decode(&name); err != nil {
			return err
		}
		*r = StorageRef{Use: name}
		return nil
	case yaml.MappingNode:
		type raw StorageRef
		var v raw
		if err := value.Decode(&v); err != nil {
			return err
		}
		*r = StorageRef(v)
		return nil
	default:
		return fmt.Errorf("storage entry must be a name or a mapping")
	}
}

// ServiceSpec is the app's primary container/Service port.
type ServiceSpec struct {
	Port int `yaml:"port,omitempty"`
}

// Exposure groups how an app is reachable from outside the cluster.
type Exposure struct {
	HTTP  *HTTPExposure    `yaml:"http,omitempty"`
	Ports config.PortsSpec `yaml:"ports,omitempty"`
}

// HTTPExposure is the proxied HTTP edge configuration.
type HTTPExposure struct {
	Hosts        []string             `yaml:"hosts,omitempty"`
	PathPrefix   string               `yaml:"pathPrefix,omitempty"`
	RedirectHTTP bool                 `yaml:"redirectHttp,omitempty"`
	Headers      map[string]string    `yaml:"headers,omitempty"`
	Compression  bool                 `yaml:"compression,omitempty"`
	H2C          bool                 `yaml:"h2c,omitempty"`
	Streaming    bool                 `yaml:"streaming,omitempty"`
	MaxBodyBytes int64                `yaml:"maxBodyBytes,omitempty"`
	Timeouts     config.RouteTimeouts `yaml:"timeouts,omitempty"`
}

// Workload is an app (service) or worker definition.
type Workload struct {
	Dockerfile string               `yaml:"dockerfile,omitempty"`
	Image      string               `yaml:"image,omitempty"`
	Context    string               `yaml:"context,omitempty"`
	From       string               `yaml:"from,omitempty"`
	Type       string               `yaml:"type,omitempty"`
	Service    *ServiceSpec         `yaml:"service,omitempty"`
	Exposure   *Exposure            `yaml:"exposure,omitempty"`
	Port       int                  `yaml:"port,omitempty"` // legacy shorthand for service.port
	Host       string               `yaml:"host,omitempty"` // legacy shorthand for exposure.http.hosts
	Command    []string             `yaml:"command,omitempty"`
	Args       []string             `yaml:"args,omitempty"`
	Env        map[string]string    `yaml:"env,omitempty"`
	Secrets    []string             `yaml:"secrets,omitempty"`
	BuildArgs  map[string]string    `yaml:"buildArgs,omitempty"`
	Storage    StorageRefs          `yaml:"storage,omitempty"`
	Replicas   *config.ReplicasSpec `yaml:"replicas,omitempty"`
	Resources  *config.ResourceSpec `yaml:"resources,omitempty"`
	Health     *config.HealthSpec   `yaml:"health,omitempty"`
}

var (
	varPattern     = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)
	secretNameRe   = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	workloadNameRe = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)
)

// LoadFile reads a manifest from path ("-" for stdin). Relative build paths are
// resolved against the file's directory (or the working directory for stdin).
func LoadFile(path string, stdin io.Reader) (*File, error) {
	return LoadFileWithVars(path, stdin, nil)
}

// LoadFileWithVars is LoadFile with extra variables merged over the manifest's
// vars before interpolation (used by --environment).
func LoadFileWithVars(path string, stdin io.Reader, overrides map[string]string) (*File, error) {
	var (
		data    []byte
		baseDir string
		err     error
	)
	if path == "-" {
		if stdin == nil {
			stdin = os.Stdin
		}
		data, err = io.ReadAll(stdin)
		baseDir = "."
	} else {
		data, err = os.ReadFile(path)
		baseDir = filepath.Dir(path)
	}
	if err != nil {
		return nil, err
	}
	return LoadWithVars(data, baseDir, overrides)
}

// Load parses and validates the manifest document. Unknown fields are rejected
// so typos do not silently disappear.
func Load(data []byte, baseDir string) (*File, error) {
	return LoadWithVars(data, baseDir, nil)
}

// LoadWithVars parses a manifest with extra variable overrides.
func LoadWithVars(data []byte, baseDir string, overrides map[string]string) (*File, error) {
	var head struct {
		Vars map[string]string `yaml:"vars"`
	}
	if err := yaml.Unmarshal(data, &head); err != nil {
		return nil, fmt.Errorf("parse manifest: %w", err)
	}
	vars := map[string]string{}
	for k, v := range head.Vars {
		vars[k] = v
	}
	for k, v := range overrides {
		vars[k] = v
	}

	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parse manifest: %w", err)
	}
	if err := interpolate(&doc, vars); err != nil {
		return nil, err
	}
	normalized, err := yaml.Marshal(&doc)
	if err != nil {
		return nil, err
	}

	dec := yaml.NewDecoder(bytes.NewReader(normalized))
	dec.KnownFields(true)
	var f File
	if err := dec.Decode(&f); err != nil {
		return nil, fmt.Errorf("parse manifest: %w", err)
	}
	if f.Version == 0 {
		f.Version = CurrentVersion
	}
	if f.Version != CurrentVersion {
		return nil, fmt.Errorf("unsupported manifest version %d (this build supports %d)", f.Version, CurrentVersion)
	}
	if f.BaseDir, err = filepath.Abs(baseDir); err != nil {
		return nil, err
	}
	if f.Project == "" {
		return nil, fmt.Errorf("project is required")
	}
	if f.Environment == "" {
		return nil, fmt.Errorf("environment is required")
	}
	if f.Defaults.Context == "" {
		f.Defaults.Context = "."
	}
	return &f, nil
}

func interpolate(node *yaml.Node, vars map[string]string) error {
	switch node.Kind {
	case yaml.DocumentNode, yaml.SequenceNode:
		for _, child := range node.Content {
			if err := interpolate(child, vars); err != nil {
				return err
			}
		}
	case yaml.MappingNode:
		for i := 0; i+1 < len(node.Content); i += 2 {
			if err := interpolate(node.Content[i+1], vars); err != nil {
				return err
			}
		}
	case yaml.ScalarNode:
		var missing string
		node.Value = varPattern.ReplaceAllStringFunc(node.Value, func(match string) string {
			name := match[2 : len(match)-1]
			value, ok := vars[name]
			if !ok && missing == "" {
				missing = name
			}
			return value
		})
		if missing != "" {
			return fmt.Errorf("undefined variable %q (define it under vars)", missing)
		}
	}
	return nil
}

// WithEnvironment rewrites the top-level environment and vars.ENVIRONMENT in a
// raw manifest document. It is used by `menina init --from` so a copied
// manifest keeps its ${ENVIRONMENT} placeholders.
func WithEnvironment(data []byte, environment string) ([]byte, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parse manifest: %w", err)
	}
	if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, fmt.Errorf("manifest must be a mapping")
	}
	root := doc.Content[0]
	var vars *yaml.Node
	for i := 0; i+1 < len(root.Content); i += 2 {
		key := root.Content[i].Value
		value := root.Content[i+1]
		switch key {
		case "environment":
			value.Kind = yaml.ScalarNode
			value.Tag = "!!str"
			value.Value = environment
		case "vars":
			if value.Kind == yaml.MappingNode {
				vars = value
			}
		}
	}
	if vars != nil {
		for i := 0; i+1 < len(vars.Content); i += 2 {
			if vars.Content[i].Value == "ENVIRONMENT" {
				v := vars.Content[i+1]
				v.Kind = yaml.ScalarNode
				v.Tag = "!!str"
				v.Value = environment
			}
		}
	}
	return yaml.Marshal(&doc)
}

// splitImage splits repo[:tag]. Digests are rejected because app.image only
// models a repository plus tag.
func splitImage(ref string) (repository, tag string, err error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", "", fmt.Errorf("image is empty")
	}
	if strings.Contains(ref, "@") {
		return "", "", fmt.Errorf("image digests are not supported in a manifest; use repository:tag")
	}
	lastSlash := strings.LastIndexByte(ref, '/')
	lastColon := strings.LastIndexByte(ref, ':')
	if lastColon > lastSlash {
		return ref[:lastColon], ref[lastColon+1:], nil
	}
	return ref, "", nil
}
