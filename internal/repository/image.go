package repository

import (
	"context"
	"database/sql"
	"time"

	"minipress/internal/models"
)

type ImageRepo struct {
	db *sql.DB
}

func NewImageRepo(db *sql.DB) *ImageRepo { return &ImageRepo{db: db} }

func (r *ImageRepo) Register(ctx context.Context, fileName string) error {
	_, err := r.db.ExecContext(ctx,
		`INSERT OR IGNORE INTO images (file_name, orphaned_at) VALUES (?, CURRENT_TIMESTAMP)`, fileName)
	return err
}

func (r *ImageRepo) SyncPageRefs(ctx context.Context, pageID int64, names []string) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	current, err := imagesOfPage(ctx, tx, pageID)
	if err != nil {
		return err
	}
	have := make(map[int64]struct{}, len(current))
	for _, id := range current {
		have[id] = struct{}{}
	}

	want := make(map[int64]struct{}, len(names))
	for _, n := range names {
		var id int64
		err := tx.QueryRowContext(ctx, `SELECT id FROM images WHERE file_name = ?`, n).Scan(&id)
		if err == sql.ErrNoRows {
			continue
		}
		if err != nil {
			return err
		}
		want[id] = struct{}{}
	}

	var touched []int64
	for id := range have {
		if _, ok := want[id]; !ok {
			if _, err := tx.ExecContext(ctx, `DELETE FROM image_refs WHERE image_id = ? AND page_id = ?`, id, pageID); err != nil {
				return err
			}
			touched = append(touched, id)
		}
	}
	for id := range want {
		if _, ok := have[id]; !ok {
			if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO image_refs (image_id, page_id) VALUES (?, ?)`, id, pageID); err != nil {
				return err
			}
			touched = append(touched, id)
		}
	}
	if err := refreshOrphanMarks(ctx, tx, touched); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *ImageRepo) ListOrphanedBefore(ctx context.Context, cutoff time.Time, limit int) ([]models.Image, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, file_name FROM images WHERE orphaned_at IS NOT NULL AND orphaned_at <= ? ORDER BY orphaned_at LIMIT ?`,
		formatTime(cutoff), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []models.Image
	for rows.Next() {
		var im models.Image
		if err := rows.Scan(&im.ID, &im.FileName); err != nil {
			return nil, err
		}
		out = append(out, im)
	}
	return out, rows.Err()
}

func (r *ImageRepo) DeleteIfOrphaned(ctx context.Context, id int64) (bool, error) {
	res, err := r.db.ExecContext(ctx, `DELETE FROM images WHERE id = ? AND orphaned_at IS NOT NULL`, id)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

func (r *ImageRepo) KnownFileNames(ctx context.Context) (map[string]struct{}, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT file_name FROM images`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]struct{})
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		out[n] = struct{}{}
	}
	return out, rows.Err()
}

func (r *ImageRepo) Adopt(ctx context.Context, fileName string) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	res, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO images (file_name) VALUES (?)`, fileName)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil
	}
	var imageID int64
	if err := tx.QueryRowContext(ctx, `SELECT id FROM images WHERE file_name = ?`, fileName).Scan(&imageID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT OR IGNORE INTO image_refs (image_id, page_id)
		 SELECT ?, id FROM pages WHERE instr(content, ?) > 0`, imageID, "/uploads/"+fileName); err != nil {
		return err
	}
	if err := refreshOrphanMarks(ctx, tx, []int64{imageID}); err != nil {
		return err
	}
	return tx.Commit()
}

func imagesOfPage(ctx context.Context, tx *sql.Tx, pageID int64) ([]int64, error) {
	rows, err := tx.QueryContext(ctx, `SELECT image_id FROM image_refs WHERE page_id = ?`, pageID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func refreshOrphanMarks(ctx context.Context, tx *sql.Tx, imageIDs []int64) error {
	for _, id := range imageIDs {
		if _, err := tx.ExecContext(ctx, `
			UPDATE images
			SET orphaned_at = CASE
				WHEN EXISTS (SELECT 1 FROM image_refs WHERE image_id = images.id) THEN NULL
				ELSE COALESCE(orphaned_at, CURRENT_TIMESTAMP)
			END
			WHERE id = ?`, id); err != nil {
			return err
		}
	}
	return nil
}
