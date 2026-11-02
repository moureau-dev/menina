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
		if err := MkdirAllSetgid(d, 0o770|os.ModeSetgid); err != nil {
			return err
		}
	}
	return nil
}

// MkdirAllSetgid creates path and any missing parents, then applies perm to
// each new directory. Linux ignores the setgid bit passed to mkdir, so it is
// set explicitly with chmod.
func MkdirAllSetgid(path string, perm os.FileMode) error {
	var missing []string
	for p := path; ; p = filepath.Dir(p) {
		if _, err := os.Stat(p); err == nil {
			break
		} else if !os.IsNotExist(err) {
			return err
		}
		missing = append(missing, p)
		parent := filepath.Dir(p)
		if parent == p {
			break
		}
	}
	for i := len(missing) - 1; i >= 0; i-- {
		if err := os.Mkdir(missing[i], perm.Perm()); err != nil && !os.IsExist(err) {
			return err
		}
		if err := os.Chmod(missing[i], perm); err != nil {
			return err
		}
	}
	return nil
}
