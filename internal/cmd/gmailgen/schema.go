package main

import (
	"fmt"
	"sort"
	"strings"
)

// spec is the part of the Discovery document the generator reads. Methods and
// parameters are ignored on purpose: request paths, authentication and error
// mapping stay hand-written in client.go.
type spec struct {
	Revision string            `json:"revision"`
	Schemas  map[string]schema `json:"schemas"`
}

// schema follows the JSON Schema subset Google uses in Discovery documents.
type schema struct {
	Type                 string            `json:"type"`
	Format               string            `json:"format"`
	Ref                  string            `json:"$ref"`
	Items                *schema           `json:"items"`
	Properties           map[string]schema `json:"properties"`
	AdditionalProperties *schema           `json:"additionalProperties"`
}

// goType renders the Go type of one schema. The bool reports an approximation:
// the schema could not be expressed and fell back to json.RawMessage, which the
// caller records in the generated header instead of dropping the field.
func goType(s schema) (string, bool) {
	switch {
	case s.Ref != "":
		return "*" + typeName(s.Ref), false
	case s.Type == "array":
		if s.Items == nil {
			return "[]json.RawMessage", true
		}
		elem, degraded := goType(*s.Items)
		return "[]" + elem, degraded
	case s.Type == "boolean":
		return "bool", false
	case s.Type == "integer":
		switch s.Format {
		case "int64", "uint32":
			// Gmail answers uint32 sizes with values that fit int64, and one
			// signed type keeps the call sites simple.
			return "int64", false
		case "uint64":
			return "uint64", false
		default:
			return "int", false
		}
	case s.Type == "string":
		// The byte, int64, uint64 and google-datetime formats are strings on
		// the wire; only their meaning differs.
		return "string", false
	case s.Type == "number":
		return "float64", false
	case s.Type == "object" && len(s.Properties) > 0:
		return inlineStruct(s), false
	case s.Type == "object" && s.AdditionalProperties != nil:
		elem, degraded := goType(*s.AdditionalProperties)
		return "map[string]" + elem, degraded
	default:
		return "json.RawMessage", true
	}
}

// inlineStruct renders an object that Discovery nested inside a property.
func inlineStruct(s schema) string {
	var b strings.Builder
	b.WriteString("struct {\n")
	for _, name := range sortedKeys(s.Properties) {
		typ, _ := goType(s.Properties[name])
		fmt.Fprintf(&b, "%s %s `json:%q`\n", exportName(name), typ, name)
	}
	b.WriteString("}")
	return b.String()
}

// sortedKeys returns the keys of m in stable order: Go maps iterate randomly and
// the generated file has to be reproducible.
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
