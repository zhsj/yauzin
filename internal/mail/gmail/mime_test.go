package gmail

import (
	"encoding/base64"
	"testing"
	"time"

	"github.com/zhsj/yauzin/internal/mail"
)

func b64(s string) string { return base64.RawURLEncoding.EncodeToString([]byte(s)) }

func b64Padded(s string) string { return base64.URLEncoding.EncodeToString([]byte(s)) }

func textPart(mimeType, text string) *MessagePart {
	return &MessagePart{MIMEType: mimeType, Body: &MessagePartBody{Data: b64(text)}}
}

func TestMessagePrefersFirstTextAndHTMLBodies(t *testing.T) {
	svc := &Service{acc: mail.Account{ID: "acc"}}
	msg := &Message{
		ID:           "m1",
		LabelIDs:     []string{"UNREAD", "STARRED"},
		InternalDate: "1700000000000",
		Payload: &MessagePart{
			MIMEType: "multipart/mixed",
			Headers: []*MessagePartHeader{
				{Name: "From", Value: "Alice <alice@example.com>"},
				{Name: "To", Value: "user@example.com"},
				{Name: "Cc", Value: "cc@example.com"},
				{Name: "Subject", Value: "Hello"},
			},
			Parts: []*MessagePart{
				{
					MIMEType: "multipart/alternative",
					Parts: []*MessagePart{
						textPart("text/plain", "plain text"),
						textPart("text/html", "<b>html</b>"),
					},
				},
				{
					Filename: "report.pdf",
					MIMEType: "application/pdf",
					Body:     &MessagePartBody{AttachmentID: "att1", Size: 1234},
				},
			},
		},
	}

	got := svc.message(msg)
	if got.TextBody != "plain text" {
		t.Fatalf("text body = %q", got.TextBody)
	}
	if got.HTMLBody != "<b>html</b>" {
		t.Fatalf("html body = %q", got.HTMLBody)
	}
	if len(got.Attachments) != 1 {
		t.Fatalf("attachments = %+v", got.Attachments)
	}
	att := got.Attachments[0]
	if att.ID != "att1" || att.Filename != "report.pdf" || att.Size != 1234 || att.MIMEType != "application/pdf" {
		t.Fatalf("attachment = %+v", att)
	}
	if !got.HasAttachments || !got.Unread || !got.Starred {
		t.Fatalf("flags = %+v", got.MessageSummary)
	}
	if got.From.Email != "alice@example.com" || got.From.Name != "Alice" {
		t.Fatalf("from = %+v", got.From)
	}
	if len(got.To) != 1 || len(got.Cc) != 1 {
		t.Fatalf("to/cc = %+v / %+v", got.To, got.Cc)
	}
	if !got.Date.Equal(time.UnixMilli(1700000000000)) {
		t.Fatalf("date = %v", got.Date)
	}
}

func TestMessageHTMLOnly(t *testing.T) {
	svc := &Service{}
	got := svc.message(&Message{ID: "m1", Payload: textPart("text/html", "<p>only html</p>")})
	if got.TextBody != "" || got.HTMLBody != "<p>only html</p>" {
		t.Fatalf("bodies = %q / %q", got.TextBody, got.HTMLBody)
	}
}

func TestBodyBase64ToleratesPadding(t *testing.T) {
	svc := &Service{}
	got := svc.message(&Message{ID: "m1", Payload: &MessagePart{
		MIMEType: "text/plain",
		Body:     &MessagePartBody{Data: b64Padded("padded text")},
	}})
	if got.TextBody != "padded text" {
		t.Fatalf("text body = %q", got.TextBody)
	}
}

// TestBodyIgnoresTheDeclaredCharset pins the bytes Gmail hands back: they are
// UTF-8 even when the part still says iso-8859-1 or windows-1252, as in a
// latin-1 source whose quoted-printable "Gr=FC=DFe" arrives as UTF-8. Converting
// by that label would read UTF-8 as latin-1 and render "GrÃ¼ÃŸe".
func TestBodyIgnoresTheDeclaredCharset(t *testing.T) {
	tests := []struct {
		name        string
		mimeType    string
		contentType string
		body        string
	}{
		{"latin1 label", "text/plain", `text/plain; charset="iso-8859-1"`, "Grüße aus München"},
		{"unquoted latin1 label", "text/plain", `text/plain; charset=ISO-8859-1`, "Grüße aus Berlin"},
		{"cp1252 label", "text/plain", `text/plain; charset="windows-1252"`, "Grüße – schöner Tag"},
		{"html body", "text/html", `text/html; charset="iso-8859-1"`, "<p>Grüße aus Köln</p>"},
		{"no content type", "text/plain", "", "Grüße"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			part := &MessagePart{MIMEType: tt.mimeType, Body: &MessagePartBody{Data: b64(tt.body)}}
			if tt.contentType != "" {
				part.Headers = []*MessagePartHeader{{Name: "Content-Type", Value: tt.contentType}}
			}
			got := (&Service{}).message(&Message{ID: "m1", Payload: part})
			body := got.TextBody
			if tt.mimeType == "text/html" {
				body = got.HTMLBody
			}
			if body != tt.body {
				t.Fatalf("body = %q, want %q", body, tt.body)
			}
		})
	}
}

func TestFilenamedPartIsAnAttachmentNotBody(t *testing.T) {
	svc := &Service{}
	got := svc.message(&Message{ID: "m1", Payload: &MessagePart{
		MIMEType: "multipart/mixed",
		Parts: []*MessagePart{
			{Filename: "notes.txt", MIMEType: "text/plain", Body: &MessagePartBody{AttachmentID: "a1", Data: b64("attached")}},
			textPart("text/plain", "the body"),
		},
	}})
	if got.TextBody != "the body" {
		t.Fatalf("text body = %q, want the non-attached part", got.TextBody)
	}
	if len(got.Attachments) != 1 || got.Attachments[0].Filename != "notes.txt" {
		t.Fatalf("attachments = %+v", got.Attachments)
	}
}

func TestDateFallsBackToTheHeader(t *testing.T) {
	svc := &Service{}
	got := svc.message(&Message{
		ID:           "m1",
		InternalDate: "not-a-number",
		Payload: &MessagePart{Headers: []*MessagePartHeader{
			{Name: "Date", Value: "Tue, 14 Nov 2023 22:13:20 +0000"},
		}},
	})
	want := time.Date(2023, 11, 14, 22, 13, 20, 0, time.UTC)
	if !got.Date.Equal(want) {
		t.Fatalf("date = %v, want %v", got.Date, want)
	}
}

func TestMalformedAddressesFallBackToText(t *testing.T) {
	addrs := parseAddressList("not an address <")
	if len(addrs) != 1 || addrs[0].String() != "not an address <" {
		t.Fatalf("addresses = %+v, want the raw text", addrs)
	}
	if got := firstAddress(""); got != (mail.Address{}) {
		t.Fatalf("firstAddress(empty) = %+v", got)
	}
}

func TestAttachmentMetadataWithoutBody(t *testing.T) {
	p := &MessagePart{Filename: "a.bin", MIMEType: "application/octet-stream"}
	a := attachment(p)
	if a.Filename != "a.bin" || a.ID != "" || a.Size != 0 {
		t.Fatalf("attachment = %+v", a)
	}
}

func TestPayloadHasAttachment(t *testing.T) {
	if payloadHasAttachment(&MessagePart{MIMEType: "text/plain", Body: &MessagePartBody{Data: b64("x")}}) {
		t.Fatal("a body-only part reported an attachment")
	}
	nested := &MessagePart{Parts: []*MessagePart{{Parts: []*MessagePart{{Filename: "x.png"}}}}}
	if !payloadHasAttachment(nested) {
		t.Fatal("nested file part was not detected")
	}
}
