package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/net/html"
)

// TestSourcePageShowsHeadersAndSource pins the shape of the original message
// page: the headers first, then the whole source as text.
func TestSourcePageShowsHeadersAndSource(t *testing.T) {
	meta := sourceMeta{
		MessageID: "18c0ffee",
		Account:   "user@example.com",
		From:      "Alice <alice@example.com>",
		To:        "user@example.com",
		Subject:   "Quarterly report",
		Date:      "Fri, 23 Oct 2026 02:00:00 +0800",
	}
	raw := []byte("Received: from mx.example.com\r\nMessage-ID: <m1@example.com>\r\n\r\n<b>body</b>\r\n")

	page := sourcePage(meta, raw)
	doc := parseDocument(t, page)
	if head := findHead(doc); !isMeta(elementChild(head, 0), "charset", "utf-8") {
		t.Fatalf("source page has no leading charset meta:\n%s", page)
	}

	text := nodeText(doc)
	for _, want := range []string{
		"18c0ffee",
		"user@example.com",
		"Alice <alice@example.com>",
		"Quarterly report",
		"Received: from mx.example.com",
		"Message-ID: <m1@example.com>",
		"<b>body</b>",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("source page lost %q:\n%s", want, text)
		}
	}
	// The source is text: nothing in it may become markup of the page.
	for _, n := range elementsInOrder(doc) {
		if n.Data == "script" || n.Data == "b" || n.Data == "img" {
			t.Fatalf("source was parsed as markup (%s):\n%s", n.Data, page)
		}
	}
}

// TestSourcePageEscapesHostileSource keeps the page inert even when the message
// claims to be HTML.
func TestSourcePageEscapesHostileSource(t *testing.T) {
	page := sourcePage(sourceMeta{MessageID: "m1"}, []byte(`<script>alert(1)</script><img src=x onerror="y()">`))

	for _, n := range elementsInOrder(parseDocument(t, page)) {
		switch n.Data {
		case "script", "img":
			t.Fatalf("hostile source became a %s element:\n%s", n.Data, page)
		}
	}
	if !strings.Contains(nodeText(parseDocument(t, page)), `<script>alert(1)</script>`) {
		t.Fatalf("escaped source is not readable:\n%s", page)
	}
	doc := parseDocument(t, page)
	policy := ""
	for _, n := range elementsInOrder(doc) {
		if isMeta(n, "http-equiv", "Content-Security-Policy") {
			policy = metaAttr(n, "content")
		}
	}
	if policy != sourceCSP || !strings.Contains(policy, "default-src 'none'") {
		t.Fatalf("source page policy = %q, want %q", policy, sourceCSP)
	}
}

// TestSourcePageToleratesInvalidUTF8 keeps a broken body from producing a
// document the browser has to guess the encoding of.
func TestSourcePageToleratesInvalidUTF8(t *testing.T) {
	page := sourcePage(sourceMeta{MessageID: "m1"}, []byte{0xff, 0xfe, 'a', 'b'})
	if !strings.Contains(page, "ab") {
		t.Fatalf("valid bytes lost: %q", page)
	}
	// Parsing must succeed; a replacement rune is acceptable, invalid bytes are
	// not carried into the document.
	if strings.ContainsRune(page, '\uFFFE') {
		t.Fatalf("invalid rune survived: %q", page)
	}
}

// TestSourcePageDropsEmptyRows keeps the header table to the fields the
// message actually has.
func TestSourcePageDropsEmptyRows(t *testing.T) {
	rows := sourceRows(sourceMeta{MessageID: "m1"})
	empty := 0
	for _, row := range rows {
		if strings.TrimSpace(row[1]) == "" {
			empty++
		}
	}
	if empty != len(rows)-1 {
		t.Fatalf("test needs exactly one filled row, got %d of %d", len(rows)-empty, len(rows))
	}

	page := sourcePage(sourceMeta{MessageID: "m1"}, []byte("raw"))
	if got := strings.Count(page, "<tr>"); got != 1 {
		t.Fatalf("header table has %d rows, want 1:\n%s", got, page)
	}
}

// TestWriteMessageSource writes the page next to the previews without
// overwriting either of them.
func TestWriteMessageSource(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested")
	meta := sourceMeta{MessageID: "demo-gmail-t000-m0", Subject: "你好"}
	raw := []byte("Subject: 你好\r\n\r\nbody\r\n")

	path, err := writeMessageSource(dir, meta.MessageID, meta, raw)
	if err != nil {
		t.Fatalf("writeMessageSource: %v", err)
	}
	if filepath.Dir(path) != dir {
		t.Fatalf("file written to %q, want %q", filepath.Dir(path), dir)
	}
	if !strings.HasSuffix(path, "-source.html") {
		t.Fatalf("source page named %q, want a -source suffix", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if !strings.Contains(string(data), "Subject: 你好") {
		t.Fatalf("source page lost the source:\n%s", data)
	}

	preview, err := writeMessageHTML(dir, meta.MessageID, `<div>你好</div>`)
	if err != nil {
		t.Fatalf("writeMessageHTML: %v", err)
	}
	unsafe, err := writeMessageHTMLUnsafe(dir, meta.MessageID, `<div>你好</div>`)
	if err != nil {
		t.Fatalf("writeMessageHTMLUnsafe: %v", err)
	}
	if path == preview || path == unsafe || preview == unsafe {
		t.Fatalf("documents share names: %q %q %q", path, preview, unsafe)
	}
}

// TestSourcePageIsValidHTML guards the hand built document against unbalanced
// markup: the parser has to end up with exactly one body and one pre.
func TestSourcePageIsValidHTML(t *testing.T) {
	doc := parseDocument(t, sourcePage(sourceMeta{MessageID: "m1"}, []byte("a < b > c & d")))
	counts := map[string]int{}
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			counts[n.Data]++
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	if counts["body"] != 1 || counts["table"] != 1 || counts["pre"] != 1 || counts["html"] != 1 {
		t.Fatalf("document structure = %v", counts)
	}
}
