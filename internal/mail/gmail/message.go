package gmail

import (
	"strconv"
	"strings"
	"time"
)

// The Message and MessagePart types come from discovery_types.go, which
// gmailgen generates from Google's Discovery document. The helpers here are
// the hand-written half: they read those documents and carry the rules the
// API leaves implicit, such as which label stands for which flag and where the
// timestamp of a message is found.

// headers returns the message headers keyed by lower-case name.
func (m *Message) headers() map[string]string {
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
func (m *Message) hasFlag(labelID string) bool {
	for _, id := range m.LabelIDs {
		if id == labelID {
			return true
		}
	}
	return false
}

// date returns the message timestamp. internalDate is authoritative; the Date
// header is only a fallback when the API omitted it.
func (m *Message) date() time.Time {
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
func (m *Message) hasAttachment() bool {
	if m.Payload == nil {
		return false
	}
	return payloadHasAttachment(m.Payload)
}

func payloadHasAttachment(p *MessagePart) bool {
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
