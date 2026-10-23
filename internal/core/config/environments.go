package config

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// EnvironmentPath is the on-disk location of an environment definition file.
func EnvironmentPath(configDir, project, name string) string {
	return filepath.Join(ProjectDir(configDir, project), environmentsDirName, name+".yml")
}

// EnvironmentOverridesDir is the directory holding one override file per app
// (environments/<env>/<app>.yml). Keeping each app's override in its own file
// lets several repositories contribute to the same environment without ever
// writing the same file, which a single shared environments/<env>.yml cannot do.
func EnvironmentOverridesDir(configDir, project, name string) string {
	return filepath.Join(ProjectDir(configDir, project), environmentsDirName, name)
}

// AppOverridePath is the on-disk location of a single app's environment override.
func AppOverridePath(configDir, project, environment, app string) string {
	return filepath.Join(EnvironmentOverridesDir(configDir, project, environment), app+".yml")
}

// LoadEnvironment reads a single environment, merging its definition file
// (optional) with any per-app override files in its directory. It returns a
// friendly error when neither exists.
func LoadEnvironment(configDir, project, name string) (*EnvironmentFile, error) {
	path := EnvironmentPath(configDir, project, name)
	var env *EnvironmentFile
	if _, err := os.Stat(path); err == nil {
		loaded, err := readYAML[EnvironmentFile](path)
		if err != nil {
			return nil, err
		}
		env = &loaded
	} else if !os.IsNotExist(err) {
		return nil, err
	}

	overridesDir := EnvironmentOverridesDir(configDir, project, name)
	if env == nil {
		if !dirExists(overridesDir) {
			return nil, fmt.Errorf("environment %q not found in project %q", name, project)
		}
		env = &EnvironmentFile{}
	}
	if env.Name == "" {
		env.Name = name
	}
	if env.Apps == nil {
		env.Apps = map[string]*AppOverride{}
	}
	overrides, err := loadAppOverrides(overridesDir)
	if err != nil {
		return nil, err
	}
	for app, ov := range overrides {
		env.Apps[app] = ov
	}
	return env, nil
}

// loadAppOverrides reads the per-app override files from an environment
// directory. A missing directory yields no overrides.
func loadAppOverrides(dir string) (map[string]*AppOverride, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	out := map[string]*AppOverride{}
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".yml" {
			continue
		}
		ov, err := readYAML[AppOverride](filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		out[trimYAMLExt(e.Name())] = &ov
	}
	return out, nil
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// SaveAppOverride writes a single app's environment override to its own file,
// leaving the environment definition and every other app's override untouched.
func SaveAppOverride(configDir, project, environment, app string, ov *AppOverride) error {
	if ov == nil {
		ov = &AppOverride{}
	}
	dir := EnvironmentOverridesDir(configDir, project, environment)
	if err := os.MkdirAll(dir, 0o770|os.ModeSetgid); err != nil {
		return err
	}
	if err := os.Chmod(dir, 0o770|os.ModeSetgid); err != nil {
		return err
	}
	data, err := yaml.Marshal(ov)
	if err != nil {
		return err
	}
	path := AppOverridePath(configDir, project, environment, app)
	if err := os.WriteFile(path, data, defaultConfigFileMode); err != nil {
		return err
	}
	return os.Chmod(path, defaultConfigFileMode)
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
