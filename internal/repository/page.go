package repository

import (
	"context"
	"database/sql"
	"time"

	"minipress/internal/models"
)

type PageRepo struct {
	db *sql.DB
}

func NewPageRepo(db *sql.DB) *PageRepo { return &PageRepo{db: db} }

const pageColumns = `id, public_id, edit_token, title, content, COALESCE(author, ''), COALESCE(is_published, 0),
	CAST(strftime('%s', created_at) AS INTEGER), CAST(strftime('%s', updated_at) AS INTEGER)`

func scanPage(row interface{ Scan(...any) error }) (*models.Page, error) {
	var (
		p                    models.Page
		createdAt, updatedAt int64
	)
	if err := row.Scan(&p.ID, &p.PublicID, &p.EditToken, &p.Title, &p.Content, &p.Author, &p.IsPublished, &createdAt, &updatedAt); err != nil {
		return nil, notFound(err)
	}
	p.CreatedAt = time.Unix(createdAt, 0).UTC()
	p.UpdatedAt = time.Unix(updatedAt, 0).UTC()
	return &p, nil
}

func (r *PageRepo) Create(ctx context.Context, p *models.Page) error {
	res, err := r.db.ExecContext(ctx,
		`INSERT INTO pages (public_id, edit_token, title, content, author, is_published) VALUES (?, ?, ?, ?, ?, ?)`,
		p.PublicID, p.EditToken, p.Title, p.Content, p.Author, p.IsPublished)
	if err != nil {
		return err
	}
	p.ID, err = res.LastInsertId()
	return err
}

func (r *PageRepo) GetByPublicID(ctx context.Context, id string) (*models.Page, error) {
	return scanPage(r.db.QueryRowContext(ctx, `SELECT `+pageColumns+` FROM pages WHERE public_id = ?`, id))
}

func (r *PageRepo) GetByEditToken(ctx context.Context, token string) (*models.Page, error) {
	return scanPage(r.db.QueryRowContext(ctx, `SELECT `+pageColumns+` FROM pages WHERE edit_token = ?`, token))
}

func (r *PageRepo) Update(ctx context.Context, p *models.Page) error {
	res, err := r.db.ExecContext(ctx,
		`UPDATE pages SET title = ?, content = ?, author = ?, is_published = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`,
		p.Title, p.Content, p.Author, p.IsPublished, p.ID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *PageRepo) Delete(ctx context.Context, token string) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var id int64
	if err := tx.QueryRowContext(ctx, `SELECT id FROM pages WHERE edit_token = ?`, token).Scan(&id); err != nil {
		return notFound(err)
	}
	if err := deletePages(ctx, tx, []int64{id}); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *PageRepo) DeleteEmptyDrafts(ctx context.Context, createdBefore time.Time) (int, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	const blank = `' ' || char(9) || char(10) || char(13)`
	rows, err := tx.QueryContext(ctx, `
		SELECT id FROM pages
		WHERE COALESCE(is_published, 0) = 0
		  AND created_at <= ?
		  AND TRIM(title, `+blank+`) = ''
		  AND TRIM(content, `+blank+`) = ''`, formatTime(createdBefore))
	if err != nil {
		return 0, err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	if len(ids) == 0 {
		return 0, nil
	}
	if err := deletePages(ctx, tx, ids); err != nil {
		return 0, err
	}
	return len(ids), tx.Commit()
}

func deletePages(ctx context.Context, tx *sql.Tx, ids []int64) error {
	for _, id := range ids {
		affected, err := imagesOfPage(ctx, tx, id)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM image_refs WHERE page_id = ?`, id); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM pages WHERE id = ?`, id); err != nil {
			return err
		}
		if err := refreshOrphanMarks(ctx, tx, affected); err != nil {
			return err
		}
	}
	return nil
}
