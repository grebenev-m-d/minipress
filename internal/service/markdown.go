package service

import (
	"bytes"

	"github.com/microcosm-cc/bluemonday"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer/html"
)

var (
	mdProcessor goldmark.Markdown
	sanitizer   *bluemonday.Policy
)

func init() {
	mdProcessor = goldmark.New(
		goldmark.WithExtensions(extension.GFM),
		goldmark.WithParserOptions(parser.WithAutoHeadingID()),
		goldmark.WithRendererOptions(html.WithHardWraps()),
	)

	sanitizer = bluemonday.UGCPolicy()

	sanitizer.AllowAttrs("class").Matching(bluemonday.SpaceSeparatedTokens).OnElements("code")

	sanitizer.AddTargetBlankToFullyQualifiedLinks(true)
	sanitizer.RequireNoReferrerOnLinks(true)
}

func RenderMarkdown(source string) string {
	var buf bytes.Buffer
	if err := mdProcessor.Convert([]byte(source), &buf); err != nil {
		return ""
	}
	return sanitizer.Sanitize(buf.String())
}
