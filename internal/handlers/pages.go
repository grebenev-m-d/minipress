package handlers

import (
	"errors"
	"html/template"
	"net/http"
	"strings"
	"unicode/utf8"

	"minipress/internal/models"
	"minipress/internal/service"
)

func (a *App) handleIndex(w http.ResponseWriter, r *http.Request) {
	a.render(w, http.StatusOK, "index", nil)
}

func (a *App) handleCreate(w http.ResponseWriter, r *http.Request) {
	page := &models.Page{
		PublicID:  service.GeneratePublicID(),
		EditToken: service.GenerateEditToken(),
	}
	if err := a.pages.Create(r.Context(), page); err != nil {
		a.serverError(w, r, err)
		return
	}
	http.Redirect(w, r, "/edit/"+page.EditToken, http.StatusSeeOther)
}

type editView struct {
	Page        *models.Page
	Preview     template.HTML
	ReadingTime int
	Origin      string
}

func (a *App) handleEdit(w http.ResponseWriter, r *http.Request) {
	page, err := a.pages.GetByEditToken(r.Context(), r.PathValue("token"))
	if err != nil {
		if isNotFound(err) {
			a.notFound(w, r)
		} else {
			a.serverError(w, r, err)
		}
		return
	}
	a.render(w, http.StatusOK, "edit", editView{
		Page:        page,
		Preview:     template.HTML(service.RenderMarkdown(page.Content)),
		ReadingTime: service.ReadingTime(page.Content),
		Origin:      origin(r),
	})
}

type previewView struct {
	HTML        template.HTML
	ReadingTime int
}

type editorInput struct {
	Title, Author, Content string
	Published              bool
}

func readEditorForm(w http.ResponseWriter, r *http.Request, maxBody int64) (in editorInput, ok bool) {
	r.Body = http.MaxBytesReader(w, r.Body, maxBody)
	if err := r.ParseForm(); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			http.Error(w, "Text is too large", http.StatusRequestEntityTooLarge)
		} else {
			http.Error(w, "Bad request", http.StatusBadRequest)
		}
		return in, false
	}

	in = editorInput{
		Title:     strings.Join(strings.Fields(r.PostFormValue("title")), " "),
		Author:    strings.TrimSpace(r.PostFormValue("author")),
		Content:   r.PostFormValue("content"),
		Published: r.PostFormValue("is_published") == "true",
	}
	switch {
	case utf8.RuneCountInString(in.Title) > maxTitleRunes:
		http.Error(w, "Title is longer than 200 characters", http.StatusUnprocessableEntity)
	case utf8.RuneCountInString(in.Author) > maxAuthorRunes:
		http.Error(w, "Author name is longer than 80 characters", http.StatusUnprocessableEntity)
	case len(in.Content) > maxContentSize:
		http.Error(w, "Text is larger than 1 MB", http.StatusRequestEntityTooLarge)
	default:
		return in, true
	}
	return in, false
}

func (a *App) handlePreview(w http.ResponseWriter, r *http.Request) {
	if !a.tokenExists(w, r) {
		return
	}
	in, ok := readEditorForm(w, r, maxPreviewBytes)
	if !ok {
		return
	}
	a.renderFragment(w, "edit", "preview-fragment", previewView{
		HTML:        template.HTML(service.RenderMarkdown(in.Content)),
		ReadingTime: service.ReadingTime(in.Content),
	})
}

func (a *App) handleUpdate(w http.ResponseWriter, r *http.Request) {
	page, err := a.pages.GetByEditToken(r.Context(), r.PathValue("token"))
	if err != nil {
		if isNotFound(err) {
			http.Error(w, "Article not found, it may have been deleted", http.StatusNotFound)
		} else {
			a.log.Error("update: read", "err", err)
			http.Error(w, "Database error", http.StatusInternalServerError)
		}
		return
	}
	in, ok := readEditorForm(w, r, maxFormBytes)
	if !ok {
		return
	}

	changed := page.Title != in.Title || page.Author != in.Author || page.Content != in.Content || page.IsPublished != in.Published
	if changed {
		page.Title, page.Author, page.Content, page.IsPublished = in.Title, in.Author, in.Content, in.Published
		if err := a.pages.Update(r.Context(), page); err != nil {
			a.log.Error("update: write", "err", err)
			http.Error(w, "Could not save", http.StatusInternalServerError)
			return
		}

		if err := a.images.SyncPageRefs(r.Context(), page.ID, service.ExtractImageNames(in.Content)); err != nil {

			a.log.Error("update: sync image refs", "err", err)
		}
	}
	a.renderFragment(w, "edit", "draft-note-oob", in.Published)
}

func (a *App) tokenExists(w http.ResponseWriter, r *http.Request) bool {
	if _, err := a.pages.GetByEditToken(r.Context(), r.PathValue("token")); err != nil {
		if isNotFound(err) {
			http.Error(w, "Article not found, it may have been deleted", http.StatusNotFound)
		} else {
			a.log.Error("preview: read", "err", err)
			http.Error(w, "Database error", http.StatusInternalServerError)
		}
		return false
	}
	return true
}

type articleView struct {
	Page        *models.Page
	Content     template.HTML
	ReadingTime int
	Excerpt     string
}

func (a *App) handleView(w http.ResponseWriter, r *http.Request) {
	page, err := a.pages.GetByPublicID(r.Context(), r.PathValue("id"))
	if err != nil {
		if isNotFound(err) {
			a.notFound(w, r)
		} else {
			a.serverError(w, r, err)
		}
		return
	}

	if !page.IsPublished {
		a.notFound(w, r)
		return
	}
	a.render(w, http.StatusOK, "view", articleView{
		Page:        page,
		Content:     template.HTML(service.RenderMarkdown(page.Content)),
		ReadingTime: service.ReadingTime(page.Content),
		Excerpt:     service.Excerpt(page.Content, 160),
	})
}

func (a *App) handleDelete(w http.ResponseWriter, r *http.Request) {
	if err := a.pages.Delete(r.Context(), r.PathValue("token")); err != nil {
		if isNotFound(err) {
			a.notFound(w, r)
		} else {
			a.serverError(w, r, err)
		}
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func origin(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}
