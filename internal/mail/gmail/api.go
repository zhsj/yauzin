package gmail

import (
	"strconv"
	"strings"
	"time"
)

// The types in discovery_types.go mirror the Gmail REST documents: they are
// generated from Google's Discovery document, so their fields are exactly the
// ones the API sends. Most fields are only present when the request asks for
// them through fields or a format.
//
// The helpers below are the hand-written half: they fold those documents into
// the domain model and carry the rules the API leaves implicit.

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
