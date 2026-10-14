package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"gopkg.in/yaml.v3"
)

const (
	configFileName        = "config.yml"
	projectsDirName       = "projects"
	projectFileName       = "project.yml"
	appsDirName           = "apps"
	environmentsDirName   = "environments"
	defaultConfigFileMode = 0o660
)

func LoadPlatform(configDir string) (*Config, error) {
	path := filepath.Join(configDir, configFileName)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	cfg := Default()
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return &cfg, nil
}

func SavePlatform(configDir string, cfg *Config) error {
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		return err
	}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	path := filepath.Join(configDir, configFileName)
	if err := os.WriteFile(path, data, defaultConfigFileMode); err != nil {
		return err
	}
	return os.Chmod(path, defaultConfigFileMode)
}

func LoadProject(projectsDir, name string) (*Project, error) {
	dir := filepath.Join(projectsDir, name)
	pf, err := readYAML[ProjectFile](filepath.Join(dir, projectFileName))
	if err != nil {
		return nil, err
	}
	p := &Project{
		Name:        pf.Name,
		Namespace:   pf.Namespace,
		Description: pf.Description,
		Apps:        map[string]*AppFile{},
		Envs:        map[string]*EnvironmentFile{},
	}
	if p.Name == "" {
		p.Name = name
	}
	apps, err := LoadApps(projectsDir, name)
	if err != nil {
		return nil, err
	}
	p.Apps = apps
	envs, order, err := LoadEnvironments(projectsDir, name)
	if err != nil {
		return nil, err
	}
	p.Envs = envs
	p.EnvOrder = order
	return p, nil
}

func LoadApps(projectsDir, project string) (map[string]*AppFile, error) {
	dir := filepath.Join(projectsDir, project, appsDirName)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]*AppFile{}, nil
		}
		return nil, err
	}
	apps := map[string]*AppFile{}
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".yml" {
			continue
		}
		app, err := readYAML[AppFile](filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		if app.Name == "" {
			app.Name = trimYAMLExt(e.Name())
		}
		apps[app.Name] = &app
	}
	return apps, nil
}

func LoadEnvironments(projectsDir, project string) (map[string]*EnvironmentFile, []string, error) {
	dir := filepath.Join(projectsDir, project, environmentsDirName)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]*EnvironmentFile{}, nil, nil
		}
		return nil, nil, err
	}
	envs := map[string]*EnvironmentFile{}
	var order []string
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".yml" {
			continue
		}
		env, err := readYAML[EnvironmentFile](filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, nil, err
		}
		if env.Name == "" {
			env.Name = trimYAMLExt(e.Name())
		}
		if env.Apps == nil {
			env.Apps = map[string]*AppOverride{}
		}
		envs[env.Name] = &env
		order = append(order, env.Name)
	}
	sort.Strings(order)
	return envs, order, nil
}

func ListProjects(projectsDir string) ([]string, error) {
	entries, err := os.ReadDir(projectsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if _, err := os.Stat(filepath.Join(projectsDir, e.Name(), projectFileName)); err != nil {
			continue
		}
		names = append(names, e.Name())
	}
	sort.Strings(names)
	return names, nil
}

func readYAML[T any](path string) (T, error) {
	var out T
	data, err := os.ReadFile(path)
	if err != nil {
		return out, fmt.Errorf("read %s: %w", path, err)
	}
	if err := yaml.Unmarshal(data, &out); err != nil {
		return out, fmt.Errorf("parse %s: %w", path, err)
	}
	return out, nil
}

func TrimYAMLExt(name string) string { return trimYAMLExt(name) }

func trimYAMLExt(name string) string {
	if filepath.Ext(name) == ".yml" {
		return name[:len(name)-4]
	}
	if filepath.Ext(name) == ".yaml" {
		return name[:len(name)-5]
	}
	return name
}
