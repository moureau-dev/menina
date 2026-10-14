package config

import (
	"os"
	"path/filepath"
)

// trashDirName holds soft-deleted definitions. It is a directory, so the
// normal loaders (which only read *.yml files) ignore it.
const trashDirName = ".trash"

// Soft-delete kinds.
const (
	SoftDeleteProject     = "project"
	SoftDeleteEnvironment = "environment"
	SoftDeleteApp         = "app"
)

// SoftDelete describes a definition that was moved aside by a soft delete.
type SoftDelete struct {
	Kind        string
	Project     string
	Environment string
	App         string
	Path        string
}

// SoftDeleteAppPath is where a soft-deleted app definition is kept.
func SoftDeleteAppPath(configDir, project, app string) string {
	return filepath.Join(ProjectDir(configDir, project), appsDirName, trashDirName, app+".yml")
}

// SoftDeleteEnvironmentPath is where a soft-deleted environment definition is kept.
func SoftDeleteEnvironmentPath(configDir, project, environment string) string {
	return filepath.Join(ProjectDir(configDir, project), environmentsDirName, trashDirName, environment+".yml")
}

// SoftDeleteProjectPath is where a soft-deleted project directory is kept.
func SoftDeleteProjectPath(configDir, project string) string {
	return filepath.Join(ProjectsDir(configDir), trashDirName, project)
}

// ScanSoftDeletes lists soft-deleted definitions by walking the trash
// directories. The control plane uses it to stop (scale to zero) the workloads
// of soft-deleted targets instead of deleting them.
func ScanSoftDeletes(configDir string) ([]SoftDelete, error) {
	projectsDir := ProjectsDir(configDir)
	var out []SoftDelete

	if entries, err := os.ReadDir(filepath.Join(projectsDir, trashDirName)); err == nil {
		for _, e := range entries {
			if e.IsDir() {
				out = append(out, SoftDelete{Kind: SoftDeleteProject, Project: e.Name(), Path: filepath.Join(projectsDir, trashDirName, e.Name())})
			}
		}
	}

	projects, err := ListProjects(projectsDir)
	if err != nil {
		return nil, err
	}
	for _, p := range projects {
		appsDir := filepath.Join(ProjectDir(configDir, p), appsDirName, trashDirName)
		for _, name := range listTrashYAML(appsDir) {
			out = append(out, SoftDelete{Kind: SoftDeleteApp, Project: p, App: name, Path: filepath.Join(appsDir, name+".yml")})
		}
		envDir := filepath.Join(ProjectDir(configDir, p), environmentsDirName, trashDirName)
		for _, name := range listTrashYAML(envDir) {
			out = append(out, SoftDelete{Kind: SoftDeleteEnvironment, Project: p, Environment: name, Path: filepath.Join(envDir, name+".yml")})
		}
	}
	return out, nil
}

func listTrashYAML(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".yml" {
			continue
		}
		names = append(names, trimYAMLExt(e.Name()))
	}
	return names
}
