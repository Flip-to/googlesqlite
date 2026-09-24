package json

import (
	"bytes"

	stdjson "encoding/json"
	"fmt"
	"github.com/goccy/googlesqlite/internal/value"
	"strconv"
	"strings"
)

// gsqlPath is a parsed GoogleSQL JSONPath (json_functions.md, JSONPath
// format): `$` followed by member accessors (`.name`, `."quoted name"`,
// `['name']`, `["name"]`, and the legacy unquoted `[name]` accepted by
// JSON_EXTRACT) and array subscripts (`[0]`). Evaluation works on the raw
// document so member order and number spelling are preserved.
type gsqlPath struct {
	steps           []pathStep
	usedSingleQuote bool
}

type pathStep struct {
	name    string
	index   int
	isIndex bool
}

// UsedSingleQuotePathSelector reports whether the path used a
// `['name']` selector, which only the legacy JSON_EXTRACT family allows.
func (p *gsqlPath) UsedSingleQuotePathSelector() bool { return p.usedSingleQuote }

// createPath parses a JSONPath expression.
func createPath(path string) (*gsqlPath, error) {
	s := strings.TrimSpace(path)
	if !strings.HasPrefix(s, "$") {
		return nil, fmt.Errorf("JSONPath must start with '$'")
	}
	p := &gsqlPath{}
	i := 1
	for i < len(s) {
		switch s[i] {
		case '.':
			i++
			if i >= len(s) {
				return nil, fmt.Errorf("invalid JSONPath %q: unexpected end after '.'", path)
			}
			if s[i] == '"' {
				name, n, err := readQuoted(s[i:], '"')
				if err != nil {
					return nil, fmt.Errorf("invalid JSONPath %q: %w", path, err)
				}
				p.steps = append(p.steps, pathStep{name: name})
				i += n
				continue
			}
			j := i
			for j < len(s) && s[j] != '.' && s[j] != '[' {
				j++
			}
			if j == i {
				return nil, fmt.Errorf("invalid JSONPath %q: empty member name", path)
			}
			p.steps = append(p.steps, pathStep{name: s[i:j]})
			i = j
		case '[':
			i++
			for i < len(s) && s[i] == ' ' {
				i++
			}
			if i >= len(s) {
				return nil, fmt.Errorf("invalid JSONPath %q: unterminated '['", path)
			}
			if s[i] == '\'' || s[i] == '"' {
				q := s[i]
				name, n, err := readQuoted(s[i:], q)
				if err != nil {
					return nil, fmt.Errorf("invalid JSONPath %q: %w", path, err)
				}
				if q == '\'' {
					p.usedSingleQuote = true
				}
				i += n
				for i < len(s) && s[i] == ' ' {
					i++
				}
				if i >= len(s) || s[i] != ']' {
					return nil, fmt.Errorf("invalid JSONPath %q: expected ']'", path)
				}
				i++
				p.steps = append(p.steps, pathStep{name: name})
				continue
			}
			j := strings.IndexByte(s[i:], ']')
			if j < 0 {
				return nil, fmt.Errorf("invalid JSONPath %q: unterminated '['", path)
			}
			tok := strings.TrimSpace(s[i : i+j])
			i += j + 1
			if tok == "" {
				return nil, fmt.Errorf("invalid JSONPath %q: empty subscript", path)
			}
			if n, err := strconv.Atoi(tok); err == nil {
				if n < 0 {
					return nil, fmt.Errorf("invalid JSONPath %q: negative subscript", path)
				}
				p.steps = append(p.steps, pathStep{index: n, isIndex: true})
			} else {
				p.steps = append(p.steps, pathStep{name: tok})
			}
		default:
			return nil, fmt.Errorf("invalid JSONPath %q: unexpected character %q", path, s[i])
		}
	}
	return p, nil
}

// readQuoted reads a quoted token starting at s[0] == q and returns
// its unescaped content and the number of bytes consumed.
func readQuoted(s string, q byte) (string, int, error) {
	var b strings.Builder
	for i := 1; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '\\' && i+1 < len(s):
			i++
			b.WriteByte(s[i])
		case c == q:
			return b.String(), i + 1, nil
		default:
			b.WriteByte(c)
		}
	}
	return "", 0, fmt.Errorf("unterminated quoted member name")
}

// Extract returns the raw JSON selected by the path (zero or one
// element), mirroring the go-json Path.Extract shape used by callers.
func (p *gsqlPath) Extract(doc []byte) ([][]byte, error) {
	cur := bytes.TrimSpace(doc)
	for _, st := range p.steps {
		var ok bool
		if st.isIndex {
			cur, ok = rawArrayElem(cur, st.index)
		} else {
			cur, ok = rawObjectMember(cur, st.name)
		}
		if !ok {
			return nil, nil
		}
	}
	return [][]byte{cur}, nil
}

// rawObjectMember returns the raw value of the first member named key
// when raw is a JSON object.
func rawObjectMember(raw []byte, key string) ([]byte, bool) {
	if len(raw) == 0 || raw[0] != '{' {
		return nil, false
	}
	dec := stdjson.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if _, err := dec.Token(); err != nil {
		return nil, false
	}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil, false
		}
		k, _ := tok.(string)
		var v stdjson.RawMessage
		if err := dec.Decode(&v); err != nil {
			return nil, false
		}
		if k == key {
			return bytes.TrimSpace(v), true
		}
	}
	return nil, false
}

// rawArrayElem returns the raw idx-th element when raw is a JSON array.
func rawArrayElem(raw []byte, idx int) ([]byte, bool) {
	if len(raw) == 0 || raw[0] != '[' {
		return nil, false
	}
	dec := stdjson.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if _, err := dec.Token(); err != nil {
		return nil, false
	}
	for i := 0; dec.More(); i++ {
		var v stdjson.RawMessage
		if err := dec.Decode(&v); err != nil {
			return nil, false
		}
		if i == idx {
			return bytes.TrimSpace(v), true
		}
	}
	return nil, false
}

// Unmarshal decodes the selected value into *[]any (zero or one
// element), mirroring go-json's Path.Unmarshal.
func (p *gsqlPath) Unmarshal(doc []byte, out *[]any) error {
	if !stdjson.Valid(doc) {
		return fmt.Errorf("invalid JSON input")
	}
	extracted, err := p.Extract(doc)
	if err != nil {
		return err
	}
	for _, raw := range extracted {
		var v any
		if err := stdjson.Unmarshal(raw, &v); err != nil {
			return err
		}
		*out = append(*out, v)
	}
	return nil
}

// keepJSONNullElements turns the SQL NULL elements of an extracted
// ARRAY<JSON> back into JSON 'null'.
func keepJSONNullElements(v value.Value) {
	arr, ok := v.(*value.ArrayValue)
	if !ok {
		return
	}
	for i, e := range arr.Values {
		if e == nil {
			arr.Values[i] = value.JsonValue("null")
		}
	}
}
