package ui

import (
	"html"
	"strings"

	"github.com/zhsj/yauzin/internal/i18n"
)

// sourceMeta is the header block above the raw source. Every field is already
// formatted for display: the page only escapes it.
type sourceMeta struct {
	MessageID string
	Account   string
	From      string
	To        string
	Cc        string
	Subject   string
	Date      string
}

// sourceCSP keeps the page inert: the source is displayed, never executed, and
// only the inline stylesheet the page ships is allowed.
const sourceCSP = "default-src 'none'; style-src 'unsafe-inline'"

// sourceStyle is the stylesheet of the page. It is inline because the file has
// no sibling assets, and the policy above allows exactly that.
const sourceStyle = `
body{margin:0;padding:24px;background:#f6f7f9;color:#202124;
font:14px/1.5 -apple-system,"Segoe UI",Roboto,"Helvetica Neue",Arial,sans-serif}
main{max-width:960px;margin:0 auto}
h1{margin:0 0 16px;font-size:18px;font-weight:600;word-break:break-word}
table{border-collapse:collapse;width:100%;background:#fff;
border:1px solid #dadce0;border-radius:8px;overflow:hidden}
th,td{padding:8px 12px;text-align:left;vertical-align:top;
border-bottom:1px solid #e8eaed;font-size:13px}
tr:last-child th,tr:last-child td{border-bottom:0}
th{width:160px;font-weight:500;color:#5f6368;white-space:nowrap}
td{word-break:break-word}
pre{margin:16px 0 0;padding:16px;background:#fff;border:1px solid #dadce0;
border-radius:8px;white-space:pre-wrap;word-break:break-all;
font:12px/1.6 "SFMono-Regular",Consolas,"Liberation Mono",Menlo,monospace}
`

// sourcePage renders one original message: the headers worth checking first,
// then the whole RFC 822 source as preformatted text. The source is escaped and
// never handed to the parser, so a hostile message can only ever show up as
// text on this page.
func sourcePage(meta sourceMeta, raw []byte) string {
	title := sourceTitle(meta)
	var b strings.Builder
	b.WriteString("<!DOCTYPE html><html><head>")
	b.WriteString(`<meta charset="utf-8">`)
	b.WriteString(`<meta http-equiv="Content-Security-Policy" content="` + sourceCSP + `">`)
	b.WriteString(`<title>` + html.EscapeString(title) + `</title>`)
	b.WriteString(`<style>` + sourceStyle + `</style>`)
	b.WriteString("</head><body><main>")
	b.WriteString(`<h1>` + html.EscapeString(title) + `</h1>`)

	b.WriteString(`<table>`)
	for _, row := range sourceRows(meta) {
		if strings.TrimSpace(row[1]) == "" {
			continue
		}
		b.WriteString(`<tr><th>` + html.EscapeString(row[0]) + `</th><td>` + html.EscapeString(row[1]) + `</td></tr>`)
	}
	b.WriteString(`</table>`)

	b.WriteString(`<pre>`)
	b.WriteString(html.EscapeString(strings.ToValidUTF8(string(raw), "\uFFFD")))
	b.WriteString(`</pre>`)
	b.WriteString("</main></body></html>")
	return b.String()
}

// sourceTitle is the page and document title: the subject, or a generic label
// when the message has none.
func sourceTitle(meta sourceMeta) string {
	if subject := strings.TrimSpace(meta.Subject); subject != "" {
		return subject
	}
	return i18n.T("source.title")
}

// sourceRows lists the header rows in reading order. Empty values are dropped
// while rendering, so a message without Cc does not show an empty row.
func sourceRows(meta sourceMeta) [][2]string {
	return [][2]string{
		{i18n.T("source.messageId"), meta.MessageID},
		{i18n.T("source.account"), meta.Account},
		{i18n.T("source.from"), meta.From},
		{i18n.T("source.to"), meta.To},
		{i18n.T("source.cc"), meta.Cc},
		{i18n.T("source.subject"), meta.Subject},
		{i18n.T("source.date"), meta.Date},
	}
}

// writeMessageSource stores the source page in dir and returns the path that
// can be handed to the system browser.
func writeMessageSource(dir, id string, meta sourceMeta, raw []byte) (string, error) {
	return writeHTMLFile(dir, messageHTMLNameSuffix(id, string(raw), "source"), sourcePage(meta, raw))
}
