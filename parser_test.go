package main

import (
	"strings"
	"testing"
)

func TestParseID(t *testing.T) {
	tests := []struct {
		name    string
		path    string
		want    int64
		wantErr bool
	}{
		{"Valid thread ID", "/thread/1263", 1263, false},
		{"Valid member ID", "/member/23", 23, false},
		{"Invalid member path", "/member/abc", 0, true},
		{"Invalid thread path", "/thread/abc", 0, true},
		{"Empty path", "", 0, true},
		{"Missing thread ID", "/thread/", 0, true},
		{"Missing member ID", "/member/", 0, true},
		{"Extra thread characters", "/thread/123/extra", 0, true},
		{"Extra member characters", "/member/123/extra", 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseID(tt.path)
			if (err != nil) != tt.wantErr {
				t.Errorf("parseID() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("parseID() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestParseHTMLLessStrict(t *testing.T) {
	testCases := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "Script tag removal",
			input:    `<p>Hello</p><script>alert('XSS');</script>`,
			expected: `<p>Hello</p>`,
		},
		{
			name:     "No anchor tags",
			input:    `<a href="javascript:alert('XSS')">Click me</a>`,
			expected: `Click me`,
		},
		{
			name:     "Iframe removal",
			input:    `<iframe src="https://malicious-site.com"></iframe>`,
			expected: ``,
		},
		{
			name:     "On* event handler removal",
			input:    `<img src="image.jpg" onerror="alert('XSS')">`,
			expected: `<img src="image.jpg">`,
		},
		{
			// We do not allow img tags with data in the src
			name:     "Data URL removal",
			input:    `<img src="data:image/svg+xml;base64,PHN2ZyBvbmxvYWQ9ImFsZXJ0KDEpIiB4bWxucz0iaHR0cDovL3d3dy53My5vcmcvMjAwMC9zdmciPjwvc3ZnPg==">`,
			expected: ``,
		},
		{
			name:     "CSS expression removal",
			input:    `<div style="background-image: url('javascript:alert(\'XSS\')')">Content</div>`,
			expected: `<div>Content</div>`,
		},
		{
			name:     "Nested dangerous tags removal",
			input:    `<p>Safe <b>content <script>alert('XSS')</script></b></p>`,
			expected: `<p>Safe <b>content </b></p>`,
		},
		{
			name:     "Malformed tag handling",
			input:    `<p>Text</p><script>alert('XSS');</script><p>More text`,
			expected: `<p>Text</p><p>More text`,
		},
		{
			name:     "Allow safe tags and attributes",
			input:    `<a href="https://example.com" target="_blank">Safe link</a>`,
			expected: `<a href="https://example.com" rel="nofollow">Safe link</a>`,
		},
		{
			name:     "Mixed safe and unsafe content",
			input:    `<p>Safe <strong>content</strong></p><img src="image.jpg" onload="alert('XSS')"><script>alert('More XSS');</script>`,
			expected: `<p>Safe <strong>content</strong></p><img src="image.jpg">`,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := parseHTMLLessStrict(tc.input)
			if result != tc.expected {
				t.Errorf("parseHTMLLessStrict(%q) = %q, want %q", tc.input, result, tc.expected)
			}
		})
	}
}

func TestParseHTMLStrict(t *testing.T) {
	testCases := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "Script tag removal",
			input:    `<p>Hello</p><script>alert('XSS');</script>`,
			expected: `Hello`,
		},
		{
			name:     "No anchor tags",
			input:    `<a href="javascript:alert('XSS')">Click me</a>`,
			expected: `Click me`,
		},
		{
			name:     "Iframe removal",
			input:    `<iframe src="https://malicious-site.com"></iframe>`,
			expected: ``,
		},
		{
			name:     "Image tag removal",
			input:    `<img src="image.jpg" onerror="alert('XSS')">`,
			expected: ``,
		},
		{
			// We do not allow img tags with data in the src
			name:     "Data URL removal",
			input:    `<img src="data:image/svg+xml;base64,PHN2ZyBvbmxvYWQ9ImFsZXJ0KDEpIiB4bWxucz0iaHR0cDovL3d3dy53My5vcmcvMjAwMC9zdmciPjwvc3ZnPg==">`,
			expected: ``,
		},
		{
			name:     "CSS expression removal",
			input:    `<div style="background-image: url('javascript:alert(\'XSS\')')">Content</div>`,
			expected: `Content`,
		},
		{
			name:     "Nested dangerous tags removal",
			input:    `<p>Safe <b>content <script>alert('XSS')</script></b></p>`,
			expected: `Safe content `,
		},
		{
			name:     "Malformed tag handling",
			input:    `<p>Text</p><script>alert('XSS');</script><p>More text`,
			expected: `TextMore text`,
		},
		{
			name:     "No anchor tags",
			input:    `<a href="https://example.com" target="_blank">Safe link</a>`,
			expected: `Safe link`,
		},
		{
			name:     "Mixed safe and unsafe content",
			input:    `<p>Safe <strong>content</strong></p><img src="image.jpg" onload="alert('XSS')"><script>alert('More XSS');</script>`,
			expected: `Safe content`,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := parseHTMLStrict(tc.input)
			if result != tc.expected {
				t.Errorf("parseHTMLStrict(%q) = %q, want %q", tc.input, result, tc.expected)
			}
		})
	}
}

func TestParseMarkdownToHTML(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "Basic Markdown",
			input:    "# Hello\nThis is **bold** and *italic*.",
			expected: "<h1>Hello</h1>\n<p>This is <strong>bold</strong> and <em>italic</em>.</p>\n",
		},
		{
			name:     "Links",
			input:    "[Google](https://www.google.com)",
			expected: "<p><a href=\"https://www.google.com\">Google</a></p>\n",
		},
		{
			name:     "Code Blocks",
			input:    "```go\nfunc main() {\n\tfmt.Println(\"Hello, World!\")\n}\n```",
			expected: "<pre><code class=\"language-go\">func main() {\n\tfmt.Println(&quot;Hello, World!&quot;)\n}\n</code></pre>\n",
		},
		{
			name:     "Lists",
			input:    "- Item 1\n- Item 2\n  - Subitem 2.1",
			expected: "<ul>\n<li>Item 1</li>\n<li>Item 2\n<ul>\n<li>Subitem 2.1</li>\n</ul>\n</li>\n</ul>\n",
		},
		{
			name:     "Emojis",
			input:    "I :heart: Markdown!",
			expected: "<p>I &#x2764;&#xfe0f; Markdown!</p>\n",
		},
		{
			name:     "Tables (GFM)",
			input:    "| Column 1 | Column 2 |\n|----------|----------|\n| Cell 1   | Cell 2   |",
			expected: "<table>\n<thead>\n<tr>\n<th>Column 1</th>\n<th>Column 2</th>\n</tr>\n</thead>\n<tbody>\n<tr>\n<td>Cell 1</td>\n<td>Cell 2</td>\n</tr>\n</tbody>\n</table>\n",
		},
		{
			name:     "Raw HTML",
			input:    "This is <span style=\"color: red;\">red</span>.",
			expected: "<p>This is <span style=\"color: red;\">red</span>.</p>\n",
		},
		{
			name:     "Empty Input",
			input:    "",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parseMarkdownToHTML(tt.input)
			if result != tt.expected {
				t.Errorf("parseMarkdownToHTML(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestParseMarkdownToHTMLError(t *testing.T) {
	// This test case is to simulate an error condition.
	// However, it's difficult to cause an error in the goldmark parser.
	// You might need to mock the goldmark.Convert function to simulate an error.
	// For now, we'll just test that extremely large input doesn't cause issues.
	largeInput := strings.Repeat("a", 1000000) // 1 million characters
	result := parseMarkdownToHTML(largeInput)
	if result == "" {
		t.Errorf("parseMarkdownToHTML failed to handle large input")
	}
}
