package service

import (
	"bytes"
	"hash/crc32"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestGeneratedIDs(t *testing.T) {
	if got := len(GeneratePublicID()); got != 10 {
		t.Errorf("public ID: %d chars, expected 10", got)
	}
	if got := len(GenerateEditToken()); got != 32 {
		t.Errorf("edit token: %d chars, expected 32", got)
	}
	seen := map[string]bool{}
	for i := 0; i < 1000; i++ {
		tok := GenerateEditToken()
		if seen[tok] {
			t.Fatal("token repeated")
		}
		seen[tok] = true
		for _, r := range tok {
			if !strings.ContainsRune(charset, r) {
				t.Fatalf("character %q outside the alphabet", r)
			}
		}
	}
}

func TestReadingTime(t *testing.T) {
	words := func(n int) string { return strings.Repeat("word ", n) }
	cases := map[int]int{0: 1, 10: 1, 399: 1, 400: 2, 1000: 5}
	for n, want := range cases {
		if got := ReadingTime(words(n)); got != want {
			t.Errorf("%d words -> %d min, expected %d", n, got, want)
		}
	}
}

func TestExtractImageNames(t *testing.T) {
	md := "![](/uploads/aaa.jpg) text ![alt](/uploads/bbb.png)\n" +
		"[link](/uploads/aaa.jpg) <img src=\"/uploads/ccc.webp\">\n" +
		"![external](https://example.com/pic.jpg) /static/x.png"
	want := []string{"aaa.jpg", "bbb.png", "ccc.webp"}
	if got := ExtractImageNames(md); !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, expected %v", got, want)
	}
	if got := ExtractImageNames("empty"); len(got) != 0 {
		t.Errorf("expected an empty result: %v", got)
	}
}

func TestExcerpt(t *testing.T) {
	md := "# Heading\n\nFirst **paragraph** with a [link](https://x.y) and `code`.\n\n![picture](/uploads/a.jpg)\n\n```go\nfmt.Println()\n```\n"
	got := Excerpt(md, 200)
	if strings.ContainsAny(got, "#*`[]()") || strings.Contains(got, "fmt") || strings.Contains(got, "uploads") {
		t.Errorf("Markdown left in excerpt: %q", got)
	}
	if !strings.Contains(got, "link") || !strings.Contains(got, "paragraph") {
		t.Errorf("text lost: %q", got)
	}

	long := Excerpt(strings.Repeat("word ", 100), 50)
	if r := []rune(long); len(r) > 51 || !strings.HasSuffix(long, "...") {
		t.Errorf("long text was not truncated: %q (%d runes)", long, len(r))
	}
}

func TestRenderMarkdownBasics(t *testing.T) {
	out := RenderMarkdown("# Hello\n\n**bold** and ~~struck~~\n\n| a | b |\n|---|---|\n| 1 | 2 |\n")
	for _, want := range []string{"<h1", "<strong>bold</strong>", "<del>", "<table>"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
}

func TestRenderMarkdownStripsDangerousHTML(t *testing.T) {
	attacks := []string{
		"<script>alert(1)</script>",
		`<img src=x onerror="alert(1)">`,
		`[click](javascript:alert(1))`,
		`<iframe src="https://evil.example"></iframe>`,
		`<a href="data:text/html;base64,PHNjcmlwdD4=">x</a>`,
		`<div style="position:fixed">x</div>`,
		`<svg onload=alert(1)>`,
	}
	for _, a := range attacks {
		out := strings.ToLower(RenderMarkdown(a))
		for _, bad := range []string{"<script", "onerror", "onload", "javascript:", "<iframe", "data:text", "style=", "<svg"} {
			if strings.Contains(out, bad) {
				t.Errorf("input %q: output still contains %q:\n%s", a, bad, out)
			}
		}
	}
}

func TestRenderMarkdownKeepsCodeLanguageClass(t *testing.T) {
	out := RenderMarkdown("```go\nfmt.Println()\n```")
	if !strings.Contains(out, `class="language-go"`) {
		t.Errorf("language class lost, highlighting will not work:\n%s", out)
	}
}

func TestRenderMarkdownExternalLinksAreSafe(t *testing.T) {
	out := RenderMarkdown("[x](https://example.com)")
	if !strings.Contains(out, `target="_blank"`) || !strings.Contains(out, "noopener") {
		t.Errorf("external link must open safely:\n%s", out)
	}
}

func newStore(t *testing.T) *ImageStore {
	t.Helper()
	s, err := NewImageStore(filepath.Join(t.TempDir(), "uploads"))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func encodePNG(t *testing.T, img image.Image) *bytes.Reader {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return bytes.NewReader(buf.Bytes())
}

func TestSaveImageReencodesToJPEG(t *testing.T) {
	s := newStore(t)
	src := image.NewRGBA(image.Rect(0, 0, 40, 30))
	for x := 0; x < 40; x++ {
		for y := 0; y < 30; y++ {
			src.Set(x, y, color.RGBA{200, 30, 30, 255})
		}
	}
	name, err := s.Save(encodePNG(t, src))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(name, ".jpg") {
		t.Errorf("name %q must end with .jpg", name)
	}
	f, err := os.Open(filepath.Join(s.Dir(), name))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := jpeg.Decode(f); err != nil {
		t.Errorf("saved file is not a JPEG: %v", err)
	}
}

func TestSaveImageFlattensTransparencyOnWhite(t *testing.T) {
	s := newStore(t)
	transparent := image.NewNRGBA(image.Rect(0, 0, 20, 20))
	name, err := s.Save(encodePNG(t, transparent))
	if err != nil {
		t.Fatal(err)
	}
	f, _ := os.Open(filepath.Join(s.Dir(), name))
	defer f.Close()
	img, _ := jpeg.Decode(f)
	r, g, b, _ := img.At(10, 10).RGBA()
	if r>>8 < 240 || g>>8 < 240 || b>>8 < 240 {
		t.Errorf("transparent pixels must become white, got rgb(%d,%d,%d)", r>>8, g>>8, b>>8)
	}
}

func TestSaveImageAcceptsGIF(t *testing.T) {
	s := newStore(t)
	var buf bytes.Buffer
	if err := gif.Encode(&buf, image.NewPaletted(image.Rect(0, 0, 8, 8), color.Palette{color.Black, color.White}), nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Save(bytes.NewReader(buf.Bytes())); err != nil {
		t.Errorf("GIF must be accepted: %v", err)
	}
}

func TestSaveImageRejectsNonImages(t *testing.T) {
	s := newStore(t)
	cases := map[string]string{
		"text":       "plain text, not an image",
		"html":       "<html><script>alert(1)</script></html>",
		"svg":        `<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`,
		"empty":      "",
		"executable": "MZ\x90\x00\x03\x00\x00\x00",
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := s.Save(strings.NewReader(body)); err == nil {
				t.Error("expected a rejection")
			}
		})
	}
	if files, _ := s.List(); len(files) != 0 {
		t.Errorf("rejected files left on disk: %v", files)
	}
}

func TestSaveImageRejectsCorruptedImage(t *testing.T) {
	s := newStore(t)

	data := append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte{0xAB}, 100)...)
	if _, err := s.Save(bytes.NewReader(data)); err != ErrCorruptImage {
		t.Errorf("expected ErrCorruptImage, got %v", err)
	}
	if files, _ := s.List(); len(files) != 0 {
		t.Errorf("files left behind: %v", files)
	}
}

func TestSaveImageRejectsHugeDimensions(t *testing.T) {
	s := newStore(t)
	var buf bytes.Buffer
	png.Encode(&buf, image.NewGray(image.Rect(0, 0, 1, 1)))
	data := buf.Bytes()

	put := func(off int, v uint32) {
		data[off], data[off+1], data[off+2], data[off+3] = byte(v>>24), byte(v>>16), byte(v>>8), byte(v)
	}
	put(16, 20000)
	put(20, 20000)
	put(29, crc32.ChecksumIEEE(data[12:29]))
	if _, err := s.Save(bytes.NewReader(data)); err != ErrImageTooLarge {
		t.Errorf("expected ErrImageTooLarge, got %v", err)
	}
}

func TestSaveImageLeavesNoTempFiles(t *testing.T) {
	s := newStore(t)
	for i := 0; i < 3; i++ {
		if _, err := s.Save(encodePNG(t, image.NewGray(image.Rect(0, 0, 4, 4)))); err != nil {
			t.Fatal(err)
		}
	}
	entries, _ := os.ReadDir(s.Dir())
	if len(entries) != 3 {
		t.Errorf("directory has %d files, expected 3", len(entries))
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") {
			t.Errorf("temp file left behind: %s", e.Name())
		}
	}
}

func TestStoreRemove(t *testing.T) {
	s := newStore(t)
	name, _ := s.Save(encodePNG(t, image.NewGray(image.Rect(0, 0, 4, 4))))

	if err := s.Remove(name); err != nil {
		t.Fatal(err)
	}
	if err := s.Remove(name); err != nil {
		t.Errorf("removing twice must not be an error: %v", err)
	}
}

func TestStoreRemoveRefusesPathTraversal(t *testing.T) {
	dir := t.TempDir()
	victim := filepath.Join(dir, "victim.txt")
	os.WriteFile(victim, []byte("important"), 0o644)
	s, _ := NewImageStore(filepath.Join(dir, "uploads"))

	for _, name := range []string{"../victim.txt", "/etc/passwd", "a/b.jpg", "", ".hidden", ".."} {
		if err := s.Remove(name); err == nil {
			t.Errorf("Remove(%q) must refuse", name)
		}
	}
	if _, err := os.Stat(victim); err != nil {
		t.Errorf("file outside the directory was affected: %v", err)
	}
}

func TestStoreListSkipsServiceFiles(t *testing.T) {
	s := newStore(t)
	os.WriteFile(filepath.Join(s.Dir(), ".keep"), nil, 0o644)
	os.WriteFile(filepath.Join(s.Dir(), ".upload-123"), nil, 0o644)
	os.WriteFile(filepath.Join(s.Dir(), "ok.jpg"), nil, 0o644)
	os.Mkdir(filepath.Join(s.Dir(), "subdir"), 0o755)

	got, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, []string{"ok.jpg"}) {
		t.Errorf("got %v", got)
	}
}

func decodeStored(t *testing.T, s *ImageStore, name string) image.Config {
	t.Helper()
	f, err := os.Open(filepath.Join(s.Dir(), name))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	cfg, err := jpeg.DecodeConfig(f)
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestSaveImageDownscalesLargePictures(t *testing.T) {
	s := newStore(t)
	cases := []struct {
		name         string
		w, h         int
		wantW, wantH int
	}{
		{"wide", 3200, 1600, 1600, 800},
		{"tall", 1000, 4000, 400, 1600},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			name, err := s.Save(encodePNG(t, image.NewGray(image.Rect(0, 0, c.w, c.h))))
			if err != nil {
				t.Fatal(err)
			}
			cfg := decodeStored(t, s, name)
			if cfg.Width != c.wantW || cfg.Height != c.wantH {
				t.Errorf("%dx%d -> %dx%d, expected %dx%d", c.w, c.h, cfg.Width, cfg.Height, c.wantW, c.wantH)
			}
		})
	}
}

func TestSaveImageKeepsSmallPicturesAtOriginalSize(t *testing.T) {
	s := newStore(t)
	name, err := s.Save(encodePNG(t, image.NewGray(image.Rect(0, 0, 800, 600))))
	if err != nil {
		t.Fatal(err)
	}
	if cfg := decodeStored(t, s, name); cfg.Width != 800 || cfg.Height != 600 {
		t.Errorf("a small image must keep its size: %dx%d", cfg.Width, cfg.Height)
	}
}

func TestFitWithin(t *testing.T) {
	cases := []struct{ w, h, max, wantW, wantH int }{
		{100, 100, 1600, 100, 100},
		{1600, 1600, 1600, 1600, 1600},
		{1601, 1, 1600, 1600, 1},
		{10000, 10, 1600, 1600, 1},
		{4000, 3000, 1600, 1600, 1200},
	}
	for _, c := range cases {
		if w, h := fitWithin(c.w, c.h, c.max); w != c.wantW || h != c.wantH {
			t.Errorf("fitWithin(%d,%d)=%dx%d, expected %dx%d", c.w, c.h, w, h, c.wantW, c.wantH)
		}
	}
}

func TestSaveImageRejectsOverPixelBudget(t *testing.T) {
	s := newStore(t)
	var buf bytes.Buffer
	png.Encode(&buf, image.NewGray(image.Rect(0, 0, 1, 1)))
	data := buf.Bytes()
	put := func(off int, v uint32) {
		data[off], data[off+1], data[off+2], data[off+3] = byte(v>>24), byte(v>>16), byte(v>>8), byte(v)
	}
	put(16, 6000)
	put(20, 5000)
	put(29, crc32.ChecksumIEEE(data[12:29]))
	if _, err := s.Save(bytes.NewReader(data)); err != ErrImageTooLarge {
		t.Errorf("expected ErrImageTooLarge, got %v", err)
	}
}
