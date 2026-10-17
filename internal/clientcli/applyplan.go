package clientcli

import (
	"fmt"
	"os"
	"reflect"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/moureau-dev/menina/internal/config"
	"github.com/moureau-dev/menina/internal/manifest"
)

type applyChange struct {
	Path   string `json:"path"`
	Action string `json:"action"`
	Diff   string `json:"diff,omitempty"`
}

func planApply(configDir string, r *manifest.Rendered) ([]applyChange, error) {
	files, err := r.Plan(configDir)
	if err != nil {
		return nil, err
	}
	var changes []applyChange
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
