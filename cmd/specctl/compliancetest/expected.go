package compliancetest

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/big"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Val is a typed value from an expected block, canonicalised so it can
// be compared against a driver result.
type Val struct {
	// Kind is the type kind (INT64, STRING, ARRAY, STRUCT, ...).
	Kind string
	// Type is the full type, when known.
	Type *Type
	Null bool
	// S is the canonical scalar text (see Canon).
	S string
	// F holds the numeric value for DOUBLE / FLOAT.
	F       float64
	IsFloat bool
	// Elems holds ARRAY elements or STRUCT fields.
	Elems []Val
	// Ordered is true for an ARRAY written with `known order:`.
	Ordered bool
}

// Result is the parsed first section of an expected block.
type Result struct {
	// Err is set when the expected block is an error.
	Err *ExpectedErr
	// Type is the header type, e.g. ARRAY<STRUCT<a INT64>>.
	Type *Type
	// Rows is the top-level value (an ARRAY).
	Rows Val
}

// ExpectedErr is a parsed `ERROR: <code>: <message>` block.
type ExpectedErr struct {
	Code    string // e.g. "generic::out_of_range"
	Message string
}

// FirstSection returns the part of an expected block before any
// further `--` separator (later sections hold label dumps).
func FirstSection(expected string) string {
	lines := strings.Split(expected, "\n")
	for i, l := range lines {
		if strings.TrimSpace(l) == "--" {
			return strings.TrimSpace(strings.Join(lines[:i], "\n"))
		}
	}
	return strings.TrimSpace(expected)
}

var errRe = regexp.MustCompile(`(?s)^ERROR:\s*([A-Za-z_:]+)\s*(?::\s*(.*))?$`)

// ParseResult parses the first section of an expected block into
// either an error or a typed row set. Only top-level ARRAY types are
// accepted; other shapes (DML STRUCT results, script results) return
// an error so the caller can record them as unsupported.
func ParseResult(expected string) (Result, error) {
	s := FirstSection(expected)
	if strings.HasPrefix(s, "ERROR:") {
		m := errRe.FindStringSubmatch(s)
		if m == nil {
			return Result{Err: &ExpectedErr{Message: strings.TrimSpace(strings.TrimPrefix(s, "ERROR:"))}}, nil
		}
		return Result{Err: &ExpectedErr{Code: m[1], Message: strings.TrimSpace(m[2])}}, nil
	}
	if !strings.HasPrefix(s, "ARRAY<") {
		return Result{}, fmt.Errorf("unsupported result shape %q", firstLine(s))
	}
	end := matchAngle(s, len("ARRAY"))
	if end < 0 {
		return Result{}, fmt.Errorf("unbalanced type header")
	}
	t, err := ParseType(s[:end+1])
	if err != nil {
		return Result{}, err
	}
	p := &valParser{s: s, i: end + 1}
	v, err := p.value(t)
	if err != nil {
		return Result{}, err
	}
	p.ws()
	// Some cases append a free-text "NOTE: ..." after the value.
	if p.i != len(p.s) && !strings.HasPrefix(p.s[p.i:], "NOTE") {
		return Result{}, fmt.Errorf("trailing text after value: %q", clip(p.s[p.i:], 40))
	}
	return Result{Type: t, Rows: v}, nil
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

func clip(s string, n int) string {
	if len(s) > n {
		return s[:n] + "..."
	}
	return s
}

// matchAngle returns the index of the '>' matching the '<' at s[open].
func matchAngle(s string, open int) int {
	depth := 0
	for i := open; i < len(s); i++ {
		switch s[i] {
		case '<':
			depth++
		case '>':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

type valParser struct {
	s string
	i int
}

func (p *valParser) ws() {
	for p.i < len(p.s) && strings.ContainsRune(" \t\r\n", rune(p.s[p.i])) {
		p.i++
	}
}

func (p *valParser) peekWord(w string) bool {
	if !strings.HasPrefix(p.s[p.i:], w) {
		return false
	}
	j := p.i + len(w)
	return j >= len(p.s) || strings.ContainsRune(" \t\r\n,}])", rune(p.s[j]))
}

func (p *valParser) expect(c byte) error {
	p.ws()
	if p.i >= len(p.s) || p.s[p.i] != c {
		return fmt.Errorf("expected %q at %q", c, clip(p.s[p.i:], 40))
	}
	p.i++
	return nil
}

func (p *valParser) value(t *Type) (Val, error) {
	p.ws()
	if p.peekWord("NULL") {
		p.i += 4
		return Val{Kind: t.Kind, Type: t, Null: true}, nil
	}
	switch t.Kind {
	case "ARRAY":
		return p.array(t)
	case "STRUCT":
		return p.structVal(t)
	case "STRING":
		raw, err := p.quoted()
		if err != nil {
			return Val{}, err
		}
		return Val{Kind: t.Kind, Type: t, S: raw}, nil
	case "BYTES":
		if p.i < len(p.s) && (p.s[p.i] == 'b' || p.s[p.i] == 'B') {
			p.i++
		}
		raw, err := p.quoted()
		if err != nil {
			return Val{}, err
		}
		return Val{Kind: t.Kind, Type: t, S: raw}, nil
	}
	tok := p.rawToken()
	if t.Kind == "RANGE" {
		return canonRange(t, tok)
	}
	return Canon(t, tok)
}

func (p *valParser) array(t *Type) (Val, error) {
	elem := t.Elem
	if strings.HasPrefix(p.s[p.i:], "ARRAY<") {
		end := matchAngle(p.s, p.i+len("ARRAY"))
		if end < 0 {
			return Val{}, fmt.Errorf("unbalanced ARRAY<> prefix")
		}
		at, err := ParseType(p.s[p.i : end+1])
		if err != nil {
			return Val{}, err
		}
		if at.Elem != nil {
			elem = at.Elem
			t = at
		}
		p.i = end + 1
		p.ws()
		// Typed NULL array: ARRAY<T>(NULL).
		if strings.HasPrefix(p.s[p.i:], "(NULL)") {
			p.i += len("(NULL)")
			return Val{Kind: "ARRAY", Type: t, Null: true}, nil
		}
	}
	if err := p.expect('['); err != nil {
		return Val{}, err
	}
	v := Val{Kind: "ARRAY", Type: t}
	p.ws()
	for _, ann := range []string{"known order:", "unknown order:"} {
		if strings.HasPrefix(p.s[p.i:], ann) {
			v.Ordered = ann == "known order:"
			p.i += len(ann)
		}
	}
	for {
		p.ws()
		if p.i < len(p.s) && p.s[p.i] == ']' {
			p.i++
			return v, nil
		}
		if len(v.Elems) > 0 {
			if err := p.expect(','); err != nil {
				return Val{}, err
			}
			p.ws()
			// Tolerate a trailing comma.
			if p.i < len(p.s) && p.s[p.i] == ']' {
				p.i++
				return v, nil
			}
		}
		if elem == nil {
			return Val{}, fmt.Errorf("array element type unknown")
		}
		e, err := p.value(elem)
		if err != nil {
			return Val{}, err
		}
		v.Elems = append(v.Elems, e)
	}
}

func (p *valParser) structVal(t *Type) (Val, error) {
	if err := p.expect('{'); err != nil {
		return Val{}, err
	}
	v := Val{Kind: "STRUCT", Type: t}
	for idx := 0; ; idx++ {
		p.ws()
		if p.i < len(p.s) && p.s[p.i] == '}' {
			p.i++
			if len(v.Elems) != len(t.Fields) {
				return Val{}, fmt.Errorf("struct has %d values for %d fields", len(v.Elems), len(t.Fields))
			}
			return v, nil
		}
		if idx > 0 {
			if err := p.expect(','); err != nil {
				return Val{}, err
			}
		}
		if idx >= len(t.Fields) {
			return Val{}, fmt.Errorf("struct has more values than fields (%d)", len(t.Fields))
		}
		e, err := p.value(t.Fields[idx].Type)
		if err != nil {
			return Val{}, err
		}
		v.Elems = append(v.Elems, e)
	}
}

// quoted reads a "..." or '...' literal and decodes escapes.
func (p *valParser) quoted() (string, error) {
	p.ws()
	if p.i >= len(p.s) || (p.s[p.i] != '"' && p.s[p.i] != '\'') {
		return "", fmt.Errorf("expected quoted literal at %q", clip(p.s[p.i:], 40))
	}
	q := p.s[p.i]
	// Triple-quoted literal.
	if strings.HasPrefix(p.s[p.i:], strings.Repeat(string(q), 3)) {
		end := strings.Index(p.s[p.i+3:], strings.Repeat(string(q), 3))
		if end < 0 {
			return "", fmt.Errorf("unterminated triple-quoted literal")
		}
		raw := p.s[p.i+3 : p.i+3+end]
		p.i += 3 + end + 3
		return unquoteString(raw)
	}
	j := p.i + 1
	for j < len(p.s) {
		if p.s[j] == '\\' {
			j += 2
			continue
		}
		if p.s[j] == q {
			break
		}
		j++
	}
	if j >= len(p.s) {
		return "", fmt.Errorf("unterminated literal")
	}
	raw := p.s[p.i+1 : j]
	p.i = j + 1
	return unquoteString(raw)
}

// rawToken reads up to the next top-level ',', '}' or ']'.
func (p *valParser) rawToken() string {
	start := p.i
	depth := 0
	var quote byte
	for p.i < len(p.s) {
		c := p.s[p.i]
		if quote != 0 {
			if c == '\\' {
				p.i += 2
				continue
			}
			if c == quote {
				quote = 0
			}
			p.i++
			continue
		}
		switch c {
		case '"', '\'':
			quote = c
		case '{', '(':
			depth++
		case '[':
			depth++
		case '}', ']', ')':
			if depth == 0 {
				return strings.TrimSpace(p.s[start:p.i])
			}
			depth--
			// A RANGE literal closes '[' with ')'.
		case ',':
			if depth == 0 {
				return strings.TrimSpace(p.s[start:p.i])
			}
		}
		p.i++
	}
	return strings.TrimSpace(p.s[start:p.i])
}

// Canon canonicalises a scalar given as text (expected-side token or
// driver string) for the scalar type t.
func Canon(t *Type, s string) (Val, error) {
	s = strings.TrimSpace(s)
	v := Val{Kind: t.Kind, Type: t}
	if s == "NULL" {
		v.Null = true
		return v, nil
	}
	switch t.Kind {
	case "INT64", "INT32", "UINT32", "UINT64":
		n, ok := new(big.Int).SetString(s, 10)
		if !ok {
			return v, fmt.Errorf("bad integer %q", s)
		}
		v.S = n.String()
	case "BOOL":
		switch strings.ToLower(s) {
		case "true", "1":
			v.S = "true"
		case "false", "0":
			v.S = "false"
		default:
			return v, fmt.Errorf("bad bool %q", s)
		}
	case "DOUBLE", "FLOAT", "FLOAT64", "FLOAT32":
		f, err := parseFloat(s)
		if err != nil {
			return v, err
		}
		v.F, v.IsFloat = f, true
		v.S = strconv.FormatFloat(f, 'g', -1, 64)
	case "NUMERIC", "BIGNUMERIC":
		r, ok := new(big.Rat).SetString(s)
		if !ok {
			return v, fmt.Errorf("bad numeric %q", s)
		}
		v.S = r.RatString()
	case "TIMESTAMP":
		ts, err := parseTimestamp(s)
		if err != nil {
			return v, err
		}
		v.S = ts
	case "DATETIME":
		v.S = trimFrac(strings.Replace(s, "T", " ", 1))
	case "TIME":
		v.S = trimFrac(s)
	case "INTERVAL":
		v.S = canonInterval(s)
	case "JSON":
		j, err := canonJSON(s)
		if err != nil {
			return v, err
		}
		v.S = j
	case "GEOGRAPHY":
		v.S = strings.Join(strings.Fields(strings.ReplaceAll(s, " (", "(")), " ")
	case "UUID":
		v.S = strings.ToLower(s)
	default:
		v.S = s
	}
	return v, nil
}

func canonRange(t *Type, s string) (Val, error) {
	v := Val{Kind: "RANGE", Type: t}
	if !strings.HasPrefix(s, "[") || !strings.HasSuffix(s, ")") {
		return v, fmt.Errorf("bad range %q", s)
	}
	parts := splitTopLevelCommas(s[1 : len(s)-1])
	if len(parts) != 2 || t.Elem == nil {
		return v, fmt.Errorf("bad range %q", s)
	}
	var out []string
	for _, part := range parts {
		if part == "UNBOUNDED" || part == "NULL" {
			out = append(out, "UNBOUNDED")
			continue
		}
		e, err := Canon(t.Elem, part)
		if err != nil {
			return v, err
		}
		out = append(out, e.S)
	}
	v.S = "[" + out[0] + ", " + out[1] + ")"
	return v, nil
}

func parseFloat(s string) (float64, error) {
	switch strings.ToLower(strings.TrimPrefix(s, "+")) {
	case "nan", "-nan":
		return nan(), nil
	case "inf", "infinity":
		return inf(1), nil
	case "-inf", "-infinity":
		return inf(-1), nil
	}
	return strconv.ParseFloat(s, 64)
}

func trimFrac(s string) string {
	dot := strings.LastIndexByte(s, '.')
	if dot < 0 || strings.ContainsAny(s[dot:], " :-+") {
		return s
	}
	s = strings.TrimRight(s, "0")
	return strings.TrimSuffix(s, ".")
}

var tsOffsetRe = regexp.MustCompile(`([+-])(\d{1,2})(?::?(\d{2}))?$`)

// parseTimestamp converts the suite / driver timestamp text into a UTC
// canonical form with trailing fractional zeros removed.
func parseTimestamp(s string) (string, error) {
	s = strings.TrimSpace(s)
	s = strings.Replace(s, "T", " ", 1)
	offset := 0
	switch {
	case strings.HasSuffix(s, " UTC"):
		s = strings.TrimSuffix(s, " UTC")
	case strings.HasSuffix(s, "Z"):
		s = strings.TrimSuffix(s, "Z")
	default:
		// The offset follows the time part, so search after the date.
		if len(s) > 10 {
			if m := tsOffsetRe.FindStringSubmatchIndex(s[10:]); m != nil {
				sub := tsOffsetRe.FindStringSubmatch(s[10:])
				h, _ := strconv.Atoi(sub[2])
				mm := 0
				if sub[3] != "" {
					mm, _ = strconv.Atoi(sub[3])
				}
				offset = h*3600 + mm*60
				if sub[1] == "-" {
					offset = -offset
				}
				s = strings.TrimSpace(s[:10+m[0]])
			}
		}
	}
	if !strings.Contains(s, " ") {
		s += " 00:00:00"
	}
	tm, err := time.Parse("2006-01-02 15:04:05.999999999", s)
	if err != nil {
		return "", fmt.Errorf("bad timestamp %q: %w", s, err)
	}
	tm = tm.Add(-time.Duration(offset) * time.Second)
	return trimFrac(tm.Format("2006-01-02 15:04:05.000000000")), nil
}

var intervalRe = regexp.MustCompile(`^(-?)(\d+)-(\d+) (-?\d+) (-?)(\d+):(\d+):(\d+)(\.\d+)?$`)

func canonInterval(s string) string {
	m := intervalRe.FindStringSubmatch(s)
	if m == nil {
		return s
	}
	atoi := func(x string) int64 { n, _ := strconv.ParseInt(x, 10, 64); return n }
	frac := strings.TrimRight(m[9], "0")
	if frac == "." {
		frac = ""
	}
	return fmt.Sprintf("%s%d-%d %d %s%d:%d:%d%s", m[1], atoi(m[2]), atoi(m[3]), atoi(m[4]), m[5], atoi(m[6]), atoi(m[7]), atoi(m[8]), frac)
}

// canonJSON re-encodes a JSON document with sorted keys and numbers in
// canonical rational form, so formatting differences do not count.
func canonJSON(s string) (string, error) {
	dec := json.NewDecoder(strings.NewReader(s))
	dec.UseNumber()
	var x any
	if err := dec.Decode(&x); err != nil {
		return "", fmt.Errorf("bad json %q: %w", clip(s, 60), err)
	}
	x = normJSONNumbers(x)
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(x); err != nil {
		return "", err
	}
	return strings.TrimSpace(b.String()), nil
}

func normJSONNumbers(x any) any {
	switch v := x.(type) {
	case json.Number:
		if r, ok := new(big.Rat).SetString(v.String()); ok {
			return json.Number(r.RatString())
		}
		return v
	case []any:
		for i := range v {
			v[i] = normJSONNumbers(v[i])
		}
	case map[string]any:
		for k := range v {
			v[k] = normJSONNumbers(v[k])
		}
	}
	return x
}

// String renders v in the suite's value notation (approximately).
func (v Val) String() string {
	if v.Null {
		return "NULL"
	}
	switch v.Kind {
	case "ARRAY":
		parts := make([]string, len(v.Elems))
		for i, e := range v.Elems {
			parts[i] = e.String()
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case "STRUCT":
		parts := make([]string, len(v.Elems))
		for i, e := range v.Elems {
			parts[i] = e.String()
		}
		return "{" + strings.Join(parts, ", ") + "}"
	case "STRING":
		return strconv.Quote(v.S)
	case "BYTES":
		return "b" + strconv.Quote(v.S)
	}
	return v.S
}
