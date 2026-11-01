package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strings"
)

func main() {
	dir := flag.String("data", "data/website/_content/doc", "content directory")
	limit := flag.Int("n", 20, "how many chunks to print")
	out := flag.String("out", "", "write chunks to this JSONL file")
	releases := flag.Bool("releases", false, "include release notes")
	flag.Parse()

	var all []Chunk

	err := filepath.WalkDir(*dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".md") {
			return nil
		}

		rel, _ := filepath.Rel(*dir, path)
		rel = filepath.ToSlash(rel)

		typ := "doc"
		if i := strings.Index(rel, "/"); i >= 0 {
			typ = rel[:i]
		}
		if releaseRe.MatchString(rel) {
			typ = "release-notes"
			if !*releases {
				return nil
			}
		}

		meta, body, err := splitMeta(path)
		if err != nil {
			log.Printf("skip %s: %v", path, err)
			return nil
		}
		body = cleanBody(body)

		title := metaTitle(meta)
		if title == "" {
			title = strings.TrimSuffix(filepath.Base(path), ".md")
		}

		root := parseMarkdown(body)
		// chunkDoc already runs splitBig; merge afterwards so tiny pieces
		// can join their neighbors before anything is dropped.
		chunks := mergeSmall(chunkDoc(root, body, title))

		slug := strings.TrimSuffix(rel, ".md")
		for _, idx := range []string{"/_index", "/index"} {
			slug = strings.TrimSuffix(slug, idx)
		}
		if slug == "index" || slug == "_index" {
			slug = ""
		}
		url := "/doc/" + slug

		n := 0
		for _, c := range chunks {
			if len(strings.Fields(c.Text)) < 10 {
				continue // stub left after merging, too small to embed usefully
			}
			c.ID = fmt.Sprintf("%s#%d", rel, n)
			n++
			c.File = rel
			c.URL = url
			if c.Anchor != "" {
				c.URL = url + "#" + c.Anchor
			}
			c.Type = typ
			all = append(all, c)
		}
		return nil
	})
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("%d chunks total\n\n", len(all))
	summary(all)
	problems(all)
	checkIDs(all)

	if *out != "" {
		if err := writeJSONL(*out, all); err != nil {
			log.Fatal(err)
		}
		fmt.Printf("wrote %d chunks to %s\n", len(all), *out)
	} else {
		printChunks(all[:min(*limit, len(all))])
	}
}

func summary(chunks []Chunk) {
	var tiny, huge, broken int
	for _, c := range chunks {
		w := len(strings.Fields(c.Text))
		if w < minWords {
			tiny++
		}
		if w > maxWords {
			huge++
		}
		if fenceUnbalanced(c.Text) {
			broken++
		}
	}
	fmt.Printf("tiny=%d huge=%d broken_fence=%d\n", tiny, huge, broken)
}

func problems(chunks []Chunk) {
	for _, c := range chunks {
		w := len(strings.Fields(c.Text))
		why := ""
		switch {
		case w > maxWords:
			why = "HUGE"
		case w < minWords:
			why = "TINY"
		}
		if fenceUnbalanced(c.Text) {
			why += " BROKEN_FENCE"
		}
		if why != "" {
			fmt.Printf("%-14s %4dw  %s | %s\n", why, w, c.File, c.Path)
		}
	}
}

// checkIDs reports duplicate ids and leftover markdown that should be gone.
func checkIDs(chunks []Chunk) {
	seen := map[string]bool{}
	for _, c := range chunks {
		if seen[c.ID] {
			fmt.Printf("DUPLICATE_ID %s\n", c.ID)
		}
		seen[c.ID] = true
		if strings.HasPrefix(strings.TrimSpace(c.Text), "Table of Contents") {
			fmt.Printf("TOC_LEFT %s\n", c.ID)
		}
	}
}

func printChunks(chunks []Chunk) {
	for _, c := range chunks {
		fmt.Printf("===== %s | %s | %d chars, %d words\n%s\n\n",
			c.ID, c.Path, len(c.Text), len(strings.Fields(c.Text)), c.Text)
	}
}

func writeJSONL(path string, chunks []Chunk) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	w := bufio.NewWriter(f)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	for _, c := range chunks {
		if err := enc.Encode(c); err != nil {
			return err
		}
	}
	return w.Flush()
}
