// Package mailto parses mailto URIs into the fields of a compose draft.
//
// The parser follows RFC 6068: the part before the first '?' is a comma
// separated list of addresses, and the query carries the remaining fields as
// hfields (subject=...&body=...). Unknown hfields are ignored.
//
// in-reply-to is the one field here that the current standard dropped: RFC 2368
// listed it beside subject and body, and links written for older clients still
// carry it. It is kept as the raw value; deciding whether it is a Message-ID
// belongs to the composer, which owns the threading headers.
//
// The query is decoded by hand with url.PathUnescape instead of
// url.ParseQuery. ParseQuery reads '+' as a space, while RFC 6068 reserves the
// percent encoding and keeps '+' literal, so a subject such as "C++" survives
// only with the hand-rolled decoder.
package mailto

import (
	"net/url"
	"strings"
)

// Scheme is the URI scheme the package understands, including the colon.
const Scheme = "mailto:"

// Message is the draft a mailto URI describes. The address fields hold what
// the composer shows: the addresses in link order, separated by commas.
type Message struct {
	To      string
	Cc      string
	Bcc     string
	Subject string
	Body    string
	// InReplyTo is the raw in-reply-to hfield, on one line and trimmed. The
	// composer rejects a value that is not a Message-ID; see Parse.
	InReplyTo string
}

// Parse decodes a mailto URI. ok is false when uri does not use the mailto
// scheme or carries a percent encoding that cannot be decoded; the caller then
// treats it as absent instead of opening a composer nobody asked for.
func Parse(uri string) (Message, bool) {
	rest, ok := cutScheme(uri)
	if !ok {
		return Message{}, false
	}
	path, query, _ := strings.Cut(rest, "?")

	to, ok := addressList(path)
	if !ok {
		return Message{}, false
	}
	var (
		msg Message
		cc  []string
		bcc []string
	)
	for _, field := range strings.Split(query, "&") {
		if field == "" {
			continue
		}
		rawKey, rawValue, _ := strings.Cut(field, "=")
		key, ok := decode(rawKey)
		if !ok {
			return Message{}, false
		}
		switch hfield(key) {
		case "to":
			list, ok := addressList(rawValue)
			if !ok {
				return Message{}, false
			}
			to = appendUnique(to, list...)
		case "cc":
			list, ok := addressList(rawValue)
			if !ok {
				return Message{}, false
			}
			cc = appendUnique(cc, list...)
		case "bcc":
			list, ok := addressList(rawValue)
			if !ok {
				return Message{}, false
			}
			bcc = appendUnique(bcc, list...)
		case "subject":
			if msg.Subject != "" {
				continue
			}
			value, ok := decode(rawValue)
			if !ok {
				return Message{}, false
			}
			msg.Subject = oneLine(value)
		case "body":
			if msg.Body != "" {
				continue
			}
			value, ok := decode(rawValue)
			if !ok {
				return Message{}, false
			}
			msg.Body = normalizeBody(value)
		case "inreplyto":
			if msg.InReplyTo != "" {
				continue
			}
			value, ok := decode(rawValue)
			if !ok {
				return Message{}, false
			}
			msg.InReplyTo = oneLine(value)
		}
	}
	msg.To = strings.Join(to, ", ")
	msg.Cc = strings.Join(cc, ", ")
	msg.Bcc = strings.Join(bcc, ", ")
	return msg, true
}

// FromArgs returns the last mailto URI among args. The composer is a single
// slot, so when a launcher passes several URIs only the last one stays on
// screen; picking it here keeps the caller from parsing the others.
func FromArgs(args []string) (uri string, ok bool) {
	for _, arg := range args {
		if _, isMailto := cutScheme(arg); isMailto {
			uri, ok = arg, true
		}
	}
	return uri, ok
}

// cutScheme strips the scheme case-insensitively, reporting false for any
// other argument. A URI that merely starts with the letters but has no colon
// is not a mailto URI.
func cutScheme(uri string) (string, bool) {
	if len(uri) < len(Scheme) || !strings.EqualFold(uri[:len(Scheme)], Scheme) {
		return "", false
	}
	return uri[len(Scheme):], true
}

// hfield normalizes one header name for the switch: links spell the same field
// as subject, Subject or SUBJECT, and in-reply-to also turns up without its
// hyphens.
func hfield(name string) string {
	key := strings.ToLower(strings.TrimSpace(name))
	return strings.ReplaceAll(key, "-", "")
}

// addressList splits one raw address field into the addresses it names. The
// split happens before decoding so an encoded comma stays inside its address.
func addressList(raw string) ([]string, bool) {
	var out []string
	for _, part := range strings.Split(raw, ",") {
		value, ok := decode(part)
		if !ok {
			return nil, false
		}
		if value = oneLine(value); value != "" {
			out = append(out, value)
		}
	}
	return out, true
}

// appendUnique adds the addresses of list that are not in out yet, keeping
// link order: a link that names the same recipient in the path and in a to=
// hfield should still produce one entry.
func appendUnique(out []string, list ...string) []string {
	for _, addr := range list {
		if contains(out, addr) {
			continue
		}
		out = append(out, addr)
	}
	return out
}

func contains(list []string, want string) bool {
	for _, item := range list {
		if item == want {
			return true
		}
	}
	return false
}

// decode percent-decodes one component, rejecting an escape that is not valid
// UTF-8 or not a percent triplet.
func decode(s string) (string, bool) {
	value, err := url.PathUnescape(s)
	if err != nil {
		return "", false
	}
	return value, true
}

// oneLine keeps a value on one line. To, Cc, Bcc and Subject land in
// single-line editors, and in-reply-to in a header, so a CR or LF from a
// hand-written link has to become a space instead of reaching either.
func oneLine(s string) string {
	return strings.TrimSpace(strings.NewReplacer("\r\n", " ", "\r", " ", "\n", " ").Replace(s))
}

// normalizeBody keeps the line structure of the body but normalizes the line
// endings to LF, which is what the body editor stores. The blank lines are
// significant, so nothing is trimmed.
func normalizeBody(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\r", "\n")
}
