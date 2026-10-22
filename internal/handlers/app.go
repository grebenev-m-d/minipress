package handlers

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"path"
	"strings"
	"time"

	"minipress/internal/repository"
	"minipress/internal/service"
)

const (
	maxFormBytes   = 2 << 20
	maxUploadBytes = 5 << 20
	maxTitleRunes  = 200
	maxAuthorRunes = 80
	maxContentSize = 1 << 20

	maxPreviewBytes = 256 << 10

	maxConcurrentImages = 2
	imageQueueTimeout   = 5 * time.Second
)

type Options struct {
	TrustProxy bool
}

type App struct {
	trustProxy bool
	createRL   *rateLimiter
	editorRL   *rateLimiter
	uploadRL   *rateLimiter
	imageSem   chan struct{}
	queueWait  time.Duration

	pages  *repository.PageRepo
	images *repository.ImageRepo
	store  *service.ImageStore
	pagesT map[string]*template.Template
	log    *slog.Logger
}

func New(pages *repository.PageRepo, images *repository.ImageRepo, store *service.ImageStore, templates fs.FS, log *slog.Logger, opts Options) (*App, error) {
	a := &App{
		pages: pages, images: images, store: store, log: log, pagesT: map[string]*template.Template{},
		trustProxy: opts.TrustProxy,
		createRL:   newRateLimiter(10.0/60, 5),
		editorRL:   newRateLimiter(10, 30),
		uploadRL:   newRateLimiter(1, 5),
		imageSem:   make(chan struct{}, maxConcurrentImages),
		queueWait:  imageQueueTimeout,
	}

	funcs := template.FuncMap{"date": formatDate, "minutes": minutesLabel}
	for _, name := range []string{"index", "edit", "view", "error"} {
		t, err := template.New("").Funcs(funcs).ParseFS(templates, "layout.html", name+".html")
		if err != nil {
			return nil, fmt.Errorf("template %s: %w", name, err)
		}
		a.pagesT[name] = t
	}
	return a, nil
}

func (a *App) Routes(static fs.FS) http.Handler {
	mux := http.NewServeMux()

	mux.Handle("GET /static/", cacheFor(time.Hour, http.StripPrefix("/static/", http.FileServerFS(noListing{static}))))
	mux.Handle("GET /uploads/", immutable(http.StripPrefix("/uploads/", http.FileServer(noListingDir{http.Dir(a.store.Dir())}))))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { fmt.Fprintln(w, "ok") })

	mux.HandleFunc("GET /{$}", a.handleIndex)
	mux.HandleFunc("POST /create", a.limit(a.createRL, a.handleCreate))
	mux.HandleFunc("GET /edit/{token}", a.handleEdit)
	mux.HandleFunc("POST /update/{token}", a.limit(a.editorRL, a.handleUpdate))
	mux.HandleFunc("POST /preview/{token}", a.limit(a.editorRL, a.handlePreview))
	mux.HandleFunc("POST /upload/{token}", a.limit(a.uploadRL, a.handleUpload))
	mux.HandleFunc("POST /delete/{token}", a.handleDelete)
	mux.HandleFunc("GET /p/{id}", a.handleView)
	mux.HandleFunc("/", a.fallback)

	return chain(mux, a.recoverer, a.logRequests, securityHeaders)
}

func (a *App) render(w http.ResponseWriter, status int, name string, data any) {
	var buf bytes.Buffer
	if err := a.pagesT[name].ExecuteTemplate(&buf, "layout", data); err != nil {
		a.log.Error("render template", "template", name, "err", err)
		http.Error(w, "Internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	buf.WriteTo(w)
}

func (a *App) renderFragment(w http.ResponseWriter, set, name string, data any) {
	var buf bytes.Buffer
	if err := a.pagesT[set].ExecuteTemplate(&buf, name, data); err != nil {
		a.log.Error("render fragment", "template", name, "err", err)
		http.Error(w, "Internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	buf.WriteTo(w)
}

type errorPage struct {
	Code    int
	Title   string
	Message string
}

func (a *App) notFound(w http.ResponseWriter, r *http.Request) {
	a.render(w, http.StatusNotFound, "error", errorPage{404, "Page not found", "This article does not exist or has not been published yet."})
}

func (a *App) serverError(w http.ResponseWriter, r *http.Request, err error) {
	a.log.Error("handler error", "path", r.URL.Path, "err", err)
	a.render(w, http.StatusInternalServerError, "error", errorPage{500, "Something went wrong", "The error has been logged. Try reloading the page."})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func jsonError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func isNotFound(err error) bool { return errors.Is(err, repository.ErrNotFound) }

func formatDate(t time.Time) string { return t.Format("2 January 2006") }

func minutesLabel(n int) string { return fmt.Sprintf("%d min", n) }

type noListing struct{ fs.FS }

func (n noListing) Open(name string) (fs.File, error) {
	f, err := n.FS.Open(name)
	if err != nil {
		return nil, err
	}
	if st, err := f.Stat(); err == nil && st.IsDir() {
		f.Close()
		return nil, fs.ErrNotExist
	}
	return f, nil
}

type noListingDir struct{ http.FileSystem }

func (n noListingDir) Open(name string) (http.File, error) {

	if strings.HasPrefix(path.Base(name), ".") {
		return nil, fs.ErrNotExist
	}
	f, err := n.FileSystem.Open(name)
	if err != nil {
		return nil, err
	}
	if st, err := f.Stat(); err == nil && st.IsDir() {
		f.Close()
		return nil, fs.ErrNotExist
	}
	return f, nil
}

func (a *App) fallback(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Path
	known := p == "/create" || p == "/"
	for _, prefix := range []string{"/edit/", "/update/", "/preview/", "/upload/", "/delete/", "/p/"} {
		if strings.HasPrefix(p, prefix) && len(p) > len(prefix) {
			known = true
		}
	}
	if known {
		w.Header().Set("Allow", "GET, POST")
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	a.notFound(w, r)
}
