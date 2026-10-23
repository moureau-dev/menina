// Package plan stores and applies rendered configuration changes.
package plan

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

	"gopkg.in/yaml.v3"

	"github.com/moureau-dev/menina/internal/api/errors"
	"github.com/moureau-dev/menina/internal/core/config"
)

// Change is one user-visible file change.
type Change struct {
	Path   string `json:"path"`
	Action string `json:"action"`
	Diff   string `json:"diff,omitempty"`
}

// Write is one file the plan writes.
type Write struct {
	Path          string `json:"path"`
	Content       []byte `json:"content"`
	Before        []byte `json:"before,omitempty"`
	BeforeHash    string `json:"beforeHash"`
	BeforeExisted bool   `json:"beforeExisted"`
}

// Delete is one file the plan removes.
type Delete struct {
	Path   string `json:"path"`
	Before []byte `json:"before,omitempty"`
	Hash   string `json:"hash"`
}

// Plan is a reviewed set of desired-state file changes.
type Plan struct {
	ID             string    `json:"id"`
	Project        string    `json:"project"`
	Environment    string    `json:"environment"`
	BaseState      string    `json:"baseState"`
	Schema         int       `json:"schemaVersion"`
	ToolVersion    string    `json:"toolVersion"`
	SourceRepo     string    `json:"sourceRepo,omitempty"`
	SourceRevision string    `json:"sourceRevision,omitempty"`
	CreatedAt      time.Time `json:"createdAt"`
	Writes         []Write   `json:"writes"`
	Deletes        []Delete  `json:"deletes"`
}

// Result reports the outcome of applying a plan.
type Result struct {
	Project     string `json:"project"`
	Environment string `json:"environment"`
	PlanID      string `json:"planId"`
	Revision    string `json:"revision,omitempty"`
	StateToken  string `json:"stateToken,omitempty"`
	Pruned      int    `json:"pruned,omitempty"`
	Verified    bool   `json:"verified,omitempty"`
}

// CurrentStateToken returns the canonical state token for a project/env.
func CurrentStateToken(configDir, project, environment string) (string, error) {
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

// Hash returns the plan ID for the plan contents.
func Hash(p *Plan) string {
	type hashable struct {
		Project        string   `json:"project"`
		Environment    string   `json:"environment"`
		BaseState      string   `json:"baseState"`
		Schema         int      `json:"schemaVersion"`
		ToolVersion    string   `json:"toolVersion"`
		SourceRepo     string   `json:"sourceRepo,omitempty"`
		SourceRevision string   `json:"sourceRevision,omitempty"`
		Writes         []Write  `json:"writes"`
		Deletes        []Delete `json:"deletes"`
	}
	h := hashable{p.Project, p.Environment, p.BaseState, p.Schema, p.ToolVersion, p.SourceRepo, p.SourceRevision, p.Writes, p.Deletes}
	data, _ := json.Marshal(h)
	return ContentHash(data)
}

// ContentHash returns the hex SHA-256 of data.
func ContentHash(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// Save writes a plan under the config directory.
func Save(configDir string, p *Plan) error {
	if p.ID == "" {
		p.ID = Hash(p)
	}
	dir := filepath.Join(configDir, ".plans")
	if err := os.MkdirAll(dir, 0o770|os.ModeSetgid); err != nil {
		return err
	}
	data, err := json.Marshal(p)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, p.ID+".json"), data, 0o640); err != nil {
		return err
	}
	pruneFiles(dir)
	return nil
}

// Load reads a plan by ID.
func Load(configDir, id string) (*Plan, error) {
	if id == "" || strings.ContainsAny(id, `/\`) {
		return nil, fmt.Errorf("invalid plan id %q", id)
	}
	data, err := os.ReadFile(filepath.Join(configDir, ".plans", id+".json"))
	if err != nil {
		return nil, err
	}
	var p Plan
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, err
	}
	return &p, nil
}

func pruneFiles(dir string) {
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

// VerifyBase checks the plan's base state token and every file hash.
func VerifyBase(configDir string, p *Plan) error {
	current, err := CurrentStateToken(configDir, p.Project, p.Environment)
	if err != nil {
		return err
	}
	if current != p.BaseState {
		return apierrors.New(apierrors.CodeConflict, "configuration changed since this plan was created", "run `apply --diff` again and review the new plan")
	}
	if Hash(p) != p.ID {
		return apierrors.New(apierrors.CodeConflict, "plan contents do not match the plan id", "regenerate the plan")
	}
	for _, w := range p.Writes {
		before, err := os.ReadFile(w.Path)
		switch {
		case err == nil && (ContentHash(before) != w.BeforeHash || !w.BeforeExisted):
			return apierrors.New(apierrors.CodeConflict, fmt.Sprintf("%s changed since the plan was created", w.Path), "run `apply --diff` again")
		case os.IsNotExist(err) && w.BeforeExisted:
			return apierrors.New(apierrors.CodeConflict, fmt.Sprintf("%s was removed since the plan was created", w.Path), "run `apply --diff` again")
		case err != nil && !os.IsNotExist(err):
			return err
		}
	}
	for _, d := range p.Deletes {
		before, err := os.ReadFile(d.Path)
		if os.IsNotExist(err) {
			return apierrors.New(apierrors.CodeConflict, fmt.Sprintf("%s was already removed since the plan was created", d.Path), "run `apply --diff` again")
		}
		if err != nil {
			return err
		}
		if ContentHash(before) != d.Hash {
			return apierrors.New(apierrors.CodeConflict, fmt.Sprintf("%s changed since the plan was created", d.Path), "run `apply --diff` again")
		}
	}
	return nil
}

// Changes summarizes a plan.
func Changes(p *Plan) []Change {
	var out []Change
	for _, w := range p.Writes {
		action := "create"
		if w.BeforeExisted {
			action = "update"
			if string(w.Before) == string(w.Content) || yamlEqual(w.Before, w.Content) {
				continue
			}
		}
		out = append(out, Change{Path: w.Path, Action: action, Diff: lineDiff(string(w.Before), string(w.Content))})
	}
	for _, d := range p.Deletes {
		out = append(out, Change{Path: d.Path, Action: "delete", Diff: lineDiff(string(d.Before), "")})
	}
	return out
}

// Apply verifies and executes a plan under the config lock.
func Apply(configDir string, p *Plan, ifStateToken string, verify bool, actor string) (Result, error) {
	lock, err := config.AcquireLock(configDir, 10*time.Second)
	if err != nil {
		return Result{}, apierrors.Wrap(apierrors.CodeConflict, err)
	}
	defer lock.Release()
	if err := lock.Check(); err != nil {
		return Result{}, apierrors.Wrap(apierrors.CodeConflict, err)
	}
	if err := ValidatePaths(configDir, p); err != nil {
		return Result{}, err
	}
	if err := VerifyBase(configDir, p); err != nil {
		return Result{}, err
	}
	if err := ValidateOwnership(configDir, p); err != nil {
		return Result{}, err
	}
	if ifStateToken != "" && ifStateToken != p.BaseState {
		return Result{}, apierrors.New(apierrors.CodeConflict, "--if-state-token does not match the plan base state", "regenerate the plan")
	}
	paths := make([]string, 0, len(p.Writes)+len(p.Deletes))
	for _, w := range p.Writes {
		paths = append(paths, w.Path)
	}
	for _, d := range p.Deletes {
		paths = append(paths, d.Path)
	}
	revision, err := config.SaveRevisionWithMeta(configDir, config.RevisionMeta{
		Project: p.Project, Environment: p.Environment, Actor: actor,
		SourceRepo: p.SourceRepo, SourceRevision: p.SourceRevision, PlanID: p.ID,
	}, paths)
	if err != nil {
		return Result{}, err
	}
	for _, w := range p.Writes {
		if err := os.MkdirAll(filepath.Dir(w.Path), 0o770|os.ModeSetgid); err != nil {
			return Result{}, err
		}
		if err := os.WriteFile(w.Path, w.Content, 0o640); err != nil {
			return Result{}, err
		}
		if err := os.Chmod(w.Path, 0o640); err != nil {
			return Result{}, err
		}
	}
	for _, d := range p.Deletes {
		if err := os.Remove(d.Path); err != nil && !os.IsNotExist(err) {
			return Result{}, err
		}
	}
	if verify {
		for _, w := range p.Writes {
			if _, err := os.Stat(w.Path); err != nil {
				return Result{}, fmt.Errorf("verification failed: %s: %w", w.Path, err)
			}
		}
		for _, d := range p.Deletes {
			if _, err := os.Stat(d.Path); !os.IsNotExist(err) {
				return Result{}, fmt.Errorf("verification failed: %s still exists", d.Path)
			}
		}
	}
	newState, _ := CurrentStateToken(configDir, p.Project, p.Environment)
	return Result{Project: p.Project, Environment: p.Environment, PlanID: p.ID, Revision: revision, StateToken: newState, Pruned: len(p.Deletes), Verified: verify}, nil
}

// ValidatePaths rejects writes outside the configuration tree.
func ValidatePaths(configDir string, p *Plan) error {
	check := func(path string) error {
		rel, err := filepath.Rel(configDir, path)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return apierrors.New(apierrors.CodeValidation, fmt.Sprintf("plan path %q is outside the config directory", path), "")
		}
		if rel != "config.yml" && !strings.HasPrefix(rel, "projects"+string(filepath.Separator)) {
			return apierrors.New(apierrors.CodeValidation, fmt.Sprintf("plan path %q is not desired state", path), "")
		}
		return nil
	}
	for _, w := range p.Writes {
		if err := check(w.Path); err != nil {
			return err
		}
	}
	for _, d := range p.Deletes {
		if err := check(d.Path); err != nil {
			return err
		}
	}
	return nil
}

func yamlEqual(a, b []byte) bool {
	var av, bv any
	if yaml.Unmarshal(a, &av) != nil || yaml.Unmarshal(b, &bv) != nil {
		return false
	}
	return reflect.DeepEqual(av, bv)
}

func lineDiff(before, after string) string {
	a, b := splitLines(before), splitLines(after)
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
