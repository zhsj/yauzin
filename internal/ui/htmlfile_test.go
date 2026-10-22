package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/net/html"
)

func TestInjectHeadMeta(t *testing.T) {
	tests := []struct {
		name string
		src  string
		// wantText must survive somewhere in the rendered document.
		wantText string
	}{
		{
			name:     "existing head",
			src:      `<html><head><title>t</title></head><body><p>body</p></body></html>`,
			wantText: "body",
		},
		{
			name:     "head with attributes and uppercase",
			src:      `<HTML><HEAD lang="zh"><body><p>body</p></body></HTML>`,
			wantText: "body",
		},
		{
			name:     "html without head",
			src:      `<html><body><p>body</p></body></html>`,
			wantText: "body",
		},
		{
			name:     "fragment",
			src:      `<div><p>body</p></div>`,
			wantText: "body",
		},
		{
			name:     "text only",
			src:      `hello`,
			wantText: "hello",
		},
		{
			// A head written inside a comment must not decide where the policy
			// goes: the script after it still has to be preceded by the CSP.
			name:     "fake head in a comment before a script",
			src:      `<!-- <head> --><script>alert(1)</script><p>body</p>`,
			wantText: "body",
		},
		{
			// The parser moves a leading script into the head, so the policy
			// must be inserted in front of it rather than after the head tag.
			name:     "script before the head tag",
			src:      `<script>alert(1)</script><html><head><title>t</title></head><body>b</body></html>`,
			wantText: "b",
		},
		{
			// A message can ship its own policy; ours must still come first and
			// the strictest policy is the one the browser enforces.
			name:     "message declares its own policy",
			src:      `<html><head><meta http-equiv="Content-Security-Policy" content="default-src *"><script>x()</script></head><body>b</body></html>`,
			wantText: "b",
		},
		{
			name:     "malformed html",
			src:      `<html><head><p>no close`,
			wantText: "no close",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := injectHeadMeta(tt.src)
			if strings.Contains(got, "script-src") {
				t.Fatalf("scripts must stay blocked:\n%s", got)
			}

			doc := parseDocument(t, got)
			head := findHead(doc)
			if head == nil {
				t.Fatalf("injected document has no head:\n%s", got)
			}
			first := elementChild(head, 0)
			if !isMeta(first, "charset", "utf-8") {
				t.Fatalf("charset meta is not the first head element child:\n%s", got)
			}
			if second := elementChild(head, 1); !isMeta(second, "http-equiv", "Content-Security-Policy") ||
				metaAttr(second, "content") != cspContent {
				t.Fatalf("CSP meta is not the second head element child:\n%s", got)
			}

			nodes := elementsInOrder(doc)
			firstPolicy, firstScript, strict := -1, -1, 0
			for i, n := range nodes {
				if n.Data == "script" && firstScript < 0 {
					firstScript = i
				}
				if !isMeta(n, "http-equiv", "Content-Security-Policy") {
					continue
				}
				if firstPolicy < 0 {
					firstPolicy = i
				}
				if metaAttr(n, "content") == cspContent {
					strict++
				}
			}
			if firstPolicy < 0 {
				t.Fatalf("no CSP meta in the injected document:\n%s", got)
			}
			if strict != 1 {
				t.Fatalf("injected %d strict CSP metas, want 1:\n%s", strict, got)
			}
			if firstScript >= 0 && firstScript < firstPolicy {
				t.Fatalf("a script precedes the CSP meta:\n%s", got)
			}
			if text := nodeText(doc); !strings.Contains(text, tt.wantText) {
				t.Fatalf("body text lost %q, got %q:\n%s", tt.wantText, text, got)
			}
		})
	}
}

// TestInjectCharsetOnly pins the unrestricted preview: the encoding is
// declared, but no policy is added, so the message runs with whatever it ships.
func TestInjectCharsetOnly(t *testing.T) {
	src := `<html><head><title>t</title></head><body><script>alert(1)</script><p>body</p></body></html>`
	got := injectCharsetOnly(src)

	doc := parseDocument(t, got)
	head := findHead(doc)
	if head == nil {
		t.Fatalf("injected document has no head:\n%s", got)
	}
	if !isMeta(elementChild(head, 0), "charset", "utf-8") {
		t.Fatalf("charset meta is not the first head element child:\n%s", got)
	}
	for _, n := range elementsInOrder(doc) {
		if isMeta(n, "http-equiv", "Content-Security-Policy") {
			t.Fatalf("unsafe preview injected a policy:\n%s", got)
		}
	}
	if !strings.Contains(got, "alert(1)") {
		t.Fatalf("unsafe preview dropped the script:\n%s", got)
	}
	if text := nodeText(doc); !strings.Contains(text, "body") {
		t.Fatalf("body text lost, got %q:\n%s", text, got)
	}

	// A message that ships its own policy keeps it: the unrestricted preview
	// only refrains from adding one.
	own := injectCharsetOnly(`<html><head><meta http-equiv="Content-Security-Policy" content="default-src *"><script>x()</script></head><body>b</body></html>`)
	if !strings.Contains(own, "default-src *") {
		t.Fatalf("unsafe preview dropped the message's own policy:\n%s", own)
	}
}

func parseDocument(t *testing.T, src string) *html.Node {
	t.Helper()
	doc, err := html.Parse(strings.NewReader(src))
	if err != nil {
		t.Fatalf("parse output: %v", err)
	}
	return doc
}

func elementsInOrder(n *html.Node) []*html.Node {
	var out []*html.Node
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			out = append(out, n)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return out
}

func elementChild(n *html.Node, index int) *html.Node {
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type != html.ElementNode {
			continue
		}
		if index == 0 {
			return c
		}
		index--
	}
	return nil
}

func isMeta(n *html.Node, key, want string) bool {
	if n == nil || n.Type != html.ElementNode || n.Data != "meta" {
		return false
	}
	return strings.EqualFold(metaAttr(n, key), want)
}

func metaAttr(n *html.Node, key string) string {
	if n == nil {
		return ""
	}
	for _, attr := range n.Attr {
		if strings.EqualFold(attr.Key, key) {
			return attr.Val
		}
	}
	return ""
}

func nodeText(n *html.Node) string {
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.TextNode {
			b.WriteString(n.Data)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return b.String()
}

func TestMessageHTMLName(t *testing.T) {
	src := `<p>hello</p>`

	tests := []struct {
		name string
		id   string
	}{
		{name: "regular id", id: "demo-gmail-t000-m0"},
		{name: "path traversal", id: "../../etc/passwd"},
		{name: "absolute path", id: "/tmp/evil"},
		{name: "empty id", id: ""},
		{name: "dots only", id: ".."},
		{name: "unicode id", id: "邮件-001"},
		{name: "very long id", id: strings.Repeat("a", 200)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := messageHTMLName(tt.id, src)

			if base := filepath.Base(got); base != got {
				t.Fatalf("name %q contains a separator", got)
			}
			if strings.ContainsAny(got, `/\`) {
				t.Fatalf("name %q contains a separator", got)
			}
			if !strings.HasSuffix(got, ".html") {
				t.Fatalf("name %q has no .html suffix", got)
			}
			prefix := strings.TrimSuffix(got, ".html")
			prefix = prefix[:strings.LastIndex(prefix, "-")]
			if len(prefix) > 64 {
				t.Fatalf("prefix %q is %d bytes, want at most 64", prefix, len(prefix))
			}
		})
	}

	if a, b := messageHTMLName("same", src), messageHTMLName("same", src); a != b {
		t.Fatalf("same id and body produced different names: %q and %q", a, b)
	}
	if a, b := messageHTMLName("same", src), messageHTMLName("same", src+"<!-- -->"); a == b {
		t.Fatalf("different bodies share the name %q", a)
	}
	if got := messageHTMLName("", src); !strings.HasPrefix(got, "message-") {
		t.Fatalf("empty id produced %q, want a message- prefix", got)
	}
}

func TestWriteMessageHTML(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested")
	src := `<div style="color:#202124"><p>你好，世界</p></div>`

	path, err := writeMessageHTML(dir, "demo-gmail-t000-m0", src)
	if err != nil {
		t.Fatalf("writeMessageHTML: %v", err)
	}
	if filepath.Dir(path) != dir {
		t.Fatalf("file written to %q, want %q", filepath.Dir(path), dir)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	doc := parseDocument(t, string(data))
	if !strings.Contains(nodeText(doc), "你好，世界") {
		t.Fatalf("written document lost the body text:\n%s", data)
	}
	if head := findHead(doc); !isMeta(elementChild(head, 1), "http-equiv", "Content-Security-Policy") {
		t.Fatalf("written document lost the CSP meta:\n%s", data)
	}
}

// TestWriteMessageHTMLUnsafe covers the second written document of a message:
// its own name, no injected policy, and markup left untouched.
func TestWriteMessageHTMLUnsafe(t *testing.T) {
	dir := t.TempDir()
	src := `<html><head><title>t</title></head><body><script>alert(1)</script><p>你好</p></body></html>`

	safe, err := writeMessageHTML(dir, "demo-gmail-t000-m0", src)
	if err != nil {
		t.Fatalf("writeMessageHTML: %v", err)
	}
	unsafe, err := writeMessageHTMLUnsafe(dir, "demo-gmail-t000-m0", src)
	if err != nil {
		t.Fatalf("writeMessageHTMLUnsafe: %v", err)
	}
	if safe == unsafe {
		t.Fatalf("both modes share the name %q", safe)
	}
	if !strings.HasSuffix(unsafe, "-unsafe.html") {
		t.Fatalf("unsafe document named %q, want an -unsafe suffix", unsafe)
	}

	data, err := os.ReadFile(unsafe)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if strings.Contains(string(data), "Content-Security-Policy") {
		t.Fatalf("unsafe document still carries a policy:\n%s", data)
	}
	if !strings.Contains(string(data), "alert(1)") || !strings.Contains(string(data), "你好") {
		t.Fatalf("unsafe document lost its markup:\n%s", data)
	}
}
