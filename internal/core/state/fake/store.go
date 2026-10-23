// Package fake provides a configurable Store double for controller tests.
package fake

import (
	"context"

	"github.com/moureau-dev/menina/internal/core/state/contract"
	"github.com/moureau-dev/menina/internal/core/state/sqlite"
)

// Store embeds the contract and overrides the operations used by controller
// tests. Unimplemented methods panic if a test reaches them unexpectedly.
type Store struct {
	contract.Store

	Release         sqlite.Release
	HasRelease      bool
	SoftDelete      []sqlite.SoftDeleteTarget
	SyncedSoft      []sqlite.SoftDeleteTarget
	ObservedRows    []sqlite.Observed
	DeletedObserved []string
}

// New builds an empty fake store.
func New() *Store { return &Store{} }

func (s *Store) LatestRelease(context.Context, string, string, string) (sqlite.Release, bool, error) {
	return s.Release, s.HasRelease, nil
}

func (s *Store) SyncObservedSoftDeletes(_ context.Context, targets []sqlite.SoftDeleteTarget) error {
	s.SyncedSoft = append([]sqlite.SoftDeleteTarget(nil), targets...)
	s.SoftDelete = append([]sqlite.SoftDeleteTarget(nil), targets...)
	return nil
}

func (s *Store) SoftDeletedTargets(context.Context) ([]sqlite.SoftDeleteTarget, error) {
	return s.SoftDelete, nil
}

func (s *Store) DeleteObserved(_ context.Context, project, environment, app string) error {
	s.DeletedObserved = append(s.DeletedObserved, project+"/"+environment+"/"+app)
	return nil
}

func (s *Store) UpsertObserved(_ context.Context, o sqlite.Observed) error {
	s.ObservedRows = append(s.ObservedRows, o)
	return nil
}
