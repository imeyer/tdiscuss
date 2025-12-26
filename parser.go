package main

import (
	"bytes"
	"fmt"
	"html"
	"regexp"
	"strconv"

	"github.com/microcosm-cc/bluemonday"
	"github.com/yuin/goldmark"
	emoji "github.com/yuin/goldmark-emoji"
	"github.com/yuin/goldmark/extension"
	gmhtml "github.com/yuin/goldmark/renderer/html"
)

func parseMarkdownToHTML(text string) string {
	var buf bytes.Buffer

	md := goldmark.New(
		goldmark.WithExtensions(
			emoji.Emoji,
			extension.GFM,
		),
		goldmark.WithRendererOptions(
			gmhtml.WithUnsafe(),
		),
	)

	if err := md.Convert([]byte(text), &buf); err != nil {
		return text // Fall back to the original text on error
	}

	return buf.String()
}

func parseHTMLStrict(text string) string {
	strict := bluemonday.StrictPolicy()

	// Sanitize to strip all HTML tags, then unescape entities to get plain text.
	// The template engine will re-escape on output.
	return html.UnescapeString(strict.Sanitize(text))
}

func parseHTMLLessStrict(text string) string {
	lessStrict := bluemonday.UGCPolicy()

	return lessStrict.Sanitize(text)
}

func parseID(path string) (int64, error) {
	re := regexp.MustCompile(`^/(thread|member)/([0-9]+)$`)
	matches := re.FindStringSubmatch(path)
	if len(matches) < 2 {
		return 0, fmt.Errorf("invalid thread ID in URL")
	}
	return strconv.ParseInt(matches[2], 10, 64)
}
