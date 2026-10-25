package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/zhsj/yauzin/internal/i18n"
	"github.com/zhsj/yauzin/internal/mail"
)

func useLanguage(t *testing.T, tag string) {
	t.Helper()
	i18n.SetLanguage(tag)
	t.Cleanup(func() { i18n.SetLanguage(i18n.DefaultLanguage) })
}

func TestFolderDisplayNameEnglish(t *testing.T) {
	useLanguage(t, "en")
	for _, tc := range []struct {
		folder mail.Folder
		want   string
	}{
		{mail.Folder{Kind: mail.FolderInbox, Name: "收件箱"}, "Inbox"},
		{mail.Folder{Kind: mail.FolderStarred, Name: "已加星标"}, "Starred"},
		{mail.Folder{Kind: mail.FolderSent, Name: "已发送"}, "Sent"},
		{mail.Folder{Kind: mail.FolderSpam, Name: "垃圾邮件"}, "Spam"},
		{mail.Folder{Kind: mail.FolderTrash, Name: "垃圾箱"}, "Trash"},
		{mail.Folder{Kind: mail.FolderLabel, Name: "工作"}, "工作"},
	} {
		if got := folderDisplayName(tc.folder); got != tc.want {
			t.Errorf("folderDisplayName(%s) = %q, want %q", tc.folder.ID, got, tc.want)
		}
	}
}

func TestFolderDisplayNameChinese(t *testing.T) {
	useLanguage(t, "zh-Hans")
	for _, tc := range []struct {
		folder mail.Folder
		want   string
	}{
		{mail.Folder{Kind: mail.FolderInbox}, "收件箱"},
		{mail.Folder{Kind: mail.FolderStarred}, "已加星标"},
		{mail.Folder{Kind: mail.FolderSent}, "已发送"},
		{mail.Folder{Kind: mail.FolderSpam}, "垃圾邮件"},
		{mail.Folder{Kind: mail.FolderTrash}, "垃圾箱"},
		{mail.Folder{Kind: mail.FolderLabel, Name: "账单"}, "账单"},
	} {
		if got := folderDisplayName(tc.folder); got != tc.want {
			t.Errorf("folderDisplayName(%s) = %q, want %q", tc.folder.ID, got, tc.want)
		}
	}
}

func TestFormatListTime(t *testing.T) {
	now := time.Date(2026, 10, 20, 15, 4, 0, 0, time.Local)
	today := time.Date(2026, 10, 20, 9, 30, 0, 0, time.Local)
	thisYear := time.Date(2026, 3, 5, 8, 7, 0, 0, time.Local)
	older := time.Date(2024, 12, 31, 23, 59, 0, 0, time.Local)

	for _, tc := range []struct {
		language string
		when     time.Time
		want     string
	}{
		{"en", today, "9:30 AM"},
		{"en", thisYear, "Mar 5"},
		{"en", older, "2024/12/31"},
		{"zh-Hans", today, "09:30"},
		{"zh-Hans", thisYear, "3月5日"},
		{"zh-Hans", older, "2024/12/31"},
	} {
		i18n.SetLanguage(tc.language)
		if got := formatListTimeAt(tc.when, now); got != tc.want {
			t.Errorf("%s formatListTimeAt(%s) = %q, want %q", tc.language, tc.when, got, tc.want)
		}
	}
	i18n.SetLanguage(i18n.DefaultLanguage)

	if got := formatListTimeAt(time.Time{}, now); got != "" {
		t.Errorf("zero time = %q, want empty", got)
	}
}

func TestFormatFullTime(t *testing.T) {
	when := time.Date(2026, 3, 5, 8, 7, 0, 0, time.Local)
	for _, tc := range []struct {
		language string
		want     string
	}{
		{"en", "Mar 5, 2026 8:07 AM"},
		{"zh-Hans", "2026年3月5日 08:07"},
	} {
		i18n.SetLanguage(tc.language)
		if got := formatFullTime(when); got != tc.want {
			t.Errorf("%s formatFullTime = %q, want %q", tc.language, got, tc.want)
		}
	}
	i18n.SetLanguage(i18n.DefaultLanguage)

	if got := formatFullTime(time.Time{}); got != "" {
		t.Errorf("zero time = %q, want empty", got)
	}
}

func TestFormatFullTimeUsesLocalZone(t *testing.T) {
	useLanguage(t, "en")
	zone := time.FixedZone("UTC+9", 9*60*60)
	when := time.Date(2026, 3, 5, 8, 7, 0, 0, zone)

	want := when.In(time.Local).Format(i18n.T("date.full"))
	if got := formatFullTime(when); got != want {
		t.Fatalf("formatFullTime = %q, want the local rendering %q", got, want)
	}
	// The list renders the same message in local time, so the two views have to
	// agree on the zone.
	if got, listWant := formatFullTime(when), formatListTime(when); got == "" || listWant == "" {
		t.Fatalf("empty timestamp: full %q, list %q", got, listWant)
	}
}

// TestQuotedBodyOnlyUsesPlainText pins the quoting rule: an HTML-only message
// has no body at all. Its HTML must never be turned into quoted text.
func TestQuotedBodyOnlyUsesPlainText(t *testing.T) {
	useLanguage(t, "en")

	for _, tc := range []struct {
		name string
		msg  *mail.Message
		want string
	}{
		{"plain", &mail.Message{TextBody: "hello\n\nworld"}, "hello\n\nworld"},
		{"plain wins over html", &mail.Message{TextBody: "plain", HTMLBody: "<p>html</p>"}, "plain"},
		{"html only", &mail.Message{HTMLBody: "<p>html only</p>"}, quoteNoBody},
		{"blank", &mail.Message{TextBody: " \n\t ", HTMLBody: "<p>html</p>"}, quoteNoBody},
		{"empty", &mail.Message{}, quoteNoBody},
	} {
		if got := quotedBody(tc.msg); got != tc.want {
			t.Errorf("%s: quotedBody = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// TestReplyQuoteLayout pins Gmail's reply header: the time comes first, the
// sender follows, and the body is marked line by line.
func TestReplyQuoteLayout(t *testing.T) {
	useLanguage(t, "en")
	when := time.Date(2026, 3, 5, 8, 7, 0, 0, time.Local)
	msg := &mail.Message{
		MessageSummary: mail.MessageSummary{
			From: mail.Address{Name: "Alice", Email: "alice@example.com"},
			Date: when,
		},
		TextBody: "hello\n   \nworld\n",
	}

	want := "\n\nOn Thu, Mar 5, 2026 at 8:07 AM Alice <alice@example.com> wrote:\n> hello\n>\n> world"
	if got := replyQuote(msg, ""); got != want {
		t.Fatalf("replyQuote = %q, want %q", got, want)
	}
	// A Chinese interface quotes the same mail: the header is content, not UI
	// copy, so it never gets localized.
	useLanguage(t, "zh-Hans")
	if got := replyQuote(msg, ""); got != want {
		t.Fatalf("zh replyQuote = %q, want the English header %q", got, want)
	}
	i18n.SetLanguage("en")

	// Missing pieces drop out of the header instead of leaving a gap.
	msg.Date = time.Time{}
	if got, want := replyQuote(msg, ""), "\n\nAlice <alice@example.com> wrote:\n> hello"; !strings.HasPrefix(got, want) {
		t.Fatalf("dateless replyQuote = %q, want the prefix %q", got, want)
	}
	msg.From = mail.Address{Email: "bob@example.com"}
	if got, want := replyQuote(msg, ""), "\n\nbob@example.com wrote:\n> hello"; !strings.HasPrefix(got, want) {
		t.Fatalf("nameless replyQuote = %q, want the prefix %q", got, want)
	}
}

// TestReplyQuoteSelection pins what a selection replaces: the body only. A
// selection without visible text falls back to the whole body, and a message
// with no plain text has nothing to select in the first place.
func TestReplyQuoteSelection(t *testing.T) {
	useLanguage(t, "en")
	msg := &mail.Message{
		MessageSummary: mail.MessageSummary{
			From: mail.Address{Name: "Alice", Email: "alice@example.com"},
		},
		TextBody: "one\ntwo\nthree",
	}
	header := replyHeader(msg)

	for _, tc := range []struct {
		name      string
		selection string
		want      string
	}{
		{"selected lines", "two\r\nthree\r\n\r\n", header + "\n> two\n> three"},
		{"blank selection", " \n\t ", header + "\n> one\n> two\n> three"},
		{"no selection", "", header + "\n> one\n> two\n> three"},
	} {
		if got := replyQuote(msg, tc.selection); got != "\n\n"+tc.want {
			t.Errorf("%s: replyQuote = %q, want %q", tc.name, got, "\n\n"+tc.want)
		}
	}

	// The reader selects its "no plain text" placeholder for an HTML-only
	// message; that placeholder is not mail and must not be quoted.
	html := &mail.Message{
		MessageSummary: mail.MessageSummary{From: msg.From},
		HTMLBody:       "<p>html</p>",
	}
	want := "\n\n" + replyHeader(html) + "\n" + quoteNoBody
	if got := replyQuote(html, "some placeholder"); got != want {
		t.Fatalf("html replyQuote = %q, want %q", got, want)
	}
}

// TestForwardQuoteLayout pins the forwarded block: the same fixed English
// header as the reply, with the body forwarded whole instead of quoted.
func TestForwardQuoteLayout(t *testing.T) {
	useLanguage(t, "en")
	when := time.Date(2026, 3, 5, 8, 7, 0, 0, time.Local)
	msg := &mail.Message{
		MessageSummary: mail.MessageSummary{
			From:    mail.Address{Name: "Alice", Email: "alice@example.com"},
			Date:    when,
			Subject: "Hello",
		},
		TextBody: "body\n",
	}
	want := "\n\n---------- Forwarded message ----------\n" +
		"From: Alice <alice@example.com>\n" +
		"Date: Thu, Mar 5, 2026 at 8:07 AM\n" +
		"Subject: Hello\n\nbody\n"
	if got := forwardQuote(msg); got != want {
		t.Fatalf("forwardQuote = %q, want %q", got, want)
	}
	useLanguage(t, "zh-Hans")
	if got := forwardQuote(msg); got != want {
		t.Fatalf("zh forwardQuote = %q, want the English header %q", got, want)
	}
	i18n.SetLanguage("en")

	// Lines the message cannot fill are left out rather than half written.
	if got, want := forwardQuote(&mail.Message{}), "\n\n---------- Forwarded message ----------\n\n"+quoteNoBody; got != want {
		t.Fatalf("empty forwardQuote = %q, want %q", got, want)
	}
}
