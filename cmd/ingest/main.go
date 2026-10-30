package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"os"

	"github.com/yuin/goldmark/v2/ast"
	"github.com/yuin/goldmark/v2/parser"
)

func splitMeta(path string) (map[string]any, []byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}

	meta := map[string]any{}

	trimmed := bytes.TrimLeft(data, "\t\r\n")
	if !bytes.HasPrefix(trimmed, []byte("<!--{")) {
		return meta, data, nil
	}

	end := bytes.Index(trimmed, []byte("}-->"))
	if end == -1 {
		return meta, data, nil
	}
	jsonPart := trimmed[len("<!--") : end+1]
	if err := json.Unmarshal(jsonPart, &meta); err != nil {
		return nil, nil, err
	}

	body := trimmed[end+len("}-->"):]

	return meta, body, nil

}

func parseMarkdown(body []byte) ast.Node {
	p := parser.New()
	return p.Parse(body)

}

func main() {
	meta, body, err := splitMeta("data/website/_content/doc/modules/layout.md")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(meta)

	root := parseMarkdown(body)

	ast.Walk(root, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}

		if h, ok := n.(*ast.Heading); ok {
			fmt.Println(h.Level, nodeText(h, body))
			return ast.WalkSkipChildren, nil
		}
		return ast.WalkContinue, nil

	})

}

func nodeText(n ast.Node, src []byte) string {

	var s string
	ast.Walk(n, func(c ast.Node, entering bool) (ast.WalkStatus, error) {
		if t, ok := c.(*ast.Text); ok && entering {
			s += t.Value.Value(src)
		}
		return ast.WalkContinue, nil
	})
	return s
}
