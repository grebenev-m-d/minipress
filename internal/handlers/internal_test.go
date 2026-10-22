package handlers

import (
	"bytes"
	"image"
	"image/png"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"minipress/internal/repository"
	"minipress/internal/service"
	"minipress/internal/testutil"
	"minipress/web"
)

func TestRateLimiterTokenBucket(t *testing.T) {
	now := time.Now()
	l := newRateLimiter(1, 3)
	l.now = func() time.Time { return now }

	for i := 0; i < 3; i++ {
		if ok, _ := l.allow("a"); !ok {
			t.Fatalf("request %d within the burst must pass", i+1)
		}
	}
	ok, wait := l.allow("a")
	if ok {
		t.Fatal("fourth request in a row must be rejected")
	}
	if wait <= 0 || wait > time.Second {
		t.Errorf("Retry-After hint: %v", wait)
	}
	if ok, _ := l.allow("b"); !ok {
		t.Error("another IP must not be affected by the first one's limit")
	}

	now = now.Add(1100 * time.Millisecond)
	if ok, _ := l.allow("a"); !ok {
		t.Error("a token must be restored after a second")
	}
	if ok, _ := l.allow("a"); ok {
		t.Error("but only one")
	}
}

func TestRateLimiterForgetsIdleClients(t *testing.T) {
	now := time.Now()
	l := newRateLimiter(1, 1)
	l.now = func() time.Time { return now }
	for _, k := range []string{"a", "b", "c"} {
		l.allow(k)
	}
	now = now.Add(11 * time.Minute)
	l.allow("d")
	if len(l.buckets) != 1 {
		t.Errorf("all but the new one must expire: %d buckets", len(l.buckets))
	}
}

func TestClientIP(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "203.0.113.7:5555"
	req.Header.Set("X-Forwarded-For", "6.6.6.6, 198.51.100.2")

	direct := &App{}
	if got := direct.clientIP(req); got != "203.0.113.7" {
		t.Errorf("the header must not be trusted without a proxy: %s", got)
	}
	proxied := &App{trustProxy: true}
	if got := proxied.clientIP(req); got != "198.51.100.2" {
		t.Errorf("behind a proxy the last element is used (added by our proxy): %s", got)
	}
}

func newTestApp(t *testing.T) (*App, string) {
	t.Helper()
	db := testutil.NewDB(t)
	store, err := service.NewImageStore(filepath.Join(t.TempDir(), "u"))
	if err != nil {
		t.Fatal(err)
	}
	pages := repository.NewPageRepo(db)
	a, err := New(pages, repository.NewImageRepo(db), store, web.Templates(), slog.New(slog.NewTextHandler(io.Discard, nil)), Options{})
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	a.handleCreate(w, httptest.NewRequest("POST", "/create", nil))
	return a, w.Header().Get("Location")[len("/edit/"):]
}

func TestUploadReturns503WhenImageSlotsBusy(t *testing.T) {
	a, token := newTestApp(t)
	a.queueWait = 20 * time.Millisecond
	for i := 0; i < cap(a.imageSem); i++ {
		a.imageSem <- struct{}{}
	}

	var img bytes.Buffer
	png.Encode(&img, image.NewGray(image.Rect(0, 0, 4, 4)))
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, _ := mw.CreateFormFile("image", "a.png")
	fw.Write(img.Bytes())
	mw.Close()

	req := httptest.NewRequest("POST", "/upload/"+token, &body)
	req.SetPathValue("token", token)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rec := httptest.NewRecorder()
	a.handleUpload(rec, req)

	if rec.Code != http.StatusServiceUnavailable || rec.Header().Get("Retry-After") == "" {
		t.Errorf("status %d, Retry-After=%q; expected 503 with Retry-After", rec.Code, rec.Header().Get("Retry-After"))
	}
}
