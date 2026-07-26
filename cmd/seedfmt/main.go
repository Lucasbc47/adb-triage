// Command seedfmt rewrites internal/classify/seed.json in the hand-curated
// layout: one entry per line, colons aligned in a column, and a blank line
// wherever the category changes from the entry above. Editors that "clean up"
// JSON on save (pretty-print, re-indent) flatten that layout; run this
// afterward to restore it. Key order and grouping are read from the file
// itself, so it never reorders entries.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
)

const path = "internal/classify/seed.json"

type entry struct {
	Label    string `json:"label"`
	Category string `json:"category"`
}

type kv struct {
	key string
	e   entry
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "erro:", err)
		os.Exit(1)
	}
}

func run() error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	kvs, err := decodeOrdered(data)
	if err != nil {
		return err
	}

	out, err := render(kvs)
	if err != nil {
		return err
	}

	return os.WriteFile(path, out, 0o644)
}

// decodeOrdered walks the JSON token by token instead of unmarshaling into a
// map, since a map would discard the insertion order the grouping depends on.
func decodeOrdered(data []byte) ([]kv, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	if _, err := dec.Token(); err != nil { // opening '{'
		return nil, err
	}

	var kvs []kv
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		key := tok.(string)

		var e entry
		if err := dec.Decode(&e); err != nil {
			return nil, fmt.Errorf("%s: %w", key, err)
		}
		kvs = append(kvs, kv{key, e})
	}
	if _, err := dec.Token(); err != nil { // closing '}'
		return nil, err
	}
	return kvs, nil
}

func render(kvs []kv) ([]byte, error) {
	// The alignment column is set by the single longest key, so every shorter
	// key pads out to line up with it (with at least one space to spare).
	col := 0
	for _, e := range kvs {
		if w := len(e.key) + len(`"":`); w > col {
			col = w
		}
	}
	col++

	var b bytes.Buffer
	b.WriteString("{\n")
	for i, e := range kvs {
		if i > 0 && e.e.Category != kvs[i-1].e.Category {
			b.WriteString("\n")
		}

		label, err := json.Marshal(e.e.Label)
		if err != nil {
			return nil, err
		}
		category, err := json.Marshal(e.e.Category)
		if err != nil {
			return nil, err
		}

		keyField := fmt.Sprintf("%q:", e.key)
		fmt.Fprintf(&b, "  %-*s {\"label\": %s, \"category\": %s},\n", col, keyField, label, category)
	}

	out := b.Bytes()
	// Drop the trailing comma on the last entry: it's the only one that isn't
	// followed by another line.
	if i := bytes.LastIndex(out, []byte(",\n")); i >= 0 {
		out = append(out[:i], out[i+1:]...)
	}
	out = append(out, '}', '\n')
	return out, nil
}
