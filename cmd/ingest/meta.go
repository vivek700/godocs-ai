package main

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

func splitMeta(path string) (map[string]any, []byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}

	data = bytes.ReplaceAll(data, []byte("\r\n"), []byte("\n"))
	meta := map[string]any{}

	trimmed := bytes.TrimLeft(data, "\t\n")

	switch {
	case bytes.HasPrefix(trimmed, []byte("<!--{")):
		end := bytes.Index(trimmed, []byte("}-->"))
		if end == -1 {
			return meta, trimmed, nil
		}

		jsonPart := trimmed[len("<!--") : end+1]
		if err := json.Unmarshal(jsonPart, &meta); err != nil {
			return nil, nil, err
		}
		body := trimmed[end+len("}-->"):]

		return meta, body, nil

	case bytes.HasPrefix(trimmed, []byte("---\n")):
		rest := trimmed[4:]
		end := bytes.Index(rest, []byte("\n---"))
		if end == -1 {
			return meta, trimmed, nil
		}
		if err := yaml.Unmarshal(rest[:end], &meta); err != nil {
			return nil, nil, err
		}
		body := rest[end+len("\n---"):]
		if i := bytes.IndexByte(body, '\n'); i >= 0 {
			return meta, body[i+1:], nil
		}
		return meta, nil, nil
	}
	return meta, trimmed, nil
}

func metaTitle(meta map[string]any) string {
	for k, v := range meta {
		if strings.EqualFold(k, "title") {
			s, _ := v.(string)
			return s
		}
	}
	return ""

}
