package compliancetest

import (
	"fmt"
	"strings"
)

// Type is a parsed GoogleSQL type from the header of an expected
// block, e.g. `ARRAY<STRUCT<a INT64, STRING>>`.
type Type struct {
	// Kind is the upper-cased base name: INT64, STRING, ARRAY, STRUCT,
	// RANGE, MAP, JSON, ... An ARRAY written as `ARRAY<>` (the suite's
	// notation for "element type given per value") has Elem == nil.
	Kind string
	// Elem is the element type for ARRAY and RANGE.
	Elem *Type
	// Fields holds STRUCT fields in order.
	Fields []Field
	// Raw is the original text of the type.
	Raw string
}

// Field is one STRUCT field. Name is empty for anonymous fields.
type Field struct {
	Name string
	Type *Type
}

// ParseType parses a GoogleSQL type string.
func ParseType(s string) (*Type, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, fmt.Errorf("empty type")
	}
	lt := strings.IndexByte(s, '<')
	if lt < 0 {
		return &Type{Kind: strings.ToUpper(s), Raw: s}, nil
	}
	if !strings.HasSuffix(s, ">") {
		return nil, fmt.Errorf("type %q: missing closing >", s)
	}
	kind := strings.ToUpper(strings.TrimSpace(s[:lt]))
	inner := strings.TrimSpace(s[lt+1 : len(s)-1])
	t := &Type{Kind: kind, Raw: s}
	switch kind {
	case "ARRAY", "RANGE":
		if inner == "" {
			return t, nil
		}
		e, err := ParseType(inner)
		if err != nil {
			return nil, err
		}
		t.Elem = e
	case "STRUCT":
		if inner == "" {
			return t, nil
		}
		for _, part := range splitTopLevelCommas(inner) {
			f, err := parseField(part)
			if err != nil {
				return nil, err
			}
			t.Fields = append(t.Fields, f)
		}
	default:
		// MAP<K, V>, PROTO<...>, ENUM<...> and similar are kept opaque.
	}
	return t, nil
}

func parseField(part string) (Field, error) {
	part = strings.TrimSpace(part)
	// A named field has a top-level space before any '<'.
	depth := 0
	for i, r := range part {
		switch r {
		case '<':
			depth++
		case '>':
			depth--
		case ' ':
			if depth == 0 {
				t, err := ParseType(part[i+1:])
				if err != nil {
					return Field{}, err
				}
				return Field{Name: part[:i], Type: t}, nil
			}
		}
	}
	t, err := ParseType(part)
	if err != nil {
		return Field{}, err
	}
	return Field{Type: t}, nil
}

// splitTopLevelCommas splits on commas not nested in <>, (), [], {}
// or quotes.
func splitTopLevelCommas(s string) []string {
	var out []string
	depth := 0
	start := 0
	var quote byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		if quote != 0 {
			if c == '\\' {
				i++
				continue
			}
			if c == quote {
				quote = 0
			}
			continue
		}
		switch c {
		case '"', '\'', '`':
			quote = c
		case '<', '(', '[', '{':
			depth++
		case '>', ')', ']', '}':
			depth--
		case ',':
			if depth == 0 {
				out = append(out, strings.TrimSpace(s[start:i]))
				start = i + 1
			}
		}
	}
	if tail := strings.TrimSpace(s[start:]); tail != "" {
		out = append(out, tail)
	}
	return out
}

// Walk calls fn for t and every nested type.
func (t *Type) Walk(fn func(*Type)) {
	if t == nil {
		return
	}
	fn(t)
	t.Elem.Walk(fn)
	for _, f := range t.Fields {
		f.Type.Walk(fn)
	}
}
