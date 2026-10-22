package repository

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"minipress/internal/models"
	"minipress/internal/testutil"
)

var ctx = context.Background()

type env struct {
	db     *sql.DB
	pages  *PageRepo
	images *ImageRepo
}

func newEnv(t *testing.T) *env {
	db := testutil.NewDB(t)
	return &env{db: db, pages: NewPageRepo(db), images: NewImageRepo(db)}
}

func (e *env) newPage(t *testing.T, token, title, content string, published bool) *models.Page {
	t.Helper()
	p := &models.Page{PublicID: "pub-" + token, EditToken: token, Title: title, Content: content, IsPublished: published}
	if err := e.pages.Create(ctx, p); err != nil {
		t.Fatal(err)
	}
	return p
}

func (e *env) backdate(t *testing.T, pageID int64, age time.Duration) {
	t.Helper()
	ts := time.Now().Add(-age).UTC().Format(sqlTime)
	if _, err := e.db.Exec(`UPDATE pages SET created_at = ? WHERE id = ?`, ts, pageID); err != nil {
		t.Fatal(err)
	}
}

func (e *env) orphanedAt(t *testing.T, name string) (orphaned bool, exists bool) {
	t.Helper()
	var v sql.NullString
	err := e.db.QueryRow(`SELECT orphaned_at FROM images WHERE file_name = ?`, name).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return false, false
	}
	if err != nil {
		t.Fatal(err)
	}
	return v.Valid, true
}

func (e *env) pageCount(t *testing.T) int {
	var n int
	if err := e.db.QueryRow(`SELECT COUNT(*) FROM pages`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestPageCreateAndGet(t *testing.T) {
	e := newEnv(t)
	p := e.newPage(t, "tok", "Title", "text", true)
	if p.ID == 0 {
		t.Fatal("Create must set ID")
	}

	byToken, err := e.pages.GetByEditToken(ctx, "tok")
	if err != nil {
		t.Fatal(err)
	}
	byPublic, err := e.pages.GetByPublicID(ctx, "pub-tok")
	if err != nil {
		t.Fatal(err)
	}
	if byToken.ID != byPublic.ID || byToken.Title != "Title" || !byToken.IsPublished {
		t.Errorf("unexpected page: %+v", byToken)
	}
	if time.Since(byToken.CreatedAt) > time.Minute || byToken.CreatedAt.IsZero() {
		t.Errorf("CreatedAt not parsed: %v", byToken.CreatedAt)
	}
}

func TestPageNotFound(t *testing.T) {
	e := newEnv(t)
	if _, err := e.pages.GetByEditToken(ctx, "nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetByEditToken: expected ErrNotFound, got %v", err)
	}
	if _, err := e.pages.GetByPublicID(ctx, "nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetByPublicID: expected ErrNotFound, got %v", err)
	}
	if err := e.pages.Delete(ctx, "nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Delete: expected ErrNotFound, got %v", err)
	}
	if err := e.pages.Update(ctx, &models.Page{ID: 999}); !errors.Is(err, ErrNotFound) {
		t.Errorf("Update: expected ErrNotFound, got %v", err)
	}
}

func TestPageUpdate(t *testing.T) {
	e := newEnv(t)
	p := e.newPage(t, "tok", "", "", false)
	p.Title, p.Author, p.Content, p.IsPublished = "New", "Author", "Text", true
	if err := e.pages.Update(ctx, p); err != nil {
		t.Fatal(err)
	}
	got, _ := e.pages.GetByEditToken(ctx, "tok")
	if got.Title != "New" || got.Author != "Author" || got.Content != "Text" || !got.IsPublished {
		t.Errorf("update was not persisted: %+v", got)
	}
}

func TestDeleteEmptyDrafts(t *testing.T) {
	e := newEnv(t)

	oldEmpty := e.newPage(t, "old-empty", "", "", false)
	oldBlank := e.newPage(t, "old-blank", "  ", "\n\t \r\n", false)
	oldTitleOnly := e.newPage(t, "old-title", "Has title", "", false)
	oldContentOnly := e.newPage(t, "old-content", "", "Has text", false)
	oldPublishedEmpty := e.newPage(t, "old-pub", "", "", true)
	freshEmpty := e.newPage(t, "fresh", "", "", false)

	for _, p := range []*models.Page{oldEmpty, oldBlank, oldTitleOnly, oldContentOnly, oldPublishedEmpty} {
		e.backdate(t, p.ID, 2*time.Hour)
	}

	n, err := e.pages.DeleteEmptyDrafts(ctx, time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("deleted %d, expected 2 (old-empty and old-blank)", n)
	}

	mustExist := []string{"old-title", "old-content", "old-pub", "fresh"}
	for _, tok := range mustExist {
		if _, err := e.pages.GetByEditToken(ctx, tok); err != nil {
			t.Errorf("page %s must not be deleted: %v", tok, err)
		}
	}
	for _, tok := range []string{"old-empty", "old-blank"} {
		if _, err := e.pages.GetByEditToken(ctx, tok); !errors.Is(err, ErrNotFound) {
			t.Errorf("page %s must be deleted", tok)
		}
	}
	_ = freshEmpty
}

func TestDeleteEmptyDraftsBoundary(t *testing.T) {
	e := newEnv(t)
	p := e.newPage(t, "edge", "", "", false)
	e.backdate(t, p.ID, 59*time.Minute)

	if n, _ := e.pages.DeleteEmptyDrafts(ctx, time.Now().Add(-time.Hour)); n != 0 {
		t.Errorf("draft younger than one hour was deleted")
	}
	e.backdate(t, p.ID, 61*time.Minute)
	if n, _ := e.pages.DeleteEmptyDrafts(ctx, time.Now().Add(-time.Hour)); n != 1 {
		t.Errorf("draft older than one hour was not deleted")
	}
}

func TestDeleteEmptyDraftsNothingToDo(t *testing.T) {
	e := newEnv(t)
	if n, err := e.pages.DeleteEmptyDrafts(ctx, time.Now()); err != nil || n != 0 {
		t.Errorf("empty database: n=%d err=%v", n, err)
	}
}

func TestImageRegisteredAsOrphanUntilReferenced(t *testing.T) {
	e := newEnv(t)
	p := e.newPage(t, "tok", "T", "C", false)

	if err := e.images.Register(ctx, "a.jpg"); err != nil {
		t.Fatal(err)
	}
	if orphan, _ := e.orphanedAt(t, "a.jpg"); !orphan {
		t.Fatal("fresh image without refs must be orphaned")
	}

	if err := e.images.SyncPageRefs(ctx, p.ID, []string{"a.jpg"}); err != nil {
		t.Fatal(err)
	}
	if orphan, _ := e.orphanedAt(t, "a.jpg"); orphan {
		t.Error("mark must be cleared once a page references the image")
	}

	if err := e.images.SyncPageRefs(ctx, p.ID, nil); err != nil {
		t.Fatal(err)
	}
	if orphan, _ := e.orphanedAt(t, "a.jpg"); !orphan {
		t.Error("image must become orphaned after the reference is removed")
	}
}

func TestSyncIgnoresUnknownFiles(t *testing.T) {
	e := newEnv(t)
	p := e.newPage(t, "tok", "", "", false)
	if err := e.images.SyncPageRefs(ctx, p.ID, []string{"ghost.jpg"}); err != nil {
		t.Fatal(err)
	}
	if _, exists := e.orphanedAt(t, "ghost.jpg"); exists {
		t.Error("unregistered name must not create a row")
	}
}

func TestOrphanMarkKeepsOriginalTimestamp(t *testing.T) {
	e := newEnv(t)
	p := e.newPage(t, "tok", "", "", false)
	e.images.Register(ctx, "a.jpg")
	e.images.SyncPageRefs(ctx, p.ID, []string{"a.jpg"})
	e.images.SyncPageRefs(ctx, p.ID, nil)

	old := "2000-01-01 00:00:00"
	e.db.Exec(`UPDATE images SET orphaned_at = ? WHERE file_name = 'a.jpg'`, old)

	e.images.SyncPageRefs(ctx, p.ID, nil)
	e.images.SyncPageRefs(ctx, p.ID, []string{"ghost.jpg"})
	var got string
	e.db.QueryRow(`SELECT strftime('%Y-%m-%d %H:%M:%S', orphaned_at) FROM images WHERE file_name = 'a.jpg'`).Scan(&got)
	if got != old {
		t.Errorf("mark was overwritten: %s", got)
	}
}

func TestImageUsedByTwoPagesSurvivesOneRemoval(t *testing.T) {
	e := newEnv(t)
	a := e.newPage(t, "a", "A", "x", false)
	b := e.newPage(t, "b", "B", "x", false)
	e.images.Register(ctx, "shared.jpg")
	e.images.SyncPageRefs(ctx, a.ID, []string{"shared.jpg"})
	e.images.SyncPageRefs(ctx, b.ID, []string{"shared.jpg"})

	e.images.SyncPageRefs(ctx, a.ID, nil)
	if orphan, _ := e.orphanedAt(t, "shared.jpg"); orphan {
		t.Fatal("image is still used by the second page")
	}
	if err := e.pages.Delete(ctx, "b"); err != nil {
		t.Fatal(err)
	}
	if orphan, _ := e.orphanedAt(t, "shared.jpg"); !orphan {
		t.Error("image must become orphaned after the last page is deleted")
	}
}

func TestDeletePageOrphansItsImages(t *testing.T) {
	e := newEnv(t)
	p := e.newPage(t, "tok", "T", "![](/uploads/a.jpg)", true)
	e.images.Register(ctx, "a.jpg")
	e.images.SyncPageRefs(ctx, p.ID, []string{"a.jpg"})

	if err := e.pages.Delete(ctx, "tok"); err != nil {
		t.Fatal(err)
	}
	if e.pageCount(t) != 0 {
		t.Error("page was not deleted")
	}
	orphan, exists := e.orphanedAt(t, "a.jpg")
	if !exists || !orphan {
		t.Errorf("image of a deleted page must stay in the DB as an orphan (exists=%v orphan=%v)", exists, orphan)
	}
}

func TestListOrphanedBeforeRespectsGrace(t *testing.T) {
	e := newEnv(t)
	e.images.Register(ctx, "old.jpg")
	e.images.Register(ctx, "new.jpg")
	e.db.Exec(`UPDATE images SET orphaned_at = ? WHERE file_name = 'old.jpg'`,
		time.Now().Add(-48*time.Hour).UTC().Format(sqlTime))

	got, err := e.images.ListOrphanedBefore(ctx, time.Now().Add(-24*time.Hour), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].FileName != "old.jpg" {
		t.Errorf("only old.jpg must be listed: %+v", got)
	}
}

func TestDeleteIfOrphanedRefusesWhenReferencedAgain(t *testing.T) {
	e := newEnv(t)
	p := e.newPage(t, "tok", "", "", false)
	e.images.Register(ctx, "a.jpg")
	e.db.Exec(`UPDATE images SET orphaned_at = '2000-01-01 00:00:00'`)
	batch, _ := e.images.ListOrphanedBefore(ctx, time.Now(), 10)

	e.images.SyncPageRefs(ctx, p.ID, []string{"a.jpg"})

	ok, err := e.images.DeleteIfOrphaned(ctx, batch[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Error("a used image must not be deleted")
	}
	if _, exists := e.orphanedAt(t, "a.jpg"); !exists {
		t.Error("row is gone")
	}
}

func TestAdoptLinksToPagesMentioningFile(t *testing.T) {
	e := newEnv(t)
	used := e.newPage(t, "u", "T", "here ![](/uploads/used.jpg) it is", false)
	e.newPage(t, "o", "T", "nothing", false)

	if err := e.images.Adopt(ctx, "used.jpg"); err != nil {
		t.Fatal(err)
	}
	if err := e.images.Adopt(ctx, "stray.jpg"); err != nil {
		t.Fatal(err)
	}
	if orphan, _ := e.orphanedAt(t, "used.jpg"); orphan {
		t.Error("a file mentioned in a page must not be orphaned")
	}
	if orphan, _ := e.orphanedAt(t, "stray.jpg"); !orphan {
		t.Error("a file mentioned nowhere must be orphaned")
	}
	var n int
	e.db.QueryRow(`SELECT COUNT(*) FROM image_refs WHERE page_id = ?`, used.ID).Scan(&n)
	if n != 1 {
		t.Errorf("refs for page: %d", n)
	}

	if err := e.images.Adopt(ctx, "used.jpg"); err != nil {
		t.Errorf("second Adopt: %v", err)
	}
}

func TestKnownFileNames(t *testing.T) {
	e := newEnv(t)
	e.images.Register(ctx, "a.jpg")
	e.images.Register(ctx, "b.jpg")
	got, err := e.images.KnownFileNames(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Errorf("expected 2 names: %v", got)
	}
}
