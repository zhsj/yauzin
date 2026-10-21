package ui

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"golang.org/x/net/html"
)

// cspContent keeps a message opened in the browser inert: no scripts, no
// remote resources, no form submission and no base URI override. Inline styles
// and data: images stay allowed so the layout survives.
const cspContent = "default-src 'none'; img-src data:; style-src 'unsafe-inline'; font-src data:; form-action 'none'; base-uri 'none'"

var unsafeFileChars = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// headMeta declares the encoding of the file we write and locks the document
// down before any of its own markup is parsed.
func headMeta() string {
	return `<meta charset="utf-8"><meta http-equiv="Content-Security-Policy" content="` + cspContent + `">`
}

// injectHeadMeta adds the charset and CSP meta elements to the document head
// without otherwise rewriting the message. Fragments are wrapped into a full
// document because a meta element outside of a head is ignored by browsers.
//
// The document is parsed instead of scanned for "<head>": a fake head inside a
// comment or a script string must not decide where the policy goes, and the
// policy has to precede every element the browser could execute. Parsing
// serialises the message again (tag names lowercased, attribute quoting
// normalised, conditional comments for Outlook dropped); that is an accepted
// trade for the guarantee, and it is why the message is only ever rendered by a
// browser through this path.
func injectHeadMeta(src string) string {
	doc, err := html.Parse(strings.NewReader(src))
	if err != nil {
		return wrapperDocument(src)
	}
	head := findHead(doc)
	if head == nil {
		return wrapperDocument(src)
	}
	// Both elements go in front of the current first child, so the policy is
	// seen before anything the parser placed in the head, including a <script>
	// that appeared before the <head> tag in the source.
	first := head.FirstChild
	head.InsertBefore(metaElement("charset", "utf-8"), first)
	head.InsertBefore(metaElement("http-equiv", "Content-Security-Policy", "content", cspContent), first)

	var out strings.Builder
	if err := html.Render(&out, doc); err != nil {
		return wrapperDocument(src)
	}
	return out.String()
}

// wrapperDocument locks down a message the parser could not handle. The policy
// is the first thing in the document, so nothing in src can run before it;
// stray <head> and <body> tags inside the body are ignored by the parser.
func wrapperDocument(src string) string {
	return "<!DOCTYPE html><html><head>" + headMeta() + "</head><body>" + src + "</body></html>"
}

// metaElement builds a meta element from key/value attribute pairs.
func metaElement(attrs ...string) *html.Node {
	n := &html.Node{Type: html.ElementNode, Data: "meta"}
	for i := 0; i+1 < len(attrs); i += 2 {
		n.Attr = append(n.Attr, html.Attribute{Key: attrs[i], Val: attrs[i+1]})
	}
	return n
}

// findHead returns the head element html.Parse put in the document.
func findHead(doc *html.Node) *html.Node {
	for n := doc.FirstChild; n != nil; n = n.NextSibling {
		if n.Type != html.ElementNode || n.Data != "html" {
			continue
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			if c.Type == html.ElementNode && c.Data == "head" {
				return c
			}
		}
	}
	return nil
}

// messageHTMLName builds a collision free file name. The id is reduced to a
// safe character set so a hostile backend cannot escape the temp directory and
// the content hash keeps different bodies of the same id apart.
func messageHTMLName(id, src string) string {
	name := unsafeFileChars.ReplaceAllString(id, "_")
	name = strings.Trim(name, "._")
	if name == "" {
		name = "message"
	}
	if len(name) > 64 {
		name = name[:64]
	}
	sum := sha256.Sum256([]byte(src))
	return name + "-" + hex.EncodeToString(sum[:8]) + ".html"
}

// writeMessageHTML stores the HTML body in dir and returns the path that can
// be handed to the system browser.
func writeMessageHTML(dir, id, src string) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, messageHTMLName(id, src))
	if err := os.WriteFile(path, []byte(injectHeadMeta(src)), 0o644); err != nil {
		return "", err
	}
	return path, nil
}
