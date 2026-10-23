package client

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/moureau-dev/menina/internal/clierr"
	"github.com/moureau-dev/menina/internal/config"
	"github.com/moureau-dev/menina/internal/contract/manifest"
	"github.com/moureau-dev/menina/internal/version"
)

type applyChange struct {
	Path   string `json:"path"`
	Action string `json:"action"`
	Diff   string `json:"diff,omitempty"`
}

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
			return clierr.New(clierr.CodeConflict, fmt.Sprintf("app %q is owned by %s; set source.repo in the manifest", name, app.Source.Repo), "use a different app name or declare the owning source.repo")
		case r.File.Source.Repo != "" && app.Source.Repo != "" && app.Source.Repo != r.File.Source.Repo:
			return clierr.New(clierr.CodeConflict, fmt.Sprintf("app %q is owned by %s, not %s", name, app.Source.Repo, r.File.Source.Repo), "use a different app name or fix source.repo")
		}
	}
	return nil
}

func planPrune(configDir string, r *manifest.Rendered) ([]string, error) {
	if r.File.Source.Repo == "" {
		return nil, clierr.New(clierr.CodeValidation, "--prune requires source.repo in the manifest (or --source-repo)", "set source.repo so only apps owned by this repository are pruned")
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

type planWrite struct {
	Path          string `json:"path"`
	Content       []byte `json:"content"`
	Before        []byte `json:"before,omitempty"`
	BeforeHash    string `json:"beforeHash"`
	BeforeExisted bool   `json:"beforeExisted"`
}

type planDelete struct {
	Path   string `json:"path"`
	Before []byte `json:"before,omitempty"`
	Hash   string `json:"hash"`
}

type applyPlan struct {
	ID          string       `json:"id"`
	Project     string       `json:"project"`
	Environment string       `json:"environment"`
	BaseState   string       `json:"baseState"`
	Schema      int          `json:"schemaVersion"`
	ToolVersion string       `json:"toolVersion"`
	CreatedAt   time.Time    `json:"createdAt"`
	Writes      []planWrite  `json:"writes"`
	Deletes     []planDelete `json:"deletes"`
}

func currentStateToken(configDir, project, environment string) (string, error) {
	cfg, err := config.LoadPlatform(configDir)
	if err != nil {
		return "", err
	}
	projects, err := config.LoadAllProjects(config.ProjectsDir(configDir))
	if err != nil {
		return "", err
	}
	return config.StateToken(cfg, projects, project, environment)
}

func savePlan(configDir string, r *manifest.Rendered, deletions []string) (*applyPlan, error) {
	files, err := r.Plan(configDir)
	if err != nil {
		return nil, err
	}
	base, err := currentStateToken(configDir, r.Project.Name, r.Environment.Name)
	if err != nil {
		return nil, err
	}
	plan := &applyPlan{
		Project: r.Project.Name, Environment: r.Environment.Name, BaseState: base,
		Schema: manifest.CurrentVersion, ToolVersion: version.Get().Version, CreatedAt: time.Now().UTC(),
	}
	for _, file := range files {
		w := planWrite{Path: file.Path, Content: file.Content}
		if before, err := os.ReadFile(file.Path); err == nil {
			w.Before, w.BeforeExisted = before, true
			w.BeforeHash = contentHash(before)
		} else if !os.IsNotExist(err) {
			return nil, err
		}
		plan.Writes = append(plan.Writes, w)
	}
	for _, path := range deletions {
		before, err := os.ReadFile(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		plan.Deletes = append(plan.Deletes, planDelete{Path: path, Before: before, Hash: contentHash(before)})
	}
	plan.ID = planHash(plan)
	dir := filepath.Join(configDir, ".plans")
	if err := os.MkdirAll(dir, 0o770|os.ModeSetgid); err != nil {
		return nil, err
	}
	data, err := json.Marshal(plan)
	if err != nil {
		return nil, err
	}
	path := filepath.Join(dir, plan.ID+".json")
	if err := os.WriteFile(path, data, 0o640); err != nil {
		return nil, err
	}
	prunePlanFiles(dir)
	return plan, nil
}

func loadPlan(configDir, id string) (*applyPlan, error) {
	if id == "" || strings.ContainsAny(id, `/\`) {
		return nil, fmt.Errorf("invalid plan id %q", id)
	}
	data, err := os.ReadFile(filepath.Join(configDir, ".plans", id+".json"))
	if err != nil {
		return nil, err
	}
	var plan applyPlan
	if err := json.Unmarshal(data, &plan); err != nil {
		return nil, err
	}
	return &plan, nil
}

func planHash(plan *applyPlan) string {
	type hashable struct {
		Project     string       `json:"project"`
		Environment string       `json:"environment"`
		BaseState   string       `json:"baseState"`
		Schema      int          `json:"schemaVersion"`
		ToolVersion string       `json:"toolVersion"`
		Writes      []planWrite  `json:"writes"`
		Deletes     []planDelete `json:"deletes"`
	}
	h := hashable{plan.Project, plan.Environment, plan.BaseState, plan.Schema, plan.ToolVersion, plan.Writes, plan.Deletes}
	data, _ := json.Marshal(h)
	return contentHash(data)
}

func contentHash(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func prunePlanFiles(dir string) {
	entries, _ := os.ReadDir(dir)
	var names []string
	for _, e := range entries {
		if !e.IsDir() && filepath.Ext(e.Name()) == ".json" {
			names = append(names, e.Name())
		}
	}
	if len(names) <= 50 {
		return
	}
	sort.Strings(names)
	for _, name := range names[:len(names)-50] {
		_ = os.Remove(filepath.Join(dir, name))
	}
}

func verifyPlanBase(configDir string, plan *applyPlan) error {
	current, err := currentStateToken(configDir, plan.Project, plan.Environment)
	if err != nil {
		return err
	}
	if current != plan.BaseState {
		return clierr.New(clierr.CodeConflict, "configuration changed since this plan was created", "run `apply --diff` again and review the new plan")
	}
	for _, w := range plan.Writes {
		before, err := os.ReadFile(w.Path)
		switch {
		case err == nil && (contentHash(before) != w.BeforeHash || !w.BeforeExisted):
			return clierr.New(clierr.CodeConflict, fmt.Sprintf("%s changed since the plan was created", w.Path), "run `apply --diff` again")
		case os.IsNotExist(err) && w.BeforeExisted:
			return clierr.New(clierr.CodeConflict, fmt.Sprintf("%s was removed since the plan was created", w.Path), "run `apply --diff` again")
		case err != nil && !os.IsNotExist(err):
			return err
		}
	}
	for _, d := range plan.Deletes {
		before, err := os.ReadFile(d.Path)
		if os.IsNotExist(err) {
			return clierr.New(clierr.CodeConflict, fmt.Sprintf("%s was already removed since the plan was created", d.Path), "run `apply --diff` again")
		}
		if err != nil {
			return err
		}
		if contentHash(before) != d.Hash {
			return clierr.New(clierr.CodeConflict, fmt.Sprintf("%s changed since the plan was created", d.Path), "run `apply --diff` again")
		}
	}
	return nil
}

func planChanges(plan *applyPlan) []applyChange {
	var changes []applyChange
	for _, w := range plan.Writes {
		action := "create"
		if w.BeforeExisted {
			action = "update"
			if string(w.Before) == string(w.Content) || yamlEqual(w.Before, w.Content) {
				continue
			}
		}
		changes = append(changes, applyChange{Path: w.Path, Action: action, Diff: lineDiff(string(w.Before), string(w.Content))})
	}
	for _, d := range plan.Deletes {
		changes = append(changes, applyChange{Path: d.Path, Action: "delete", Diff: lineDiff(string(d.Before), "")})
	}
	return changes
}

func applyFromPlan(cmd *cobra.Command, app *App, planID, ifRevision string, diff, dryRun, verify bool) error {
	plan, err := loadPlan(app.configDir(), planID)
	if err != nil {
		return clierr.Wrap(clierr.CodeNotFound, err)
	}
	lock, err := config.AcquireLock(app.configDir(), 10*time.Second)
	if err != nil {
		return clierr.Wrap(clierr.CodeConflict, err)
	}
	defer lock.Release()
	if err := lock.Check(); err != nil {
		return clierr.Wrap(clierr.CodeConflict, err)
	}
	if err := verifyPlanBase(app.configDir(), plan); err != nil {
		return err
	}
	if ifRevision != "" && ifRevision != plan.BaseState {
		return clierr.New(clierr.CodeConflict, "--if-revision does not match the plan base state", "regenerate the plan")
	}
	changes := planChanges(plan)
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
	paths := make([]string, 0, len(plan.Writes)+len(plan.Deletes))
	for _, w := range plan.Writes {
		paths = append(paths, w.Path)
	}
	for _, d := range plan.Deletes {
		paths = append(paths, d.Path)
	}
	revision, err := config.SaveRevisionWithMeta(app.configDir(), config.RevisionMeta{
		Project: plan.Project, Environment: plan.Environment, PlanID: plan.ID,
	}, paths)
	if err != nil {
		return err
	}
	for _, w := range plan.Writes {
		if err := os.MkdirAll(filepath.Dir(w.Path), 0o770|os.ModeSetgid); err != nil {
			return err
		}
		if err := os.WriteFile(w.Path, w.Content, 0o640); err != nil {
			return err
		}
		if err := os.Chmod(w.Path, 0o640); err != nil {
			return err
		}
	}
	for _, d := range plan.Deletes {
		if err := os.Remove(d.Path); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	if verify {
		for _, w := range plan.Writes {
			if _, err := os.Stat(w.Path); err != nil {
				return fmt.Errorf("verification failed: %s: %w", w.Path, err)
			}
		}
		for _, d := range plan.Deletes {
			if _, err := os.Stat(d.Path); !os.IsNotExist(err) {
				return fmt.Errorf("verification failed: %s still exists", d.Path)
			}
		}
	}
	newState, _ := currentStateToken(app.configDir(), plan.Project, plan.Environment)
	if app.jsonOutput() {
		return app.printJSON(map[string]any{
			"project": plan.Project, "environment": plan.Environment, "applied": true,
			"planId": plan.ID, "revision": revision, "stateToken": newState,
			"baseStateToken": plan.BaseState, "pruned": len(plan.Deletes), "verified": verify,
		})
	}
	app.infof("Applied plan %s for %s/%s", plan.ID, plan.Project, plan.Environment)
	if revision != "" {
		app.infof("Revision: %s", revision)
	}
	if newState != "" {
		app.infof("State token: %s", newState)
	}
	if len(plan.Deletes) > 0 {
		app.infof("Pruned %d stale file(s).", len(plan.Deletes))
	}
	return nil
}
