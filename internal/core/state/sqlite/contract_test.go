package sqlite_test

import (
	"path/filepath"
	"testing"

	"github.com/moureau-dev/menina/internal/core/state/contract"
	"github.com/moureau-dev/menina/internal/core/state/contracttest"
	"github.com/moureau-dev/menina/internal/core/state/sqlite"
)

func TestStoreContract(t *testing.T) {
	contracttest.Run(t, func(t *testing.T) contract.Store {
		db, err := sqlite.Open(filepath.Join(t.TempDir(), "store.db"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = db.Close() })
		if err := db.Migrate(t.Context()); err != nil {
			t.Fatal(err)
		}
		return db
	})
}
