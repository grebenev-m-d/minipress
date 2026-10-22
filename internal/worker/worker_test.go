package worker

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"image"
	"image/png"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"minipress/internal/models"
	"minipress/internal/repository"
	"minipress/internal/service"
	"minipress/internal/testutil"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

type fakeClock struct {
	mu   sync.Mutex
	now  time.Time
	wait chan time.Time
	asks chan time.Duration
}

func newFakeClock(now time.Time) *fakeClock {
	return &fakeClock{now: now, wait: make(chan time.Time), asks: make(chan time.Duration, 10)}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) After(d time.Duration) <-chan time.Time {
	c.asks <- d
	return c.wait
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

func TestNextRun(t *testing.T) {
	loc := time.FixedZone("MSK", 3*3600)
	at := func(day, h, m int) time.Time { return time.Date(2026, 10, day, h, m, 0, 0, loc) }

	cases := []struct {
		name string
		now  time.Time
		want time.Time
	}{
		{"before the run time: today", at(21, 10, 0), at(21, 23, 30)},
		{"after the run time: tomorrow", at(21, 23, 45), at(22, 23, 30)},
		{"exactly at the run time: tomorrow", at(21, 23, 30), at(22, 23, 30)},
		{"right after midnight", at(22, 0, 1), at(22, 23, 30)},
		{"month boundary", time.Date(2026, 10, 31, 23, 59, 0, 0, loc), time.Date(2026, 11, 1, 23, 30, 0, 0, loc)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := NextRun(c.now, 23, 30); !got.Equal(c.want) {
				t.Errorf("got %v, expected %v", got, c.want)
			}
		})
	}
}

func TestNextRunAcrossDST(t *testing.T) {
	loc, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		t.Skip("no timezone database")
	}
	now := time.Date(2026, 10, 24, 23, 45, 0, 0, loc)
	got := NextRun(now, 23, 30)
	want := time.Date(2026, 10, 25, 23, 30, 0, 0, loc)
	if !got.Equal(want) {
		t.Errorf("got %v, expected %v", got, want)
	}
}

func TestSchedulerRunsJobsInOrderEachDay(t *testing.T) {
	loc := time.FixedZone("T", 0)
	clock := newFakeClock(time.Date(2026, 10, 21, 12, 0, 0, 0, loc))

	var mu sync.Mutex
	var order []string
	done := make(chan struct{}, 10)
	job := func(name string) Job {
		return Job{Name: name, Run: func(context.Context) (string, error) {
			mu.Lock()
			order = append(order, name)
			mu.Unlock()
			done <- struct{}{}
			return "ok", nil
		}}
	}

	s := &Scheduler{Hour: 23, Minute: 30, Clock: clock, Log: quiet, Jobs: []Job{job("drafts"), job("images")}}
	ctx, cancel := context.WithCancel(context.Background())
	finished := make(chan struct{})
	go func() { s.Run(ctx); close(finished) }()

	if d := <-clock.asks; d != 11*time.Hour+30*time.Minute {
		t.Errorf("first wait: %v, expected 11h30m", d)
	}
	clock.advance(11*time.Hour + 30*time.Minute)
	clock.wait <- clock.Now()
	<-done
	<-done

	if d := <-clock.asks; d != 24*time.Hour {
		t.Errorf("second wait: %v, expected 24h", d)
	}

	cancel()
	select {
	case <-finished:
	case <-time.After(2 * time.Second):
		t.Fatal("scheduler did not stop on context cancel")
	}

	mu.Lock()
	defer mu.Unlock()
	if strings.Join(order, ",") != "drafts,images" {
		t.Errorf("run order: %v", order)
	}
}

func TestSchedulerSurvivesFailingJobs(t *testing.T) {
	var ran []string
	s := &Scheduler{Log: quiet, Jobs: []Job{
		{Name: "error", Run: func(context.Context) (string, error) {
			ran = append(ran, "error")
			return "", errors.New("broke")
		}},
		{Name: "panic", Run: func(context.Context) (string, error) { ran = append(ran, "panic"); panic("boom") }},
		{Name: "ok", Run: func(context.Context) (string, error) { ran = append(ran, "ok"); return "", nil }},
	}}
	s.RunAll(context.Background(), quiet)
	if strings.Join(ran, ",") != "error,panic,ok" {
		t.Errorf("failing jobs must not block the others: %v", ran)
	}
}

func TestSchedulerStopsBetweenJobsOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var ran int
	s := &Scheduler{Log: quiet, Jobs: []Job{
		{Name: "first", Run: func(context.Context) (string, error) { ran++; cancel(); return "", nil }},
		{Name: "second", Run: func(context.Context) (string, error) { ran++; return "", nil }},
	}}
	s.RunAll(ctx, quiet)
	if ran != 1 {
		t.Errorf("the second job must not start after cancel, ran %d", ran)
	}
}

type env struct {
	db     *sql.DB
	pages  *repository.PageRepo
	images *repository.ImageRepo
	store  *service.ImageStore
	clock  *fakeClock
}

func newEnv(t *testing.T) *env {
	db := testutil.NewDB(t)
	store, err := service.NewImageStore(filepath.Join(t.TempDir(), "uploads"))
	if err != nil {
		t.Fatal(err)
	}
	return &env{db: db, pages: repository.NewPageRepo(db), images: repository.NewImageRepo(db), store: store,
		clock: newFakeClock(time.Now())}
}

func (e *env) writeFile(t *testing.T, name string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(e.store.Dir(), name), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func (e *env) fileExists(name string) bool {
	_, err := os.Stat(filepath.Join(e.store.Dir(), name))
	return err == nil
}

func (e *env) uploadReal(t *testing.T) string {
	t.Helper()
	var buf bytes.Buffer
	png.Encode(&buf, image.NewGray(image.Rect(0, 0, 4, 4)))
	name, err := e.store.Save(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	if err := e.images.Register(context.Background(), name); err != nil {
		t.Fatal(err)
	}
	return name
}

func (e *env) age(t *testing.T, name string, by time.Duration) {
	t.Helper()
	ts := time.Now().Add(-by).UTC().Format("2006-01-02 15:04:05")
	if _, err := e.db.Exec(`UPDATE images SET orphaned_at = ? WHERE file_name = ? AND orphaned_at IS NOT NULL`, ts, name); err != nil {
		t.Fatal(err)
	}
}

func TestDraftCleanupJob(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	mk := func(tok string, content string, age time.Duration) {
		p := &models.Page{PublicID: "p" + tok, EditToken: tok, Content: content}
		if err := e.pages.Create(ctx, p); err != nil {
			t.Fatal(err)
		}
		e.db.Exec(`UPDATE pages SET created_at = ? WHERE id = ?`, time.Now().Add(-age).UTC().Format("2006-01-02 15:04:05"), p.ID)
	}
	mk("stale", "", 3*time.Hour)
	mk("fresh", "", 10*time.Minute)
	mk("written", "text", 3*time.Hour)

	job := NewDraftCleanupJob(e.pages, time.Hour, RealClock)
	summary, err := job.Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(summary, "1") {
		t.Errorf("summary must report 1 deletion: %q", summary)
	}
	for tok, want := range map[string]bool{"stale": false, "fresh": true, "written": true} {
		_, err := e.pages.GetByEditToken(ctx, tok)
		if got := err == nil; got != want {
			t.Errorf("%s: exists=%v, expected %v", tok, got, want)
		}
	}
}

func TestImageCleanupRemovesOnlyOldOrphans(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	job := NewImageCleanupJob(e.images, e.store, 24*time.Hour, RealClock, quiet)

	used := e.uploadReal(t)
	oldOrphan := e.uploadReal(t)
	freshOrphan := e.uploadReal(t)

	page := &models.Page{PublicID: "p", EditToken: "t", Content: "![](/uploads/" + used + ")"}
	e.pages.Create(ctx, page)
	e.images.SyncPageRefs(ctx, page.ID, service.ExtractImageNames(page.Content))
	e.age(t, oldOrphan, 48*time.Hour)

	summary, err := job.Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(summary, "removed files: 1") {
		t.Errorf("summary: %q", summary)
	}
	if e.fileExists(oldOrphan) {
		t.Error("an old orphan must be removed from disk")
	}
	if !e.fileExists(used) {
		t.Error("a used image was deleted!")
	}
	if !e.fileExists(freshOrphan) {
		t.Error("a fresh orphan must not be deleted before the grace period ends")
	}
	known, _ := e.images.KnownFileNames(ctx)
	if _, ok := known[oldOrphan]; ok {
		t.Error("the row of a removed file must leave the DB")
	}
}

func TestImageReturnedToTextBeforeCleanupSurvives(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	name := e.uploadReal(t)
	page := &models.Page{PublicID: "p", EditToken: "t"}
	e.pages.Create(ctx, page)

	e.images.SyncPageRefs(ctx, page.ID, []string{name})
	e.images.SyncPageRefs(ctx, page.ID, nil)
	e.age(t, name, 48*time.Hour)
	e.images.SyncPageRefs(ctx, page.ID, []string{name})

	if _, err := NewImageCleanupJob(e.images, e.store, 24*time.Hour, RealClock, quiet).Run(ctx); err != nil {
		t.Fatal(err)
	}
	if !e.fileExists(name) {
		t.Error("the image is back in the text, it must not be deleted")
	}
}

func TestImageOfDeletedPageIsCleanedAfterGrace(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	name := e.uploadReal(t)
	page := &models.Page{PublicID: "p", EditToken: "t", Content: "![](/uploads/" + name + ")"}
	e.pages.Create(ctx, page)
	e.images.SyncPageRefs(ctx, page.ID, []string{name})

	if err := e.pages.Delete(ctx, "t"); err != nil {
		t.Fatal(err)
	}
	job := NewImageCleanupJob(e.images, e.store, 24*time.Hour, RealClock, quiet)

	job.Run(ctx)
	if !e.fileExists(name) {
		t.Fatal("too early to delete: grace period is still running")
	}
	e.age(t, name, 25*time.Hour)
	job.Run(ctx)
	if e.fileExists(name) {
		t.Error("the file of a deleted page must disappear after the grace period")
	}
}

func TestImageCleanupToleratesMissingFile(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.images.Register(ctx, "ghost.jpg")
	e.age(t, "ghost.jpg", 48*time.Hour)

	if _, err := NewImageCleanupJob(e.images, e.store, 24*time.Hour, RealClock, quiet).Run(ctx); err != nil {
		t.Fatalf("a missing file must not break the job: %v", err)
	}
	known, _ := e.images.KnownFileNames(ctx)
	if len(known) != 0 {
		t.Errorf("the row must be removed: %v", known)
	}
}

func TestImageCleanupHandlesMoreThanOneBatch(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	total := imageBatch + 25
	for i := 0; i < total; i++ {
		name := "f" + strings.Repeat("x", 3) + string(rune('a'+i%26)) + string(rune('a'+i/26%26)) + string(rune('a'+i/676)) + ".jpg"
		e.writeFile(t, name)
		e.images.Register(ctx, name)
	}
	e.db.Exec(`UPDATE images SET orphaned_at = '2000-01-01 00:00:00'`)

	summary, err := NewImageCleanupJob(e.images, e.store, time.Hour, RealClock, quiet).Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if left, _ := e.store.List(); len(left) != 0 {
		t.Errorf("files left: %d (summary %q)", len(left), summary)
	}
}

func TestReconcileAdoptsStrayFiles(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()

	e.writeFile(t, "legacy.png")
	e.writeFile(t, "stray.jpg")
	e.writeFile(t, ".keep")
	page := &models.Page{PublicID: "p", EditToken: "t", Content: "![](/uploads/legacy.png)"}
	e.pages.Create(ctx, page)

	n, err := Reconcile(ctx, e.images, e.store)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("untracked found %d, expected 2", n)
	}

	job := NewImageCleanupJob(e.images, e.store, 24*time.Hour, RealClock, quiet)
	job.Run(ctx)
	if !e.fileExists("legacy.png") || !e.fileExists("stray.jpg") {
		t.Fatal("grace period has not passed, nothing may be deleted")
	}

	e.age(t, "legacy.png", 48*time.Hour)
	e.age(t, "stray.jpg", 48*time.Hour)
	job.Run(ctx)
	if !e.fileExists("legacy.png") {
		t.Error("the legacy image is used in a page, it must not be deleted")
	}
	if e.fileExists("stray.jpg") {
		t.Error("an unused stray file must be deleted")
	}

	if n, _ := Reconcile(ctx, e.images, e.store); n != 0 {
		t.Errorf("second reconcile found %d files", n)
	}
}

func TestImageCleanupStopsOnCancelledContext(t *testing.T) {
	e := newEnv(t)
	e.images.Register(context.Background(), "a.jpg")
	e.age(t, "a.jpg", 48*time.Hour)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := NewImageCleanupJob(e.images, e.store, time.Hour, RealClock, quiet).Run(ctx); err == nil {
		t.Error("the job must return an error with a cancelled context")
	}
}
