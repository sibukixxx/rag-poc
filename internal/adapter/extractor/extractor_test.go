package extractor_test

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/sibukixxx/rag-poc/internal/adapter/extractor"
	"github.com/sibukixxx/rag-poc/internal/domain/knowledge"
)

func TestTextLoader(t *testing.T) {
	l := extractor.TextLoader{}
	if !l.Supports("readme.md", "") {
		t.Error("expected .md to be supported")
	}
	if !l.Supports("notes.txt", "") {
		t.Error("expected .txt to be supported")
	}
	if l.Supports("data.csv", "") {
		t.Error("expected .csv to NOT be supported")
	}

	pages, err := l.Load(context.Background(), []byte("# Hello\n\nWorld"), knowledge.FileMeta{})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(pages) != 1 || pages[0].Text != "# Hello\n\nWorld" {
		t.Errorf("got pages %+v", pages)
	}
}

func TestHTMLLoaderStripsTagsAndScripts(t *testing.T) {
	l := extractor.HTMLLoader{}
	if !l.Supports("page.html", "") {
		t.Error("expected .html to be supported")
	}

	html := `<html><head><style>body{color:red}</style></head>
<body><h1>Title</h1><p>Hello <b>world</b>.</p><script>alert(1)</script></body></html>`
	pages, err := l.Load(context.Background(), []byte(html), knowledge.FileMeta{})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(pages) != 1 {
		t.Fatalf("expected 1 page, got %d", len(pages))
	}
	text := pages[0].Text
	if !strings.Contains(text, "Title") || !strings.Contains(text, "Hello") || !strings.Contains(text, "world") {
		t.Errorf("expected visible text preserved, got %q", text)
	}
	if strings.Contains(text, "alert(1)") {
		t.Errorf("expected script contents stripped, got %q", text)
	}
	if strings.Contains(text, "color:red") {
		t.Errorf("expected style contents stripped, got %q", text)
	}
}

func TestCSVLoaderOneRowPerPage(t *testing.T) {
	l := extractor.CSVLoader{}
	if !l.Supports("data.csv", "") {
		t.Error("expected .csv to be supported")
	}

	csv := "question,answer\n\"What is ForgeAI?\",\"A RAG platform\"\n\"Who made it?\",\"You\"\n"
	pages, err := l.Load(context.Background(), []byte(csv), knowledge.FileMeta{})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(pages) != 2 {
		t.Fatalf("expected 2 rows -> 2 pages, got %d", len(pages))
	}
	if pages[0].Number != 1 || !strings.Contains(pages[0].Text, "question: What is ForgeAI?") {
		t.Errorf("got page 0: %+v", pages[0])
	}
	if !strings.Contains(pages[1].Text, "answer: You") {
		t.Errorf("got page 1: %+v", pages[1])
	}
}

func TestJSONLoaderArrayOfObjects(t *testing.T) {
	l := extractor.JSONLoader{}
	if !l.Supports("data.json", "") {
		t.Error("expected .json to be supported")
	}

	body := `[{"name":"alice","age":30},{"name":"bob","age":25}]`
	pages, err := l.Load(context.Background(), []byte(body), knowledge.FileMeta{})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(pages) != 2 {
		t.Fatalf("expected 2 elements -> 2 pages, got %d", len(pages))
	}
	if !strings.Contains(pages[0].Text, "name: alice") || !strings.Contains(pages[0].Text, "age: 30") {
		t.Errorf("got page 0: %q", pages[0].Text)
	}
}

func TestJSONLoaderNonArrayFallsBackToPrettyPrint(t *testing.T) {
	l := extractor.JSONLoader{}
	pages, err := l.Load(context.Background(), []byte(`{"a":1,"b":2}`), knowledge.FileMeta{})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(pages) != 1 {
		t.Fatalf("expected 1 page for a non-array document, got %d", len(pages))
	}
	if !strings.Contains(pages[0].Text, `"a": 1`) {
		t.Errorf("expected pretty-printed JSON, got %q", pages[0].Text)
	}
}

// TestPDFLoaderRealFile extracts text from a real two-page PDF (generated
// with fpdf2, checked in under testdata/) to verify the best-effort
// extraction path against actual PDF bytes, not just the interface shape.
func TestPDFLoaderRealFile(t *testing.T) {
	l := extractor.PDFLoader{}
	if !l.Supports("sample.pdf", "") {
		t.Error("expected .pdf to be supported")
	}

	data, err := os.ReadFile("testdata/sample.pdf")
	if err != nil {
		t.Fatalf("reading testdata/sample.pdf: %v", err)
	}

	pages, err := l.Load(context.Background(), data, knowledge.FileMeta{Filename: "sample.pdf"})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(pages) != 2 {
		t.Fatalf("expected 2 pages, got %d", len(pages))
	}
	if pages[0].Number != 1 || !strings.Contains(pages[0].Text, "ForgeAI PDF Extraction Test") {
		t.Errorf("got page 1: %+v", pages[0])
	}
	if pages[1].Number != 2 || !strings.Contains(pages[1].Text, "page two") {
		t.Errorf("got page 2: %+v", pages[1])
	}
}

func TestRegistryDispatchesByExtension(t *testing.T) {
	reg := extractor.NewDefaultRegistry()

	cases := map[string]string{
		"a.txt":  "TextLoader",
		"a.md":   "TextLoader",
		"a.html": "HTMLLoader",
		"a.csv":  "CSVLoader",
		"a.json": "JSONLoader",
		"a.pdf":  "PDFLoader",
	}
	for filename := range cases {
		if _, ok := reg.Find(filename, ""); !ok {
			t.Errorf("expected a loader for %s", filename)
		}
	}

	if _, ok := reg.Find("a.exe", "application/octet-stream"); ok {
		t.Error("expected no loader for an unsupported extension")
	}
}
