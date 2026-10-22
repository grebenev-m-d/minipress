package service

import (
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	_ "image/gif"
	_ "image/png"

	xdraw "golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

const (
	maxImageSide   = 10000
	maxImagePixels = 25_000_000

	maxStoredSide = 1600
	jpegQuality   = 80
)

var (
	ErrUnsupportedFormat = errors.New("unsupported format: JPEG, PNG, GIF or WebP required")
	ErrCorruptImage      = errors.New("could not read image: file is corrupt")
	ErrImageTooLarge     = errors.New("image dimensions are too large")
)

var allowedTypes = map[string]bool{
	"image/jpeg": true, "image/png": true, "image/gif": true, "image/webp": true,
}

type ImageStore struct {
	dir string
}

func NewImageStore(dir string) (*ImageStore, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return &ImageStore{dir: dir}, nil
}

func (s *ImageStore) Dir() string { return s.dir }

func (s *ImageStore) Save(src io.ReadSeeker) (string, error) {
	head := make([]byte, 512)
	n, err := src.Read(head)
	if err != nil && err != io.EOF {
		return "", err
	}
	if !allowedTypes[http.DetectContentType(head[:n])] {
		return "", ErrUnsupportedFormat
	}
	if _, err := src.Seek(0, io.SeekStart); err != nil {
		return "", err
	}

	cfg, _, err := image.DecodeConfig(src)
	if err != nil {
		return "", ErrCorruptImage
	}
	if cfg.Width <= 0 || cfg.Height <= 0 {
		return "", ErrCorruptImage
	}
	if cfg.Width > maxImageSide || cfg.Height > maxImageSide || cfg.Width*cfg.Height > maxImagePixels {
		return "", ErrImageTooLarge
	}
	if _, err := src.Seek(0, io.SeekStart); err != nil {
		return "", err
	}

	img, _, err := image.Decode(src)
	if err != nil {
		return "", ErrCorruptImage
	}

	flat := flatten(img)

	name := GeneratePublicID() + ".jpg"

	tmp, err := os.CreateTemp(s.dir, ".upload-*")
	if err != nil {
		return "", err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	if err := jpeg.Encode(tmp, flat, &jpeg.Options{Quality: jpegQuality}); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	if err := os.Chmod(tmpName, 0o644); err != nil {
		return "", err
	}
	if err := os.Rename(tmpName, filepath.Join(s.dir, name)); err != nil {
		return "", err
	}
	return name, nil
}

func flatten(img image.Image) *image.RGBA {
	src := img.Bounds()
	w, h := fitWithin(src.Dx(), src.Dy(), maxStoredSide)
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(dst, dst.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
	if w == src.Dx() && h == src.Dy() {
		draw.Draw(dst, dst.Bounds(), img, src.Min, draw.Over)
	} else {
		xdraw.CatmullRom.Scale(dst, dst.Bounds(), img, src, xdraw.Over, nil)
	}
	return dst
}

func fitWithin(w, h, max int) (int, int) {
	if w <= max && h <= max {
		return w, h
	}
	if w >= h {
		return max, maxInt(1, h*max/w)
	}
	return maxInt(1, w*max/h), max
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func (s *ImageStore) Remove(name string) error {
	if !validFileName(name) {
		return fmt.Errorf("invalid file name %q", name)
	}
	if err := os.Remove(filepath.Join(s.dir, name)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func (s *ImageStore) List() ([]string, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if e.Type().IsRegular() && validFileName(e.Name()) {
			out = append(out, e.Name())
		}
	}
	return out, nil
}

func validFileName(name string) bool {
	if name == "" || strings.HasPrefix(name, ".") || name != filepath.Base(name) {
		return false
	}
	for _, r := range name {
		ok := r == '.' || r == '_' || r == '-' || (r >= '0' && r <= '9') || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
		if !ok {
			return false
		}
	}
	return true
}
