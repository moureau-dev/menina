package config

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

// StateToken returns a stable hash of the resolved environment (project +
// environment + every app after merge). It is computed from in-memory config,
// not file bytes, so hand-edited YAML and comments are handled consistently.
// Missing projects/environments return an empty token.
func StateToken(cfg *Config, projects []*Project, project, environment string) (string, error) {
	resolved, err := NewResolver(cfg).Resolve(projects, 0)
	if err != nil {
		return "", err
	}
	rp, ok := resolved.Projects[project]
	if !ok {
		return "", nil
	}
	env, ok := rp.Environments[environment]
	if !ok {
		return "", nil
	}
	data, err := json.Marshal(env)
	if err != nil {
		return "", fmt.Errorf("marshal state: %w", err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}
