package config

import (
	"os"
	"path/filepath"
)

func LoadAllProjects(projectsDir string) ([]*Project, error) {
	names, err := ListProjects(projectsDir)
	if err != nil {
		return nil, err
	}
	var out []*Project
	for _, name := range names {
		p, err := LoadProject(projectsDir, name)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
}

func ProjectsDir(configDir string) string {
	return filepath.Join(configDir, projectsDirName)
}

func ProjectDir(configDir, project string) string {
	return filepath.Join(configDir, projectsDirName, project)
}

func EnsureProjectDirs(configDir, project string) error {
	base := ProjectDir(configDir, project)
	for _, d := range []string{
		base,
		filepath.Join(base, appsDirName),
		filepath.Join(base, environmentsDirName),
	} {
		if err := os.MkdirAll(d, 0o770|os.ModeSetgid); err != nil {
			return err
		}
		// chmod explicitly; MkdirAll modes are masked by umask.
		if err := os.Chmod(d, 0o770|os.ModeSetgid); err != nil {
			return err
		}
	}
	return nil
}
