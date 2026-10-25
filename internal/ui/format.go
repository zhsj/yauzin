package ui

import (
	"strings"
	"time"

	"github.com/zhsj/yauzin/internal/i18n"
	"github.com/zhsj/yauzin/internal/mail"
)

// folderDisplayName localizes the system folders. User created labels keep the
// name supplied by the backend because it is data, not UI copy.
func folderDisplayName(f mail.Folder) string {
	switch f.Kind {
	case mail.FolderInbox:
		return i18n.T("folder.inbox")
	case mail.FolderStarred:
		return i18n.T("folder.starred")
	case mail.FolderSent:
		return i18n.T("folder.sent")
	case mail.FolderSpam:
		return i18n.T("folder.spam")
	case mail.FolderTrash:
		return i18n.T("folder.trash")
	default:
		return f.Name
	}
}

// formatListTime renders the compact timestamp of a conversation row.
func formatListTime(t time.Time) string {
	return formatListTimeAt(t, time.Now())
}

// formatListTimeAt is the testable form of formatListTime.
func formatListTimeAt(t, now time.Time) string {
	if t.IsZero() {
		return ""
	}
	local := t.In(now.Location())
	switch {
	case local.Year() == now.Year() && local.YearDay() == now.YearDay():
		return local.Format(i18n.T("date.today"))
	case local.Year() == now.Year():
		return local.Format(i18n.T("date.thisYear"))
	default:
		return local.Format(i18n.T("date.older"))
	}
}

// formatFullTime renders the expanded timestamp shown on an opened message. The
// list already renders in local time, so the expanded form has to use the same
// zone or one message appears to move by the sender's offset.
func formatFullTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.In(time.Local).Format(i18n.T("date.full"))
}

// The quote header is part of the message a recipient reads, not UI copy, so it
// is fixed English in every interface language, the way Gmail writes it.
// Localizing it would quote the same mail differently depending on the reader's
// settings.
const (
	quoteTimeLayout   = "Mon, Jan 2, 2006 at 3:04 PM"
	quoteNoBody       = "(no body)"
	forwardHeaderLine = "---------- Forwarded message ----------"
)

// formatQuoteTime renders the timestamp of a reply or forward header in the
// same local zone as the rest of the app, but with a fixed English layout.
func formatQuoteTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.In(time.Local).Format(quoteTimeLayout)
}

// quoteSender names the sender in a quote header. Address.String already
// handles the name-only, address-only and quoted-name cases.
func quoteSender(a mail.Address) string {
	return strings.TrimSpace(a.String())
}

// replyHeader is the line Gmail opens a reply with: the time first, then the
// sender, or whichever of the two exists.
func replyHeader(m *mail.Message) string {
	when, from := formatQuoteTime(m.Date), quoteSender(m.From)
	switch {
	case when != "" && from != "":
		return "On " + when + " " + from + " wrote:"
	case when != "":
		return "On " + when + " wrote:"
	case from != "":
		return from + " wrote:"
	default:
		return "wrote:"
	}
}

// quoteLines prefixes every line with the plain-text quote marker. Blank lines
// carry a bare ">" as Gmail does, so no line ends in trailing whitespace.
func quoteLines(body string) string {
	body = strings.ReplaceAll(body, "\r\n", "\n")
	body = strings.ReplaceAll(body, "\r", "\n")
	lines := strings.Split(strings.TrimSpace(body), "\n")
	var b strings.Builder
	b.Grow(len(body) + 2*len(lines))
	for i, line := range lines {
		if i > 0 {
			b.WriteByte('\n')
		}
		if strings.TrimSpace(line) == "" {
			b.WriteByte('>')
			continue
		}
		b.WriteString("> ")
		b.WriteString(line)
	}
	return b.String()
}

// plainText is the part of m that can be quoted. Only text/plain counts: an
// HTML-only message has no body, and its HTML is never parsed or converted to
// text.
func plainText(m *mail.Message) string {
	return strings.TrimSpace(m.TextBody)
}

// quotedBody returns the unmarked body of a forwarded message. An HTML-only
// message has no body to quote.
func quotedBody(m *mail.Message) string {
	if plainText(m) == "" {
		return quoteNoBody
	}
	return m.TextBody
}

// replyQuote builds the quoted block a reply starts with. A selection quoted
// from the reader replaces the body, but only when there is a plain-text body
// to select from: the placeholder shown for an HTML-only message is not mail.
func replyQuote(m *mail.Message, selection string) string {
	var quoted string
	switch body := plainText(m); {
	case body == "":
		quoted = quoteNoBody
	case strings.TrimSpace(selection) != "":
		quoted = quoteLines(selection)
	default:
		quoted = quoteLines(body)
	}
	return "\n\n" + replyHeader(m) + "\n" + quoted
}

// forwardQuote builds the block a forward starts with. Like the reply header it
// is fixed English, and the body is forwarded whole instead of line by line.
func forwardQuote(m *mail.Message) string {
	var b strings.Builder
	b.WriteString("\n\n")
	b.WriteString(forwardHeaderLine)
	b.WriteByte('\n')
	if from := quoteSender(m.From); from != "" {
		b.WriteString("From: " + from + "\n")
	}
	if when := formatQuoteTime(m.Date); when != "" {
		b.WriteString("Date: " + when + "\n")
	}
	if subject := strings.TrimSpace(m.Subject); subject != "" {
		b.WriteString("Subject: " + subject + "\n")
	}
	b.WriteByte('\n')
	b.WriteString(quotedBody(m))
	return b.String()
}

func replySubject(subject string) string {
	s := strings.TrimSpace(subject)
	if strings.HasPrefix(strings.ToLower(s), "re:") {
		return s
	}
	return "Re: " + s
}

func forwardSubject(subject string) string {
	s := strings.TrimSpace(subject)
	if strings.HasPrefix(strings.ToLower(s), "fwd:") {
		return s
	}
	return "Fwd: " + s
}
