package repository

import (
	"database/sql"
	"errors"
	"time"
)

var ErrNotFound = errors.New("not found")

const sqlTime = "2006-01-02 15:04:05"

func formatTime(t time.Time) string { return t.UTC().Format(sqlTime) }

func notFound(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	return err
}
