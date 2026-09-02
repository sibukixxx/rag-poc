package extractor

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/sibukixxx/rag-poc/internal/domain/knowledge"
)

// JSONLoader gives each element of a top-level array its own Page (same
// row-level granularity as CSVLoader); any other JSON shape becomes one
// pretty-printed Page.
type JSONLoader struct{}

var _ knowledge.Loader = JSONLoader{}

func (JSONLoader) Supports(filename, mimeType string) bool {
	ext := strings.ToLower(filepath.Ext(filename))
	return ext == ".json" || mimeType == "application/json"
}

func (JSONLoader) Load(_ context.Context, data []byte, _ knowledge.FileMeta) ([]knowledge.Page, error) {
	var arr []json.RawMessage
	if err := json.Unmarshal(data, &arr); err == nil {
		pages := make([]knowledge.Page, len(arr))
		for i, item := range arr {
			pages[i] = knowledge.Page{Number: i + 1, Text: flattenJSON(item)}
		}
		return pages, nil
	}

	var generic any
	if err := json.Unmarshal(data, &generic); err != nil {
		return nil, fmt.Errorf("json: %w", err)
	}
	pretty, err := json.MarshalIndent(generic, "", "  ")
	if err != nil {
		return nil, err
	}
	return []knowledge.Page{{Text: string(pretty)}}, nil
}

// flattenJSON renders one array element as "key: value" lines (objects)
// or its raw text (scalars), so it reads like the CSV row format.
func flattenJSON(raw json.RawMessage) string {
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err == nil {
		keys := make([]string, 0, len(obj))
		for k := range obj {
			keys = append(keys, k)
		}
		sort.Strings(keys)

		parts := make([]string, len(keys))
		for i, k := range keys {
			parts[i] = fmt.Sprintf("%s: %v", k, obj[k])
		}
		return strings.Join(parts, " | ")
	}
	return string(raw)
}
