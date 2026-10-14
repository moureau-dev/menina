package config

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// EnvironmentPath is the on-disk location of an environment file.
func EnvironmentPath(configDir, project, name string) string {
	return filepath.Join(ProjectDir(configDir, project), environmentsDirName, name+".yml")
}

// LoadEnvironment reads a single environment, returning a friendly error when
// it does not exist.
func LoadEnvironment(configDir, project, name string) (*EnvironmentFile, error) {
	path := EnvironmentPath(configDir, project, name)
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("environment %q not found in project %q", name, project)
		}
		return nil, err
	}
	env, err := readYAML[EnvironmentFile](path)
	if err != nil {
		return nil, err
	}
	if env.Name == "" {
		env.Name = name
	}
	if env.Apps == nil {
		env.Apps = map[string]*AppOverride{}
	}
	return &env, nil
}

// SaveEnvironment writes an environment file, creating the directory with the
// platform's group-writable permissions (umask-independent).
func SaveEnvironment(configDir, project string, env *EnvironmentFile) error {
	dir := filepath.Join(ProjectDir(configDir, project), environmentsDirName)
	if err := os.MkdirAll(dir, 0o770|os.ModeSetgid); err != nil {
		return err
	}
	if err := os.Chmod(dir, 0o770|os.ModeSetgid); err != nil {
		return err
	}
	data, err := yaml.Marshal(env)
	if err != nil {
		return err
	}
	path := EnvironmentPath(configDir, project, env.Name)
	if err := os.WriteFile(path, data, defaultConfigFileMode); err != nil {
		return err
	}
	return os.Chmod(path, defaultConfigFileMode)
}
