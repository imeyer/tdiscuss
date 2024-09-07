package main

import (
	"bytes"
	"fmt"
	"regexp"
	"strconv"

	"github.com/microcosm-cc/bluemonday"
	"github.com/yuin/goldmark"
	emoji "github.com/yuin/goldmark-emoji"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/renderer/html"
)

func parseMarkdownToHTML(text string) string {
	var buf bytes.Buffer

	md := goldmark.New(
		goldmark.WithExtensions(
			emoji.Emoji,
			extension.GFM,
		),
		goldmark.WithRendererOptions(
			html.WithUnsafe(), // Allow raw HTML if needed
		),
	)

	if err := md.Convert([]byte(text), &buf); err != nil {
		return text // Fall back to the original text on error
	}

	return buf.String()
}

func parseHTMLStrict(text string) string {
	strict := bluemonday.StrictPolicy()

	return strict.Sanitize(text)
}

func parseHTMLLessStrict(text string) string {
	lessStrict := bluemonday.UGCPolicy()

	return lessStrict.Sanitize(text)
}

func parseThreadID(path string) (int64, error) {
	re := regexp.MustCompile(`^/thread/([0-9]+)$`)
	matches := re.FindStringSubmatch(path)
	if len(matches) < 2 {
		return 0, fmt.Errorf("invalid thread ID in URL")
	}
	return strconv.ParseInt(matches[1], 10, 64)
}
