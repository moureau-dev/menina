package client

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/moureau-dev/menina/internal/api"
	"github.com/moureau-dev/menina/internal/api/errors"
	"github.com/moureau-dev/menina/internal/contract/manifest"
	"github.com/moureau-dev/menina/internal/core/config"
	configplan "github.com/moureau-dev/menina/internal/core/config/plan"
	"github.com/moureau-dev/menina/internal/version"
)

type applyChange = configplan.Change
type planWrite = configplan.Write
type planDelete = configplan.Delete
type applyPlan = configplan.Plan

func planApply(configDir string, r *manifest.Rendered, deletions []string) ([]applyChange, error) {
	files, err := r.Plan(configDir)
	if err != nil {
		return nil, err
	}
	var changes []applyChange
	for _, path := range deletions {
		existing, err := os.ReadFile(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		changes = append(changes, applyChange{
			Path:   path,
			Action: "delete",
			Diff:   lineDiff(string(existing), ""),
		})
	}
	for _, file := range files {
		existing, err := os.ReadFile(file.Path)
		if os.IsNotExist(err) {
			changes = append(changes, applyChange{
				Path:   file.Path,
				Action: "create",
				Diff:   lineDiff("", string(file.Content)),
			})
			continue
		}
		if err != nil {
			return nil, err
		}
		if string(existing) == string(file.Content) || yamlEqual(existing, file.Content) {
			continue
		}
		changes = append(changes, applyChange{
			Path:   file.Path,
			Action: "update",
			Diff:   lineDiff(string(existing), string(file.Content)),
		})
	}
	return changes, nil
}

func checkOwnership(configDir string, r *manifest.Rendered) error {
	existing, err := config.LoadApps(config.ProjectsDir(configDir), r.Project.Name)
	if err != nil {
		return err
	}
	for name := range r.Apps {
		app := existing[name]
		if app == nil {
			continue
		}
		switch {
		case r.File.Source.Repo == "" && app.Source.Repo != "":
			return apierrors.New(apierrors.CodeConflict, fmt.Sprintf("app %q is owned by %s; set source.repo in the manifest", name, app.Source.Repo), "use a different app name or declare the owning source.repo")
		case r.File.Source.Repo != "" && app.Source.Repo != "" && app.Source.Repo != r.File.Source.Repo:
			return apierrors.New(apierrors.CodeConflict, fmt.Sprintf("app %q is owned by %s, not %s", name, app.Source.Repo, r.File.Source.Repo), "use a different app name or fix source.repo")
		}
	}
	return nil
}

func planPrune(configDir string, r *manifest.Rendered) ([]string, error) {
	if r.File.Source.Repo == "" {
		return nil, apierrors.New(apierrors.CodeValidation, "--prune requires source.repo in the manifest (or --source-repo)", "set source.repo so only apps owned by this repository are pruned")
	}
	project, err := config.LoadProject(config.ProjectsDir(configDir), r.Project.Name)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var paths []string
	add := func(path string) {
		if !seen[path] {
			seen[path] = true
			paths = append(paths, path)
		}
	}
	for name, app := range project.Apps {
		if _, declared := r.Apps[name]; declared {
			continue
		}
		if app.Source.Repo != r.File.Source.Repo {
			continue
		}
		add(config.AppOverridePath(configDir, r.Project.Name, r.Environment.Name, name))
		usedElsewhere := false
		for envName, env := range project.Envs {
			if envName == r.Environment.Name {
				continue
			}
			if _, ok := env.Apps[name]; ok {
				usedElsewhere = true
				break
			}
		}
		if !usedElsewhere {
			add(filepath.Join(config.ProjectDir(configDir, r.Project.Name), "apps", name+".yml"))
		}
	}
	return paths, nil
}

func yamlEqual(a, b []byte) bool {
	var av, bv any
	if yaml.Unmarshal(a, &av) != nil || yaml.Unmarshal(b, &bv) != nil {
		return false
	}
	return reflect.DeepEqual(av, bv)
}

func lineDiff(before, after string) string {
	a := splitLines(before)
	b := splitLines(after)
	n, m := len(a), len(b)
	dp := make([][]int, n+1)
	for i := range dp {
		dp[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[i] == b[j] {
				dp[i][j] = dp[i+1][j+1] + 1
			} else if dp[i+1][j] >= dp[i][j+1] {
				dp[i][j] = dp[i+1][j]
			} else {
				dp[i][j] = dp[i][j+1]
			}
		}
	}
	var out []string
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case a[i] == b[j]:
			out = append(out, " "+a[i])
			i++
			j++
		case dp[i+1][j] >= dp[i][j+1]:
			out = append(out, "-"+a[i])
			i++
		default:
			out = append(out, "+"+b[j])
			j++
		}
	}
	for ; i < n; i++ {
		out = append(out, "-"+a[i])
	}
	for ; j < m; j++ {
		out = append(out, "+"+b[j])
	}
	return strings.Join(out, "\n")
}

func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	s = strings.TrimSuffix(s, "\n")
	return strings.Split(s, "\n")
}

func verifyApply(configDir string, r *manifest.Rendered) error {
	project, err := config.LoadProject(config.ProjectsDir(configDir), r.Project.Name)
	if err != nil {
		return err
	}
	env, ok := project.Envs[r.Environment.Name]
	if !ok {
		return fmt.Errorf("environment %q not found after apply", r.Environment.Name)
	}
	for name := range r.Apps {
		if _, ok := project.Apps[name]; !ok {
			return fmt.Errorf("app %q not found after apply", name)
		}
		if _, ok := env.Apps[name]; !ok {
			return fmt.Errorf("environment override for %q not found after apply", name)
		}
	}
	return nil
}

func currentStateToken(configDir, project, environment string) (string, error) {
	return configplan.CurrentStateToken(configDir, project, environment)
}

func savePlan(configDir string, r *manifest.Rendered, deletions []string) (*configplan.Plan, error) {
	files, err := r.Plan(configDir)
	if err != nil {
		return nil, err
	}
	base, err := currentStateToken(configDir, r.Project.Name, r.Environment.Name)
	if err != nil {
		return nil, err
	}
	p := &configplan.Plan{
		Project: r.Project.Name, Environment: r.Environment.Name, BaseState: base,
		Schema: manifest.CurrentVersion, ToolVersion: version.Get().Version, CreatedAt: time.Now().UTC(),
		SourceRepo: r.File.Source.Repo, SourceRevision: r.File.Source.Revision,
	}
	for _, file := range files {
		w := configplan.Write{Path: file.Path, Content: file.Content}
		if before, err := os.ReadFile(file.Path); err == nil {
			w.Before, w.BeforeExisted = before, true
			w.BeforeHash = configplan.ContentHash(before)
		} else if !os.IsNotExist(err) {
			return nil, err
		}
		p.Writes = append(p.Writes, w)
	}
	for _, path := range deletions {
		before, err := os.ReadFile(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		p.Deletes = append(p.Deletes, configplan.Delete{Path: path, Before: before, Hash: configplan.ContentHash(before)})
	}
	p.ID = configplan.Hash(p)
	if err := configplan.Save(configDir, p); err != nil {
		return nil, err
	}
	return p, nil
}

func loadPlan(configDir, id string) (*configplan.Plan, error) {
	return configplan.Load(configDir, id)
}

func applyFromPlan(cmd *cobra.Command, app *App, planID, ifRevision string, diff, dryRun, verify bool) error {
	plan, err := loadPlan(app.configDir(), planID)
	if err != nil {
		return apierrors.Wrap(apierrors.CodeNotFound, err)
	}
	if ifRevision != "" && ifRevision != plan.BaseState {
		return apierrors.New(apierrors.CodeConflict, "--if-revision does not match the plan base state", "regenerate the plan")
	}
	changes := configplan.Changes(plan)
	if diff || dryRun {
		if app.jsonOutput() {
			return app.printJSON(map[string]any{
				"project": plan.Project, "environment": plan.Environment, "applied": false,
				"dryRun": true, "planId": plan.ID, "stateToken": plan.BaseState, "changes": changes,
			})
		}
		if len(changes) == 0 {
			app.infof("No changes.")
			return nil
		}
		for _, change := range changes {
			app.infof("%s %s", strings.ToUpper(change.Action), change.Path)
			if change.Diff != "" {
				app.infof("%s", change.Diff)
			}
		}
		return nil
	}
	result, err := executePlan(cmd, app, plan, ifRevision, verify)
	if err != nil {
		return err
	}
	return printPlanResult(app, plan, result)
}

func executePlan(cmd *cobra.Command, app *App, plan *configplan.Plan, ifStateToken string, verify bool) (configplan.Result, error) {
	if cfg, err := app.loadConfig(); err == nil {
		if client, err := api.Dial(cfg.Platform.ControlSocket, cfg.Security.ControlTokenFile); err == nil {
			result, err := client.ApplyPlan(cmd.Context(), *plan, ifStateToken, verify)
			if err != nil {
				return configplan.Result{}, err
			}
			return result, nil
		}
	}
	return configplan.Apply(app.configDir(), plan, ifStateToken, verify, "cli")
}

func printPlanResult(app *App, plan *configplan.Plan, result configplan.Result) error {
	if app.jsonOutput() {
		return app.printJSON(map[string]any{
			"project": result.Project, "environment": result.Environment, "applied": true,
			"planId": result.PlanID, "revision": result.Revision, "stateToken": result.StateToken,
			"baseStateToken": plan.BaseState, "pruned": result.Pruned, "verified": result.Verified,
		})
	}
	app.infof("Applied plan %s for %s/%s", result.PlanID, result.Project, result.Environment)
	if result.Revision != "" {
		app.infof("Revision: %s", result.Revision)
	}
	if result.StateToken != "" {
		app.infof("State token: %s", result.StateToken)
	}
	if result.Pruned > 0 {
		app.infof("Pruned %d stale file(s).", result.Pruned)
	}
	return nil
}
