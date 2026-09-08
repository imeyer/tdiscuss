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

// neverMatchEmail disables goldmark's email autolinking. It must never match
// any input: an empty-matching regexp (e.g. `^$`) makes the linkify parser
// compute line[-1:] and panic with "slice bounds out of range [:-1]" on inputs
// as small as "00(". This pattern requires a codepoint outside the Unicode
// range, so it can never match.
var neverMatchEmail = regexp.MustCompile(`[^\x00-\x{10FFFF}]`)

func parseMarkdownToHTML(text string) (result string) {
	// Defense in depth: goldmark extensions have panicked on malformed input
	// before. Recover so a single crafted post can't 500 the request.
	defer func() {
		if r := recover(); r != nil {
			result = html.EscapeString(text)
		}
	}()

	var buf bytes.Buffer

	md := goldmark.New(
		goldmark.WithExtensions(
			emoji.Emoji,
			extension.Strikethrough,
			extension.Table,
			extension.TaskList,
			// Linkify URLs but not email addresses.
			extension.NewLinkify(
				extension.WithLinkifyEmailRegexp(neverMatchEmail),
			),
		),
		goldmark.WithRendererOptions(
			gmhtml.WithUnsafe(),
		),
	)

	if err := md.Convert([]byte(text), &buf); err != nil {
		return text
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
	return bodyPolicy().Sanitize(text)
}

// bodyPolicy returns a bluemonday policy for thread/post bodies.
// Based on UGCPolicy but excludes headings (h1-h6) to prevent
// users from dominating the page with large headers.
func bodyPolicy() *bluemonday.Policy {
	p := bluemonday.NewPolicy()

	// Allowed block elements (no h1-h6, no tables)
	p.AllowElements("p", "br", "hr", "div", "span")
	p.AllowElements("blockquote", "pre")
	p.AllowElements("ul", "ol", "li", "dl", "dt", "dd")

	p.AllowElements("b", "i", "strong", "em", "u", "s", "strike", "del", "ins")
	p.AllowElements("sub", "sup", "small", "mark")
	p.AllowElements("abbr", "acronym", "cite", "dfn", "kbd", "samp", "var")

	p.AllowElements("code")
	p.AllowAttrs("class").Matching(regexp.MustCompile(`^language-[\w-]+$`)).OnElements("code")

	p.AllowAttrs("href").OnElements("a")
	p.AllowAttrs("title").OnElements("a")
	p.AllowRelativeURLs(true)
	p.RequireNoFollowOnLinks(true)
	p.RequireNoReferrerOnLinks(true)
	p.AddTargetBlankToFullyQualifiedLinks(true)

	p.AllowImages()

	// Task lists (GFM)
	p.AllowAttrs("type", "disabled", "checked").OnElements("input")

	return p
}

func parseID(path string) (int64, error) {
	re := regexp.MustCompile(`^/(thread|member)/([0-9]+)$`)
	matches := re.FindStringSubmatch(path)
	if len(matches) < 2 {
		return 0, fmt.Errorf("invalid thread ID in URL")
	}
	return strconv.ParseInt(matches[2], 10, 64)
}
