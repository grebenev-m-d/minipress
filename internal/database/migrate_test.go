package database

import (
	"path/filepath"
	"testing"
)

func open(t *testing.T) *sqlDB {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return &sqlDB{db}
}

func TestMigrateFreshAndIdempotent(t *testing.T) {
	db := open(t)
	for i := 0; i < 2; i++ {
		if err := Migrate(db.DB); err != nil {
			t.Fatalf("run %d: %v", i+1, err)
		}
	}
	if got := db.count(t, `SELECT COUNT(*) FROM schema_migrations`); got != 1 {
		t.Errorf("applied migrations: %d, expected 1", got)
	}
	for _, table := range []string{"pages", "images", "image_refs"} {
		if db.count(t, `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='`+table+`'`) != 1 {
			t.Errorf("missing table %s", table)
		}
	}
	if db.count(t, `SELECT COUNT(*) FROM pragma_table_info('pages') WHERE name = 'is_published'`) != 1 {
		t.Error("missing column pages.is_published")
	}
}
