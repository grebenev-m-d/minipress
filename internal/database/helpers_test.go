package database

import (
	"database/sql"
	"testing"
)

type sqlDB struct{ *sql.DB }

func (d *sqlDB) exec(t *testing.T, q string) {
	t.Helper()
	if _, err := d.Exec(q); err != nil {
		t.Fatalf("exec: %v", err)
	}
}

func (d *sqlDB) count(t *testing.T, q string) int {
	t.Helper()
	var n int
	if err := d.QueryRow(q).Scan(&n); err != nil {
		t.Fatalf("query %q: %v", q, err)
	}
	return n
}
