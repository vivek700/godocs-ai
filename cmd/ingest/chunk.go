package main

import (
	"bytes"
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/yuin/goldmark/v2/ast"
	"github.com/yuin/goldmark/v2/parser"
)

const (
	minWords = 50
	maxWords = 500
)

type Chunk struct {
	ID     string `json:"id"`
	File   string `json:"file"`
	URL    string `json:"url"`
	Type   string `json:"type"`
	Path   string `json:"path"`
	Anchor string `json:"anchor,omitempty"`
	Text   string `json:"text"`
	Merged bool   `json:"-"`
}

type heading struct {
	level int
	title string
}

// mark is one heading found in the AST, with byte offsets into the cleaned body.
type mark struct {
	level         int
	title, anchor string
	lineStart     int // start of the heading's line
	bodyStart     int // start of the line after the heading
}

var (
	tmplRe      = regexp.MustCompile(`\{\{.*?\}\}`)
	commentRe   = regexp.MustCompile(`(?s)<!--.*?-->`)
	attrRe      = regexp.MustCompile(`\s*\{#([^}\s]+)[^}]*\}\s*$`)
	emptyHeadRe = regexp.MustCompile(`(?m)^#{1,6}[ \t]*$\n?`)
	tocRe       = regexp.MustCompile(`(?m)^Table of Contents:[ \t]*\n(?:[ \t]*\n)*(?:[ \t]*\[[^\n]*(?:\n|$))+`)
	imgRe       = regexp.MustCompile(`!\[([^\]]*)\]\([^)]*\)`)
	linkRe      = regexp.MustCompile(`\[([^\]]+)\]\([^)]*\)`)
	releaseRe   = regexp.MustCompile(`^(go1(\.[0-9]+)?|next)\.md$`)
)

func parseMarkdown(body []byte) ast.Node {
	return parser.New().Parse(body)
}

func isFence(line string) bool {
	t := strings.TrimSpace(line)
	return strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~")
}

// fenceUnbalanced reports whether a code fence is left open at the end of s.
func fenceUnbalanced(s string) bool {
	open := false
	for _, line := range strings.Split(s, "\n") {
		if isFence(line) {
			open = !open
		}
	}
	return open
}

// mapOutsideFences applies f to the parts of s outside fenced code blocks.
func mapOutsideFences(s string, f func(string) string) string {
	var out, buf strings.Builder
	inFence := false
	for _, line := range strings.SplitAfter(s, "\n") {
		if isFence(line) {
			if !inFence {
				out.WriteString(f(buf.String()))
				buf.Reset()
			}
			inFence = !inFence
			out.WriteString(line)
			continue
		}
		if inFence {
			out.WriteString(line)
		} else {
			buf.WriteString(line)
		}
	}
	out.WriteString(f(buf.String()))
	return out.String()
}

// cleanBody removes HTML comments and {{ templates }} outside code fences.
// Call it once, then pass its output to both parseMarkdown and chunkDoc so
// byte offsets stay consistent.
func cleanBody(body []byte) []byte {
	return []byte(mapOutsideFences(string(body), func(s string) string {
		s = commentRe.ReplaceAllString(s, "")
		return tmplRe.ReplaceAllString(s, "")
	}))
}

// cleanTitle splits "Overview {#overview}" into ("Overview", "overview").
func cleanTitle(s string) (title, id string) {
	if m := attrRe.FindStringSubmatch(s); m != nil {
		return strings.TrimSpace(s[:len(s)-len(m[0])]), m[1]
	}
	return strings.TrimSpace(s), ""
}

func nodeText(n ast.Node, src []byte) string {
	var sb strings.Builder
	ast.Walk(n, func(c ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch t := c.(type) {
		case *ast.Text:
			sb.WriteString(t.Value.Value(src))
		case *ast.CodeSpan:
			sb.WriteString(t.Value.Value(src))
		}
		return ast.WalkContinue, nil
	})
	return sb.String()
}

// cleanSection prepares a section's body: drops the ToC and bare "#" lines,
// and turns links and images into plain text, all outside code fences.
func cleanSection(s string) string {
	s = tocRe.ReplaceAllString(s, "")
	s = mapOutsideFences(s, func(p string) string {
		p = emptyHeadRe.ReplaceAllString(p, "")
		p = imgRe.ReplaceAllString(p, "$1")
		return linkRe.ReplaceAllString(p, "$1")
	})
	return strings.TrimSpace(s)
}

// chunkDoc splits the cleaned body at headings. Each chunk's text is the raw
// body between two heading lines, so the heading line is never in the text
// and the path always matches the content.
func chunkDoc(root ast.Node, body []byte, docTitle string) []Chunk {
	var marks []mark
	for n := range root.Children() {
		h, ok := n.(*ast.Heading)
		if !ok {
			continue
		}
		title, id := cleanTitle(nodeText(h, body))
		if title == "" {
			continue // bare "#" separator; cleanSection removes it from the text
		}
		// Snap to line boundaries so it works whether Pos() points at the
		// "#" or at the title text.
		pos := h.Pos()
		lineStart := bytes.LastIndexByte(body[:pos], '\n') + 1
		bodyStart := len(body)
		if i := bytes.IndexByte(body[pos:], '\n'); i >= 0 {
			bodyStart = pos + i + 1
		}
		marks = append(marks, mark{h.Level, title, id, lineStart, bodyStart})
	}

	var chunks []Chunk
	emit := func(path, anchor string, from, to int) {
		if from >= to {
			return
		}
		t := cleanSection(string(body[from:to]))
		if t == "" {
			return
		}
		if fenceUnbalanced(t) {
			fmt.Fprintf(os.Stderr, "fixed: closed open code fence in %q\n", path)
			t += "\n```"
		}
		chunks = append(chunks, splitBig(Chunk{Path: path, Anchor: anchor, Text: t})...)
	}

	// Text before the first heading belongs to the document itself.
	first := len(body)
	if len(marks) > 0 {
		first = marks[0].lineStart
	}
	emit(docTitle, "", 0, first)

	var stack []heading
	for i, m := range marks {
		for len(stack) > 0 && stack[len(stack)-1].level >= m.level {
			stack = stack[:len(stack)-1]
		}
		stack = append(stack, heading{m.level, m.title})

		var parts []string
		if docTitle != "" {
			parts = append(parts, docTitle)
		}
		for _, s := range stack {
			parts = append(parts, s.title)
		}

		to := len(body)
		if i+1 < len(marks) {
			to = marks[i+1].lineStart
		}
		emit(strings.Join(parts, " > "), m.anchor, m.bodyStart, to)
	}
	return chunks
}

// splitBig cuts chunks over maxWords at blank lines or </dl>, never inside a fence.
func splitBig(c Chunk) []Chunk {
	if len(strings.Fields(c.Text)) <= maxWords {
		return []Chunk{c}
	}
	var out []Chunk
	var cur, unit strings.Builder
	curWords := 0

	emit := func() {
		if t := strings.TrimSpace(cur.String()); t != "" {
			cc := c
			cc.Text = t
			out = append(out, cc)
		}
		cur.Reset()
		curWords = 0
	}
	flushUnit := func() {
		u := unit.String()
		unit.Reset()
		w := len(strings.Fields(u))
		if curWords+w > maxWords && curWords > 0 {
			emit()
		}
		cur.WriteString(u)
		curWords += w
	}

	inFence := false
	for _, line := range strings.SplitAfter(c.Text, "\n") {
		t := strings.TrimSpace(line)
		if isFence(line) {
			inFence = !inFence
		}
		unit.WriteString(line)
		big := len(strings.Fields(unit.String())) > maxWords/2
		if !inFence && (t == "" || strings.HasSuffix(t, "</dl>") || big) {
			flushUnit()
		}
	}
	flushUnit()
	emit()
	return out
}

func commonPath(a, b string) string {
	as, bs := strings.Split(a, " > "), strings.Split(b, " > ")
	i := 0
	for i < len(as) && i < len(bs) && as[i] == bs[i] {
		i++
	}
	return strings.Join(as[:i], " > ")
}

// mergeSmall joins a chunk with its predecessor when either is under minWords
// and the result fits in maxWords. Run it after chunkDoc.
func mergeSmall(in []Chunk) []Chunk {
	var out []Chunk
	for _, c := range in {
		if n := len(out); n > 0 {
			prev := &out[n-1]
			pw, cw := len(strings.Fields(prev.Text)), len(strings.Fields(c.Text))
			if (pw < minWords || cw < minWords) && pw+cw <= maxWords {
				if np := commonPath(prev.Path, c.Path); np != prev.Path {
					prev.Path = np
					prev.Anchor = ""
					prev.Merged = true
				}
				prev.Text += "\n\n" + c.Text
				continue
			}
		}
		out = append(out, c)
	}
	return out
}

// embedText is the exact string sent to the embedding model. Hash this same
// string for the cache so the hash and the embedding always see identical bytes.
func embedText(c Chunk) string {
	return "search_document: " + c.Path + "\n\n" + c.Text
}
