package handlers_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"image"
	"image/png"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"minipress/internal/handlers"
	"minipress/internal/repository"
	"minipress/internal/service"
	"minipress/internal/testutil"
	"minipress/web"
)

type app struct {
	db     *sql.DB
	srv    *httptest.Server
	pages  *repository.PageRepo
	images *repository.ImageRepo
	dir    string
	client *http.Client
}

func newApp(t *testing.T) *app {
	t.Helper()
	db := testutil.NewDB(t)
	dir := filepath.Join(t.TempDir(), "uploads")
	store, err := service.NewImageStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	a := &app{db: db, pages: repository.NewPageRepo(db), images: repository.NewImageRepo(db), dir: dir}
	h, err := handlers.New(a.pages, a.images, store, web.Templates(), slog.New(slog.NewTextHandler(io.Discard, nil)), handlers.Options{})
	if err != nil {
		t.Fatal(err)
	}
	a.srv = httptest.NewServer(h.Routes(web.Static()))
	t.Cleanup(a.srv.Close)
	a.client = &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return a
}

func (a *app) do(t *testing.T, method, path string, body io.Reader, contentType string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(method, a.srv.URL+path, body)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := a.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func (a *app) get(t *testing.T, path string) *http.Response { return a.do(t, "GET", path, nil, "") }

func (a *app) post(t *testing.T, path string, form url.Values) *http.Response {
	return a.do(t, "POST", path, strings.NewReader(form.Encode()), "application/x-www-form-urlencoded")
}

func bodyOf(t *testing.T, r *http.Response) string {
	t.Helper()
	b, _ := io.ReadAll(r.Body)
	return string(b)
}

func (a *app) createPage(t *testing.T) (token string) {
	t.Helper()
	resp := a.post(t, "/create", nil)
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("/create: status %d", resp.StatusCode)
	}
	loc := resp.Header.Get("Location")
	if !strings.HasPrefix(loc, "/edit/") {
		t.Fatalf("unexpected redirect %q", loc)
	}
	return strings.TrimPrefix(loc, "/edit/")
}

func (a *app) save(t *testing.T, token string, v url.Values) (int, string) {
	t.Helper()
	resp := a.post(t, "/update/"+token, v)
	return resp.StatusCode, bodyOf(t, resp)
}

func (a *app) preview(t *testing.T, token string, v url.Values) (int, string) {
	t.Helper()
	resp := a.post(t, "/preview/"+token, v)
	return resp.StatusCode, bodyOf(t, resp)
}

func pngBytes(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 10, 10)))
	return buf.Bytes()
}

func (a *app) upload(t *testing.T, token, field string, data []byte) (int, map[string]string) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, _ := mw.CreateFormFile(field, "pic.png")
	fw.Write(data)
	mw.Close()
	resp := a.do(t, "POST", "/upload/"+token, &buf, mw.FormDataContentType())
	var out map[string]string
	json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func TestIndex(t *testing.T) {
	a := newApp(t)
	resp := a.get(t, "/")
	if resp.StatusCode != 200 || !strings.Contains(bodyOf(t, resp), "Новая статья") {
		t.Errorf("index: status %d", resp.StatusCode)
	}
}

func TestFullArticleLifecycle(t *testing.T) {
	a := newApp(t)
	token := a.createPage(t)

	if resp := a.get(t, "/edit/"+token); resp.StatusCode != 200 {
		t.Fatalf("/edit: %d", resp.StatusCode)
	}

	page, _ := a.pages.GetByEditToken(context.Background(), token)
	if len(token) != 32 {
		t.Errorf("edit token must be 32 chars, got %d", len(token))
	}

	code, out := a.save(t, token, url.Values{"title": {"Hello"}, "author": {"Anna"}, "content": {"**world**"}, "is_published": {"false"}})
	if code != 200 || !strings.Contains(out, `id="draft-note"`) || strings.Contains(out, "hidden") {
		t.Fatalf("saving a draft: %d %s", code, out)
	}
	if resp := a.get(t, "/p/"+page.PublicID); resp.StatusCode != 404 {
		t.Errorf("a draft must return 404, got %d", resp.StatusCode)
	}

	code, out = a.save(t, token, url.Values{"title": {"Hello"}, "author": {"Anna"}, "content": {"**world**"}, "is_published": {"true"}})
	if code != 200 || !strings.Contains(out, "hidden") {
		t.Fatalf("publishing: %d %s", code, out)
	}
	resp := a.get(t, "/p/"+page.PublicID)
	body := bodyOf(t, resp)
	if resp.StatusCode != 200 || !strings.Contains(body, "Hello") || !strings.Contains(body, "Anna") || !strings.Contains(body, "<strong>world</strong>") {
		t.Errorf("published article rendered incorrectly: %d\n%s", resp.StatusCode, body)
	}
	if strings.Contains(body, token) {
		t.Error("public page must not reveal the edit token")
	}

	a.save(t, token, url.Values{"title": {"Hello"}, "content": {"x"}, "is_published": {"false"}})
	if resp := a.get(t, "/p/"+page.PublicID); resp.StatusCode != 404 {
		t.Errorf("an unpublished article must return 404, got %d", resp.StatusCode)
	}

	resp = a.post(t, "/delete/"+token, nil)
	if resp.StatusCode != http.StatusSeeOther {
		t.Errorf("delete: %d", resp.StatusCode)
	}
	if resp := a.get(t, "/edit/"+token); resp.StatusCode != 404 {
		t.Errorf("editor must return 404 after deletion, got %d", resp.StatusCode)
	}
}

func TestUnknownTokensAndIDs(t *testing.T) {
	a := newApp(t)
	for _, path := range []string{"/edit/nope", "/p/nope", "/nothing-here", "/p/"} {
		if resp := a.get(t, path); resp.StatusCode != 404 {
			t.Errorf("GET %s: %d, expected 404", path, resp.StatusCode)
		}
	}
	if resp := a.post(t, "/update/nope", url.Values{"title": {"x"}}); resp.StatusCode != 404 {
		t.Errorf("update with a wrong token: %d", resp.StatusCode)
	}
	if resp := a.post(t, "/delete/nope", nil); resp.StatusCode != 404 {
		t.Errorf("delete with a wrong token: %d", resp.StatusCode)
	}
}

func TestMethodsAreEnforced(t *testing.T) {
	a := newApp(t)
	token := a.createPage(t)
	cases := []struct{ method, path string }{
		{"GET", "/create"},
		{"GET", "/delete/" + token},
		{"GET", "/update/" + token},
		{"GET", "/preview/" + token},
		{"GET", "/upload/" + token},
		{"POST", "/edit/" + token},
		{"POST", "/p/abc"},
	}
	for _, c := range cases {
		resp := a.do(t, c.method, c.path, nil, "")
		if resp.StatusCode != http.StatusMethodNotAllowed {
			t.Errorf("%s %s: %d, expected 405", c.method, c.path, resp.StatusCode)
		}
	}

	if resp := a.get(t, "/edit/"+token); resp.StatusCode != 200 {
		t.Error("article is gone")
	}
}

func TestUpdateValidation(t *testing.T) {
	a := newApp(t)
	token := a.createPage(t)

	if code, _ := a.save(t, token, url.Values{"title": {strings.Repeat("я", 201)}}); code != http.StatusUnprocessableEntity {
		t.Errorf("title too long: %d", code)
	}
	if code, _ := a.save(t, token, url.Values{"author": {strings.Repeat("я", 81)}}); code != http.StatusUnprocessableEntity {
		t.Errorf("author too long: %d", code)
	}
	if code, _ := a.save(t, token, url.Values{"content": {strings.Repeat("a", 1<<20+1)}}); code != http.StatusRequestEntityTooLarge {
		t.Errorf("text too large: %d", code)
	}

	a.save(t, token, url.Values{"title": {"  two \n words  "}})
	p, _ := a.pages.GetByEditToken(context.Background(), token)
	if p.Title != "two words" {
		t.Errorf("title not normalized: %q", p.Title)
	}
}

func TestPublicPageEscapesTitle(t *testing.T) {
	a := newApp(t)
	token := a.createPage(t)
	a.save(t, token, url.Values{"title": {`<script>alert(1)</script>`}, "author": {`<img src=x onerror=alert(1)>`}, "content": {"x"}, "is_published": {"true"}})
	p, _ := a.pages.GetByEditToken(context.Background(), token)

	body := bodyOf(t, a.get(t, "/p/"+p.PublicID))
	if strings.Contains(body, "<script>alert") || strings.Contains(body, "<img src=x") {
		t.Errorf("title or author not escaped:\n%s", body)
	}
}

func TestUploadRequiresValidToken(t *testing.T) {
	a := newApp(t)
	code, _ := a.upload(t, "wrong-token", "image", pngBytes(t))
	if code != http.StatusNotFound {
		t.Errorf("upload without a valid token: %d, expected 404", code)
	}
	if entries, _ := os.ReadDir(a.dir); len(entries) != 0 {
		t.Errorf("nothing must reach the disk: %d files", len(entries))
	}
}

func TestUploadValidation(t *testing.T) {
	a := newApp(t)
	token := a.createPage(t)

	if code, _ := a.upload(t, token, "image", []byte("<script>alert(1)</script>")); code != http.StatusUnsupportedMediaType {
		t.Errorf("not an image: %d, expected 415", code)
	}
	if code, _ := a.upload(t, token, "wrong-field", pngBytes(t)); code != http.StatusBadRequest {
		t.Errorf("missing image field: %d, expected 400", code)
	}
	big := append(pngBytes(t), bytes.Repeat([]byte{0}, 6<<20)...)
	if code, _ := a.upload(t, token, "image", big); code != http.StatusRequestEntityTooLarge {
		t.Errorf("file over 5 MB: %d, expected 413", code)
	}
}

func TestImageTrackingEndToEnd(t *testing.T) {
	a := newApp(t)
	ctx := context.Background()
	token := a.createPage(t)

	code, out := a.upload(t, token, "image", pngBytes(t))
	if code != 200 || !strings.HasPrefix(out["url"], "/uploads/") || !strings.HasSuffix(out["url"], ".jpg") {
		t.Fatalf("upload: %d %v", code, out)
	}
	name := strings.TrimPrefix(out["url"], "/uploads/")

	resp := a.get(t, out["url"])
	if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "image/jpeg" {
		t.Errorf("image unavailable: %d %s", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	if !strings.Contains(resp.Header.Get("Cache-Control"), "immutable") {
		t.Errorf("images must be cacheable: %q", resp.Header.Get("Cache-Control"))
	}

	orphaned := func() bool {
		list, err := a.images.ListOrphanedBefore(ctx, time.Now().Add(time.Hour), 10)
		if err != nil {
			t.Fatal(err)
		}
		for _, im := range list {
			if im.FileName == name {
				return true
			}
		}
		return false
	}

	if !orphaned() {
		t.Error("an image not yet used in text is an orphan")
	}
	a.save(t, token, url.Values{"content": {"![](" + out["url"] + ")"}})
	if orphaned() {
		t.Error("image is in the text, mark must be cleared")
	}
	a.save(t, token, url.Values{"content": {"image removed"}})
	if !orphaned() {
		t.Error("image removed from the text, mark must appear")
	}
}

func TestNoDirectoryListing(t *testing.T) {
	a := newApp(t)
	token := a.createPage(t)
	_, out := a.upload(t, token, "image", pngBytes(t))
	os.WriteFile(filepath.Join(a.dir, ".keep"), nil, 0o644)

	for _, path := range []string{"/uploads/", "/uploads", "/static/", "/static/css/", "/uploads/.keep"} {
		resp := a.get(t, path)
		if resp.StatusCode == 200 {
			t.Errorf("GET %s returned 200: listing or service file is exposed", path)
		}
		if b := bodyOf(t, resp); strings.Contains(b, strings.TrimPrefix(out["url"], "/uploads/")) {
			t.Errorf("GET %s exposed file names", path)
		}
	}
}

func TestUploadsServerRejectsTraversal(t *testing.T) {
	a := newApp(t)
	secret := filepath.Join(filepath.Dir(a.dir), "secret.txt")
	os.WriteFile(secret, []byte("file-content-xyz"), 0o644)
	for _, path := range []string{"/uploads/../secret.txt", "/uploads/%2e%2e/secret.txt", "/uploads/..%2fsecret.txt"} {
		if b := bodyOf(t, a.get(t, path)); strings.Contains(b, "file-content-xyz") {
			t.Errorf("GET %s served a file outside the directory", path)
		}
	}
}

func TestSecurityHeaders(t *testing.T) {
	a := newApp(t)
	token := a.createPage(t)

	resp := a.get(t, "/")
	for header, want := range map[string]string{
		"X-Content-Type-Options": "nosniff",
		"X-Frame-Options":        "DENY",
		"Referrer-Policy":        "no-referrer",
	} {
		if got := resp.Header.Get(header); got != want {
			t.Errorf("%s = %q, expected %q", header, got, want)
		}
	}
	csp := resp.Header.Get("Content-Security-Policy")
	if !strings.Contains(csp, "default-src 'self'") || strings.Contains(csp, "unsafe-inline") || strings.Contains(csp, "unsafe-eval") {
		t.Errorf("CSP is unsafe: %q", csp)
	}

	edit := a.get(t, "/edit/"+token)
	if !strings.Contains(edit.Header.Get("X-Robots-Tag"), "noindex") {
		t.Error("the edit page must be excluded from indexing")
	}
	if edit.Header.Get("Cache-Control") != "no-store" {
		t.Errorf("editor must not be cached: %q", edit.Header.Get("Cache-Control"))
	}
}

func TestTemplatesHaveNoInlineScriptsOrStyles(t *testing.T) {
	a := newApp(t)
	token := a.createPage(t)
	a.save(t, token, url.Values{"title": {"T"}, "content": {"x"}, "is_published": {"true"}})
	p, _ := a.pages.GetByEditToken(context.Background(), token)

	for _, path := range []string{"/", "/edit/" + token, "/p/" + p.PublicID, "/missing"} {
		body := bodyOf(t, a.get(t, path))
		for _, bad := range []string{" style=", "<style", " onclick=", " onchange=", " oninput="} {
			if strings.Contains(body, bad) {
				t.Errorf("%s: found %q, CSP would block it", path, bad)
			}
		}
		for _, seg := range strings.Split(body, "<script")[1:] {
			if !strings.HasPrefix(strings.TrimSpace(seg), "src=") {
				t.Errorf("%s: inline <script> without src", path)
			}
		}
	}
}

func TestStaticAssetsServed(t *testing.T) {
	a := newApp(t)
	for _, path := range []string{"/static/css/app.css", "/static/js/app.js", "/static/js/editor.js", "/static/js/theme.js", "/static/vendor/htmx-1.9.12.min.js", "/static/vendor/highlight-11.9.0.min.js"} {
		if resp := a.get(t, path); resp.StatusCode != 200 {
			t.Errorf("GET %s: %d", path, resp.StatusCode)
		}
	}
}

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func TestLogsDoNotLeakEditToken(t *testing.T) {
	logs := &syncBuffer{}
	db := testutil.NewDB(t)
	store, _ := service.NewImageStore(filepath.Join(t.TempDir(), "u"))
	h, _ := handlers.New(repository.NewPageRepo(db), repository.NewImageRepo(db), store, web.Templates(), slog.New(slog.NewTextHandler(logs, nil)), handlers.Options{})
	srv := httptest.NewServer(h.Routes(web.Static()))
	defer srv.Close()

	noRedirect := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	r, err := noRedirect.Post(srv.URL+"/create", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	r.Body.Close()
	token := strings.TrimPrefix(r.Header.Get("Location"), "/edit/")
	g, err := http.Get(srv.URL + "/edit/" + token)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(io.Discard, g.Body); err != nil {
		g.Body.Close()
		t.Fatal(err)
	}
	g.Body.Close()

	if strings.Contains(logs.String(), token) {
		t.Errorf("edit token leaked into the log:\n%s", logs.String())
	}
	if !strings.Contains(logs.String(), "/edit/***") {
		t.Errorf("expected a masked path in the log:\n%s", logs.String())
	}
}

func TestHealthz(t *testing.T) {
	a := newApp(t)
	if resp := a.get(t, "/healthz"); resp.StatusCode != 200 {
		t.Errorf("healthz: %d", resp.StatusCode)
	}
}

func TestPreviewRendersWithoutWriting(t *testing.T) {
	a := newApp(t)
	token := a.createPage(t)

	code, out := a.preview(t, token, url.Values{"title": {"Not saved"}, "content": {"**hello** <script>alert(1)</script>"}})
	if code != 200 || !strings.Contains(out, "<strong>hello</strong>") {
		t.Fatalf("preview: %d %s", code, out)
	}
	if strings.Contains(out, "<script") {
		t.Errorf("preview was not sanitized: %s", out)
	}
	if !strings.Contains(out, `id="reading-time"`) || !strings.Contains(out, `hx-swap-oob="true"`) {
		t.Errorf("response lacks the reading time oob block: %s", out)
	}

	p, _ := a.pages.GetByEditToken(context.Background(), token)
	if p.Title != "" || p.Content != "" {
		t.Errorf("preview must not write to the DB: %+v", p)
	}
}

func TestPreviewRequiresToken(t *testing.T) {
	a := newApp(t)
	if code, _ := a.preview(t, "nope", url.Values{"content": {"x"}}); code != http.StatusNotFound {
		t.Errorf("preview without a valid token: %d, expected 404", code)
	}
}

func TestUpdateSkipsWriteWhenNothingChanged(t *testing.T) {
	a := newApp(t)
	token := a.createPage(t)
	form := url.Values{"title": {"T"}, "content": {"text"}}
	a.save(t, token, form)

	old := "2001-02-03 04:05:06"
	if _, err := a.db.Exec(`UPDATE pages SET updated_at = ?`, old); err != nil {
		t.Fatal(err)
	}
	a.save(t, token, form)

	var got string
	a.db.QueryRow(`SELECT strftime('%Y-%m-%d %H:%M:%S', updated_at) FROM pages`).Scan(&got)
	if got != old {
		t.Errorf("write performed without changes (updated_at=%s)", got)
	}

	a.save(t, token, url.Values{"title": {"T"}, "content": {"other text"}})
	a.db.QueryRow(`SELECT strftime('%Y-%m-%d %H:%M:%S', updated_at) FROM pages`).Scan(&got)
	if got == old {
		t.Error("real changes must be written")
	}
}

func TestCreateIsRateLimited(t *testing.T) {
	a := newApp(t)
	var limited *http.Response
	for i := 0; i < 8; i++ {
		resp := a.post(t, "/create", nil)
		if resp.StatusCode == http.StatusTooManyRequests {
			limited = resp
			break
		}
	}
	if limited == nil {
		t.Fatal("expected 429 after a burst of requests")
	}
	if limited.Header.Get("Retry-After") == "" {
		t.Error("the 429 response needs Retry-After")
	}
	if a.get(t, "/").StatusCode != 200 {
		t.Error("reading pages must not be rate limited")
	}
}

func TestPreviewBodyLimit(t *testing.T) {
	a := newApp(t)
	token := a.createPage(t)
	big := url.Values{"content": {strings.Repeat("a", 300<<10)}}
	if code, _ := a.preview(t, token, big); code != http.StatusRequestEntityTooLarge {
		t.Errorf("oversized preview: %d, expected 413", code)
	}

	if code, _ := a.save(t, token, big); code != 200 {
		t.Errorf("saving 300 KB: %d", code)
	}
}
