package json

import (
	"bytes"

	stdjson "encoding/json"
	"fmt"
	"github.com/goccy/googlesqlite/internal/value"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
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

var (
	pathCache     sync.Map // string -> *gsqlPath
	pathCacheSize atomic.Int64
)

// maxCachedPaths bounds pathCache so per-row computed paths cannot grow
// it without limit; past the cap paths are parsed on every call.
const maxCachedPaths = 4096

// createPath parses a JSONPath expression. Parsed paths are cached: the
// path is usually a literal repeated for every row. A cached *gsqlPath
// is never mutated.
func createPath(path string) (*gsqlPath, error) {
	if p, ok := pathCache.Load(path); ok {
		return p.(*gsqlPath), nil
	}
	p, err := parsePath(path)
	if err != nil {
		return nil, err
	}
	if pathCacheSize.Load() < maxCachedPaths {
		if _, loaded := pathCache.LoadOrStore(path, p); !loaded {
			pathCacheSize.Add(1)
		}
	}
	return p, nil
}

func parsePath(path string) (*gsqlPath, error) {
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
// when raw is a JSON object. It scans the bytes directly (no decoder)
// because JSON_VALUE and friends run it once per row.
func rawObjectMember(raw []byte, key string) ([]byte, bool) {
	if len(raw) == 0 || raw[0] != '{' {
		return nil, false
	}
	i := skipSpace(raw, 1)
	if i < len(raw) && raw[i] == '}' {
		return nil, false
	}
	for i < len(raw) {
		if raw[i] != '"' {
			return nil, false
		}
		end, ok := skipString(raw, i)
		if !ok {
			return nil, false
		}
		k := raw[i+1 : end-1]
		i = skipSpace(raw, end)
		if i >= len(raw) || raw[i] != ':' {
			return nil, false
		}
		i = skipSpace(raw, i+1)
		vEnd, ok := skipValue(raw, i)
		if !ok {
			return nil, false
		}
		if keyEquals(k, key) {
			return raw[i:vEnd], true
		}
		i = skipSpace(raw, vEnd)
		if i >= len(raw) {
			return nil, false
		}
		if raw[i] == '}' {
			return nil, false
		}
		if raw[i] != ',' {
			return nil, false
		}
		i = skipSpace(raw, i+1)
	}
	return nil, false
}

// rawArrayElem returns the raw idx-th element when raw is a JSON array.
func rawArrayElem(raw []byte, idx int) ([]byte, bool) {
	if len(raw) == 0 || raw[0] != '[' || idx < 0 {
		return nil, false
	}
	i := skipSpace(raw, 1)
	if i < len(raw) && raw[i] == ']' {
		return nil, false
	}
	for n := 0; i < len(raw); n++ {
		vEnd, ok := skipValue(raw, i)
		if !ok {
			return nil, false
		}
		if n == idx {
			return raw[i:vEnd], true
		}
		i = skipSpace(raw, vEnd)
		if i >= len(raw) || raw[i] != ',' {
			return nil, false
		}
		i = skipSpace(raw, i+1)
	}
	return nil, false
}

// keyEquals compares a raw (still escaped) member name with key.
func keyEquals(rawKey []byte, key string) bool {
	if bytes.IndexByte(rawKey, 0x5c) < 0 {
		return string(rawKey) == key
	}
	var k string
	if err := stdjson.Unmarshal(append(append([]byte{'"'}, rawKey...), '"'), &k); err != nil {
		return false
	}
	return k == key
}

func skipSpace(b []byte, i int) int {
	for i < len(b) && (b[i] == ' ' || b[i] == '\t' || b[i] == '\n' || b[i] == '\r') {
		i++
	}
	return i
}

// skipString returns the index just past the string starting at b[i].
func skipString(b []byte, i int) (int, bool) {
	for j := i + 1; j < len(b); j++ {
		switch b[j] {
		case 0x5c:
			j++
		case '"':
			return j + 1, true
		}
	}
	return 0, false
}

// skipValue returns the index just past the JSON value starting at b[i].
func skipValue(b []byte, i int) (int, bool) {
	if i >= len(b) {
		return 0, false
	}
	switch b[i] {
	case '"':
		return skipString(b, i)
	case '{', '[':
		depth := 0
		for j := i; j < len(b); j++ {
			switch b[j] {
			case '"':
				end, ok := skipString(b, j)
				if !ok {
					return 0, false
				}
				j = end - 1
			case '{', '[':
				depth++
			case '}', ']':
				depth--
				if depth == 0 {
					return j + 1, true
				}
			}
		}
		return 0, false
	}
	j := i
	for j < len(b) && b[j] != ',' && b[j] != '}' && b[j] != ']' && b[j] != ' ' && b[j] != '\t' && b[j] != '\n' && b[j] != '\r' {
		j++
	}
	if j == i {
		return 0, false
	}
	return j, true
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

// jsonElementsToStrings turns the JSON elements of an extracted array
// into their STRING text, for the STRING-input forms of
// JSON_QUERY_ARRAY / JSON_EXTRACT_ARRAY.
func jsonElementsToStrings(v value.Value) {
	arr, ok := v.(*value.ArrayValue)
	if !ok {
		return
	}
	for i, e := range arr.Values {
		if jv, ok := e.(value.JsonValue); ok {
			arr.Values[i] = value.StringValue(string(jv))
		}
	}
}
