// Package htmltext extracts readable text from HTML mail bodies.
//
// It intentionally supports a small, safe subset of HTML: scripts, styles and
// frames are dropped, while block elements become line or paragraph breaks. It
// is used for quoting the original message when the sender shipped no
// text/plain part.
package htmltext

import (
	"regexp"
	"strings"
	"unicode/utf8"

	"golang.org/x/net/html"
)

// maxInput caps how much markup is parsed so a hostile message cannot stall
// the UI.
const maxInput = 1 << 20

var (
	whitespace = regexp.MustCompile(`\s+`)
	// inlineSpaces collapses the runs that neighbouring inline nodes leave
	// behind: each node is collapsed on its own, so "a " + " " + "b" would keep
	// two spaces without a second pass.
	inlineSpaces = regexp.MustCompile(`[ \t]{2,}`)
	blankRuns    = regexp.MustCompile(`\n{3,}`)
)

// skipTags are dropped together with their content.
var skipTags = map[string]bool{
	"head": true, "script": true, "style": true, "title": true,
	"meta": true, "link": true, "noscript": true, "iframe": true,
	"object": true, "embed": true, "svg": true,
}

// paragraphTags are separated from their neighbours by a blank line.
var paragraphTags = map[string]bool{
	"p": true, "div": true, "section": true, "article": true,
	"header": true, "footer": true, "main": true, "blockquote": true,
	"pre": true, "table": true, "ul": true, "ol": true,
	"h1": true, "h2": true, "h3": true, "h4": true, "h5": true, "h6": true,
}

// lineTags start a new line without leaving a blank line behind.
var lineTags = map[string]bool{"li": true, "tr": true}

// ToText renders the given HTML as plain text with block elements turned into
// line or paragraph breaks.
func ToText(src string) string {
	if len(src) > maxInput {
		src = truncateRunes(src, maxInput)
	}
	doc, err := html.Parse(strings.NewReader(src))
	if err != nil {
		return strings.TrimSpace(src)
	}
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		switch n.Type {
		case html.TextNode:
			text := strings.ReplaceAll(n.Data, "\u00a0", " ")
			b.WriteString(whitespace.ReplaceAllString(text, " "))
			return
		case html.ElementNode:
			if skipTags[n.Data] {
				return
			}
			switch {
			case n.Data == "br":
				b.WriteString("\n")
				return
			case paragraphTags[n.Data]:
				b.WriteString("\n\n")
			case lineTags[n.Data]:
				b.WriteString("\n")
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
		if n.Type == html.ElementNode && paragraphTags[n.Data] {
			b.WriteString("\n\n")
		}
	}
	walk(doc)

	out := inlineSpaces.ReplaceAllString(b.String(), " ")
	out = strings.ReplaceAll(out, " \n", "\n")
	out = blankRuns.ReplaceAllString(out, "\n\n")
	return strings.TrimSpace(out)
}

// truncateRunes cuts src to at most limit bytes without splitting a UTF-8
// sequence, which would otherwise turn the tail of the quoted message into a
// replacement character.
func truncateRunes(src string, limit int) string {
	if len(src) <= limit {
		return src
	}
	cut := limit
	for cut > 0 && !utf8.RuneStart(src[cut]) {
		cut--
	}
	return src[:cut]
}
