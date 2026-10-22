package web

import (
	"embed"
	"io/fs"
)

//go:embed templates/*.html
var templates embed.FS

//go:embed static
var static embed.FS

func Templates() fs.FS { return mustSub(templates, "templates") }
func Static() fs.FS    { return mustSub(static, "static") }

func mustSub(fsys fs.FS, dir string) fs.FS {
	sub, err := fs.Sub(fsys, dir)
	if err != nil {
		panic(err)
	}
	return sub
}
