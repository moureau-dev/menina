package plan

import (
	"fmt"
	"path/filepath"
	"strings"

	apierrors "github.com/moureau-dev/menina/internal/api/errors"
	"github.com/moureau-dev/menina/internal/core/config"
)

// ValidateOwnership enforces source.repo as a check for existing apps.
func ValidateOwnership(configDir string, p *Plan) error {
	if p.SourceRepo == "" && len(p.Deletes) > 0 {
		return apierrors.New(apierrors.CodeValidation, "pruning requires source.repo", "declare the owning repository in source.repo")
	}
	for _, w := range p.Writes {
		if err := checkOwnershipPath(configDir, w.Path, p.SourceRepo, false); err != nil {
			return err
		}
	}
	for _, d := range p.Deletes {
		if err := checkOwnershipPath(configDir, d.Path, p.SourceRepo, true); err != nil {
			return err
		}
	}
	return nil
}

func checkOwnershipPath(configDir, path, sourceRepo string, deleting bool) error {
	project, app, ok := appFromConfigPath(configDir, path)
	if !ok {
		return nil
	}
	existing, err := config.LoadApps(config.ProjectsDir(configDir), project)
	if err != nil {
		return err
	}
	current := existing[app]
	if current == nil || current.Source.Repo == "" {
		return nil
	}
	if sourceRepo == "" {
		return apierrors.New(apierrors.CodeConflict, fmt.Sprintf("app %q is owned by %s; set source.repo in the manifest", app, current.Source.Repo), "use a different app name or declare the owning source.repo")
	}
	if current.Source.Repo != sourceRepo {
		return apierrors.New(apierrors.CodeConflict, fmt.Sprintf("app %q is owned by %s, not %s", app, current.Source.Repo, sourceRepo), "use a different app name or fix source.repo")
	}
	if deleting && current.Source.Repo != sourceRepo {
		return apierrors.New(apierrors.CodeConflict, fmt.Sprintf("app %q is owned by %s, not %s", app, current.Source.Repo, sourceRepo), "only the owning repository may prune this app")
	}
	return nil
}

func appFromConfigPath(configDir, path string) (project, app string, ok bool) {
	rel, err := filepath.Rel(configDir, path)
	if err != nil {
		return "", "", false
	}
	parts := strings.Split(filepath.ToSlash(rel), "/")
	if len(parts) < 4 || parts[0] != "projects" {
		return "", "", false
	}
	project = parts[1]
	switch {
	case len(parts) == 4 && parts[2] == "apps" && strings.HasSuffix(parts[3], ".yml"):
		return project, strings.TrimSuffix(parts[3], ".yml"), true
	case len(parts) == 5 && parts[2] == "environments" && strings.HasSuffix(parts[4], ".yml"):
		return project, strings.TrimSuffix(parts[4], ".yml"), true
	}
	return "", "", false
}
