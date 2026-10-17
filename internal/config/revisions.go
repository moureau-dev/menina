package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const revisionsDirName = ".revisions"
const maxRevisions = 50

// RevisionEntry is one file's state before an apply.
type RevisionEntry struct {
	Path    string `json:"path"`
	Existed bool   `json:"existed"`
	Before  []byte `json:"before,omitempty"`
}

// Revision is a snapshot of the files an apply changed.
type Revision struct {
	ID          string          `json:"id"`
	Project     string          `json:"project"`
	Environment string          `json:"environment"`
	CreatedAt   time.Time       `json:"createdAt"`
	Files       int             `json:"files"`
	Entries     []RevisionEntry `json:"entries,omitempty"`
}

func revisionsDir(configDir string) string {
	return filepath.Join(configDir, revisionsDirName)
}

// SaveRevision captures the current state of paths (absolute paths under
// configDir) and writes a revision file. It returns the revision id.
func SaveRevision(configDir, project, environment string, paths []string) (string, error) {
	seen := map[string]bool{}
	entries := make([]RevisionEntry, 0, len(paths))
	for _, path := range paths {
		rel, err := filepath.Rel(configDir, path)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return "", fmt.Errorf("revision path %q is outside the config directory", path)
		}
		if seen[rel] {
			continue
		}
		seen[rel] = true
		entry := RevisionEntry{Path: filepath.ToSlash(rel)}
		data, err := os.ReadFile(path)
		switch {
		case err == nil:
			entry.Existed = true
			entry.Before = data
		case os.IsNotExist(err):
			entry.Existed = false
		default:
			return "", err
		}
		entries = append(entries, entry)
	}
	if len(entries) == 0 {
		return "", nil
	}
	now := time.Now().UTC()
	id := now.Format("20060102T150405.000000000")
	rev := Revision{ID: id, Project: project, Environment: environment, CreatedAt: now, Entries: entries}
	data, err := json.Marshal(rev)
	if err != nil {
		return "", err
	}
	dir := revisionsDir(configDir)
	if err := os.MkdirAll(dir, 0o770|os.ModeSetgid); err != nil {
		return "", err
	}
	path := filepath.Join(dir, id+".json")
	if err := os.WriteFile(path, data, 0o640); err != nil {
		return "", err
	}
	if err := os.Chmod(path, 0o640); err != nil {
		return "", err
	}
	_ = pruneRevisions(dir)
	return id, nil
}

// ListRevisions returns revision metadata, newest first, without file contents.
func ListRevisions(configDir string) ([]Revision, error) {
	entries, err := os.ReadDir(revisionsDir(configDir))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []Revision
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(revisionsDir(configDir), entry.Name()))
		if err != nil {
			continue
		}
		var rev Revision
		if json.Unmarshal(data, &rev) != nil {
			continue
		}
		rev.Files = len(rev.Entries)
		rev.Entries = nil
		out = append(out, rev)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

// RestoreRevision writes every file back to its pre-apply state and removes
// files that did not exist before that revision.
func RestoreRevision(configDir, id string) error {
	if id == "" || strings.ContainsAny(id, `/\`) {
		return fmt.Errorf("invalid revision id %q", id)
	}
	path := filepath.Join(revisionsDir(configDir), id+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var rev Revision
	if err := json.Unmarshal(data, &rev); err != nil {
		return err
	}
	for _, entry := range rev.Entries {
		target := filepath.Join(configDir, filepath.FromSlash(entry.Path))
		rel, err := filepath.Rel(configDir, target)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return fmt.Errorf("revision %s contains an unsafe path %q", id, entry.Path)
		}
		if !entry.Existed {
			if err := os.Remove(target); err != nil && !os.IsNotExist(err) {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o770|os.ModeSetgid); err != nil {
			return err
		}
		if err := os.WriteFile(target, entry.Before, 0o640); err != nil {
			return err
		}
		if err := os.Chmod(target, 0o640); err != nil {
			return err
		}
	}
	return nil
}

func pruneRevisions(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	var names []string
	for _, entry := range entries {
		if !entry.IsDir() && filepath.Ext(entry.Name()) == ".json" {
			names = append(names, entry.Name())
		}
	}
	if len(names) <= maxRevisions {
		return nil
	}
	sort.Strings(names)
	for _, name := range names[:len(names)-maxRevisions] {
		_ = os.Remove(filepath.Join(dir, name))
	}
	return nil
}
