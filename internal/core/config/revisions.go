package config

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
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
	ID             string          `json:"id"`
	Project        string          `json:"project"`
	Environment    string          `json:"environment"`
	CreatedAt      time.Time       `json:"createdAt"`
	Actor          string          `json:"actor,omitempty"`
	SourceRepo     string          `json:"sourceRepo,omitempty"`
	SourceRevision string          `json:"sourceRevision,omitempty"`
	PlanID         string          `json:"planId,omitempty"`
	ContentHash    string          `json:"contentHash,omitempty"`
	Files          int             `json:"files"`
	Entries        []RevisionEntry `json:"entries,omitempty"`
}

// RevisionMeta records attribution for a revision.
type RevisionMeta struct {
	Project        string
	Environment    string
	Actor          string
	SourceRepo     string
	SourceRevision string
	PlanID         string
}

func revisionsDir(configDir string) string {
	return filepath.Join(configDir, revisionsDirName)
}

// SaveRevision captures the current state of paths (absolute paths under
// configDir) and writes a revision file. It returns the revision id.
func SaveRevision(configDir, project, environment string, paths []string) (string, error) {
	return SaveRevisionWithMeta(configDir, RevisionMeta{Project: project, Environment: environment, Actor: currentUser()}, paths)
}

// SaveRevisionWithMeta is SaveRevision with attribution metadata.
func SaveRevisionWithMeta(configDir string, meta RevisionMeta, paths []string) (string, error) {
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
	if meta.Actor == "" {
		meta.Actor = currentUser()
	}
	rev := Revision{
		ID: id, Project: meta.Project, Environment: meta.Environment, CreatedAt: now,
		Actor: meta.Actor, SourceRepo: meta.SourceRepo, SourceRevision: meta.SourceRevision,
		PlanID: meta.PlanID, Entries: entries,
	}
	hashData, _ := json.Marshal(entries)
	sum := sha256.Sum256(hashData)
	rev.ContentHash = hex.EncodeToString(sum[:])
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
			return []Revision{}, nil
		}
		return nil, err
	}
	out := []Revision{}
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

func revisionRetention() int {
	if v := os.Getenv("MENINA_REVISION_RETENTION"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return maxRevisions
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
	limit := revisionRetention()
	if len(names) <= limit {
		return nil
	}
	sort.Strings(names)
	for _, name := range names[:len(names)-limit] {
		_ = os.Remove(filepath.Join(dir, name))
	}
	return nil
}
