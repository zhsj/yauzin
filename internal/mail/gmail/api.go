package gmail

import (
	"strconv"
	"strings"
	"time"
)

// The types below are the subset of the Gmail REST documents this backend
// reads. Field names follow the API; most are only present when the request
// asks for them through fields or a format.

type labelList struct {
	Labels []label `json:"labels"`
}

type label struct {
	ID             string      `json:"id"`
	Name           string      `json:"name"`
	Type           string      `json:"type"`
	MessagesUnread int         `json:"messagesUnread"`
	Color          *labelColor `json:"color"`
}

type labelColor struct {
	BackgroundColor string `json:"backgroundColor"`
}

// threadRef is one entry of threads.list.
type threadRef struct {
	ID      string `json:"id"`
	Snippet string `json:"snippet"`
}

type threadList struct {
	Threads            []threadRef `json:"threads"`
	NextPageToken      string      `json:"nextPageToken"`
	ResultSizeEstimate int64       `json:"resultSizeEstimate"`
}

// thread is one conversation with its messages, oldest first.
type thread struct {
	ID       string    `json:"id"`
	Messages []message `json:"messages"`
}

// message is one message of a conversation.
type message struct {
	ID           string   `json:"id"`
	ThreadID     string   `json:"threadId"`
	LabelIDs     []string `json:"labelIds"`
	Snippet      string   `json:"snippet"`
	InternalDate string   `json:"internalDate"`
	Payload      *payload `json:"payload"`
}

// payload is the MIME tree node of a message.
type payload struct {
	PartID   string     `json:"partId"`
	MIMEType string     `json:"mimeType"`
	Filename string     `json:"filename"`
	Headers  []header   `json:"headers"`
	Body     *body      `json:"body"`
	Parts    []*payload `json:"parts"`
}

// body is the content of one MIME part. Data is base64url in the full format
// and absent in the metadata format.
type body struct {
	AttachmentID string `json:"attachmentId"`
	Size         int64  `json:"size"`
	Data         string `json:"data"`
}

type header struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// headers returns the message headers keyed by lower-case name.
func (m *message) headers() map[string]string {
	out := make(map[string]string, 8)
	if m.Payload == nil {
		return out
	}
	for _, h := range m.Payload.Headers {
		out[strings.ToLower(h.Name)] = h.Value
	}
	return out
}

// hasFlag reports whether the message carries one of the Gmail label IDs that
// stand for a flag.
func (m *message) hasFlag(labelID string) bool {
	for _, id := range m.LabelIDs {
		if id == labelID {
			return true
		}
	}
	return false
}

// date returns the message timestamp. internalDate is authoritative; the Date
// header is only a fallback when the API omitted it.
func (m *message) date() time.Time {
	if raw := strings.TrimSpace(m.InternalDate); raw != "" {
		if ms, err := strconv.ParseInt(raw, 10, 64); err == nil {
			return time.UnixMilli(ms)
		}
	}
	if raw := m.headers()["date"]; raw != "" {
		if t, err := parseMailDate(raw); err == nil {
			return t
		}
	}
	return time.Time{}
}

// hasAttachment reports whether the MIME tree carries a file part.
func (m *message) hasAttachment() bool {
	if m.Payload == nil {
		return false
	}
	return payloadHasAttachment(m.Payload)
}

func payloadHasAttachment(p *payload) bool {
	if p.Filename != "" || (p.Body != nil && p.Body.AttachmentID != "") {
		return true
	}
	for _, child := range p.Parts {
		if payloadHasAttachment(child) {
			return true
		}
	}
	return false
}
