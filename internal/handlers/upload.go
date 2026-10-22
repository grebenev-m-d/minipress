package handlers

import (
	"errors"
	"net/http"
	"time"

	"minipress/internal/service"
)

func (a *App) handleUpload(w http.ResponseWriter, r *http.Request) {
	if _, err := a.pages.GetByEditToken(r.Context(), r.PathValue("token")); err != nil {
		if isNotFound(err) {
			jsonError(w, http.StatusNotFound, "Article not found")
		} else {
			a.log.Error("upload: read article", "err", err)
			jsonError(w, http.StatusInternalServerError, "Database error")
		}
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxUploadBytes+(64<<10))
	if err := r.ParseMultipartForm(1 << 20); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			jsonError(w, http.StatusRequestEntityTooLarge, "File is larger than 5 MB")
		} else {
			jsonError(w, http.StatusBadRequest, "Bad request")
		}
		return
	}
	if r.MultipartForm != nil {
		defer r.MultipartForm.RemoveAll()
	}

	file, _, err := r.FormFile("image")
	if err != nil {
		jsonError(w, http.StatusBadRequest, "No file provided")
		return
	}
	defer file.Close()

	select {
	case a.imageSem <- struct{}{}:
		defer func() { <-a.imageSem }()
	case <-time.After(a.queueWait):
		w.Header().Set("Retry-After", "5")
		jsonError(w, http.StatusServiceUnavailable, "Server is busy processing images, try again in a few seconds")
		return
	case <-r.Context().Done():
		return
	}

	name, err := a.store.Save(file)
	switch {
	case errors.Is(err, service.ErrUnsupportedFormat):
		jsonError(w, http.StatusUnsupportedMediaType, err.Error())
		return
	case errors.Is(err, service.ErrCorruptImage), errors.Is(err, service.ErrImageTooLarge):
		jsonError(w, http.StatusUnprocessableEntity, err.Error())
		return
	case err != nil:
		a.log.Error("upload: save", "err", err)
		jsonError(w, http.StatusInternalServerError, "Could not save the file")
		return
	}

	if err := a.images.Register(r.Context(), name); err != nil {

		a.log.Error("upload: register", "err", err)
		_ = a.store.Remove(name)
		jsonError(w, http.StatusInternalServerError, "Could not save the file")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"url": "/uploads/" + name})
}
