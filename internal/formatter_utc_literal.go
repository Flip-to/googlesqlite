package internal

import (
	"context"
	"errors"
	"regexp"
	"strconv"
	"strings"

	googlesql "github.com/goccy/go-googlesql"

	"github.com/goccy/googlesqlite/internal/value"
)

// The analyzer folds constant expressions over literals (literal casts
// and literal coercions) into a single ResolvedLiteral, and it does so
// in its built-in default time zone, America/Los_Angeles. go-googlesql
// v0.4.0 cannot be configured with a UTC TimeZone, so every folded
// value whose conversion depends on the time zone comes out shifted by
// the Los Angeles offset. BigQuery evaluates the same conversions in
// UTC.
//
// The folded shapes are (see docs/decisions/analyzer-default-time-zone.md):
//
//   - CAST / SAFE_CAST of a literal expression to TIMESTAMP, or from a
//     TIMESTAMP to DATE / DATETIME / TIME / STRING (nested casts fold
//     into one literal whose parse location spans the outermost cast);
//   - a STRING literal implicitly coerced to TIMESTAMP (comparison with
//     a TIMESTAMP, TIMESTAMP function argument, INSERT into a TIMESTAMP
//     column);
//   - STRUCT / ARRAY literals built from such values.
//
// Function calls (DATE(ts), STRING(ts), EXTRACT, TIMESTAMP_ADD, ...)
// are never folded and reach the runtime, which works in UTC.
// TIMESTAMP '...' and RANGE<TIMESTAMP> '...' literals without a zone
// are rewritten to carry +00:00 before analysis
// (applyNaiveTimestampUTC), so they are exact.
//
// utcLiteralSQL recovers the source text of a zone-dependent folded
// scalar literal through its parse location and re-evaluates it with
// the runtime CAST, which uses UTC. Shapes it cannot evaluate are
// reported through the zoneFoldState in the context so the statement
// is re-analyzed with literal-cast folding disabled.

// errZoneFoldedLiteral reports a folded literal whose value depends on
// the analyzer's default time zone and that could not be re-evaluated
// in UTC from its source text.
var errZoneFoldedLiteral = errors.New("literal folded in the analyzer default time zone")

type zoneFoldStateKey struct{}

// zoneFoldState carries, for one statement, whether an unrecoverable
// zone-dependent folded literal was seen (needRefold) and whether the
// caller can re-analyze the statement without folding (allowRefold).
type zoneFoldState struct {
	hasTimestampKeyword bool
	allowRefold         bool
	needRefold          bool
}

func withZoneFoldState(ctx context.Context, s *zoneFoldState) context.Context {
	return context.WithValue(ctx, zoneFoldStateKey{}, s)
}

func zoneFoldStateFromContext(ctx context.Context) *zoneFoldState {
	s, _ := ctx.Value(zoneFoldStateKey{}).(*zoneFoldState)
	return s
}

var timestampKeywordRe = regexp.MustCompile(`(?i)\bTIMESTAMP\b`)

// newZoneFoldState builds the per-statement state for query.
func newZoneFoldState(query string, allowRefold bool) *zoneFoldState {
	return &zoneFoldState{
		hasTimestampKeyword: timestampKeywordRe.MatchString(query),
		allowRefold:         allowRefold,
	}
}

// utcLiteralSQL returns the SQL for lit when its folded value depends on
// the analyzer's default time zone. handled is false when the literal
// is not zone-dependent and should be formatted as usual.
func utcLiteralSQL(ctx context.Context, lit *googlesql.ResolvedLiteral) (sql string, handled bool, err error) {
	state := zoneFoldStateFromContext(ctx)
	if state == nil {
		return "", false, nil
	}
	typ, _ := lit.Type()
	if typ == nil {
		return "", false, nil
	}
	kind, _ := typ.Kind()
	switch kind {
	case googlesql.TypeKindTypeTimestamp:
	case googlesql.TypeKindTypeDate, googlesql.TypeKindTypeDatetime, googlesql.TypeKindTypeTime, googlesql.TypeKindTypeString:
		// Only a cast from a TIMESTAMP yields a zone-dependent value of
		// these types, and that requires the TIMESTAMP keyword.
		if !state.hasTimestampKeyword {
			return "", false, nil
		}
		if explicit, _ := lit.HasExplicitType(); !explicit {
			return "", false, nil
		}
	case googlesql.TypeKindTypeArray, googlesql.TypeKindTypeStruct:
		return compositeZoneLiteral(ctx, state, lit, typ)
	default:
		return "", false, nil
	}
	v, _ := lit.Value()
	if v == nil || m1(v.IsNull()) {
		return "", false, nil
	}
	src, ok := literalSourceText(ctx, lit)
	if !ok {
		return "", false, nil
	}
	if kind == googlesql.TypeKindTypeTimestamp {
		if isTypedStringLiteral(src, "TIMESTAMP") {
			return "", false, nil
		}
	} else if !startsWithCast(src) || !timestampKeywordRe.MatchString(src) {
		return "", false, nil
	}
	out, err := evalConstantInUTC(src, kind)
	if err != nil {
		return state.fallback()
	}
	if out == nil {
		return "NULL", true, nil
	}
	s, err := literalFromValue(out)
	if err != nil {
		return state.fallback()
	}
	return s, true, nil
}

// fallback records that the statement must be re-analyzed without
// literal-cast folding. When the caller cannot do that, the folded
// value is formatted as usual.
func (s *zoneFoldState) fallback() (string, bool, error) {
	if !s.allowRefold {
		return "", false, nil
	}
	s.needRefold = true
	return "", true, errZoneFoldedLiteral
}

// compositeZoneLiteral handles ARRAY / STRUCT literals whose elements
// may have been folded in the default time zone. They are not
// re-evaluated here; the statement is re-analyzed without folding.
func compositeZoneLiteral(ctx context.Context, state *zoneFoldState, lit *googlesql.ResolvedLiteral, typ googlesql.Googlesql_TypeNode) (string, bool, error) {
	if !state.allowRefold || !state.hasTimestampKeyword {
		return "", false, nil
	}
	dbg, _ := typ.DebugString(false)
	if !strings.Contains(dbg, "TIMESTAMP") {
		// Only a composite with a TIMESTAMP-derived member can differ;
		// it needs a CAST in the source.
		src, ok := literalSourceText(ctx, lit)
		if !ok || !strings.Contains(strings.ToUpper(src), "CAST") || !timestampKeywordRe.MatchString(src) {
			return "", false, nil
		}
		return state.fallback()
	}
	src, ok := literalSourceText(ctx, lit)
	if !ok {
		return "", false, nil
	}
	if !hasUntypedString(src) && !strings.Contains(strings.ToUpper(src), "CAST") {
		return "", false, nil
	}
	return state.fallback()
}

func literalSourceText(ctx context.Context, lit *googlesql.ResolvedLiteral) (string, bool) {
	query, ok := sourceQueryFromContext(ctx)
	if !ok {
		return "", false
	}
	loc, _ := lit.GetParseLocationRangeOrNULL()
	if loc == nil {
		return "", false
	}
	text, err := loc.GetTextFrom(query)
	if err != nil {
		return "", false
	}
	return strings.TrimSpace(text), true
}

// isTypedStringLiteral reports whether src is `<keyword> '<string>'`.
func isTypedStringLiteral(src, keyword string) bool {
	p := &constParser{s: src}
	word := p.ident()
	if !strings.EqualFold(word, keyword) {
		return false
	}
	if _, err := p.stringLit(); err != nil {
		return false
	}
	p.skipSpace()
	return p.i == len(p.s)
}

func startsWithCast(src string) bool {
	p := &constParser{s: src}
	word := strings.ToUpper(p.ident())
	return word == "CAST" || word == "SAFE_CAST"
}

// hasUntypedString reports whether src contains a string literal that
// is not introduced by a TIMESTAMP / DATE / DATETIME / TIME keyword or
// by a RANGE<...> type.
func hasUntypedString(src string) bool {
	prev := ""
	for i := 0; i < len(src); {
		c := src[i]
		switch {
		case c == '\'' || c == '"':
			switch strings.ToUpper(prev) {
			case "TIMESTAMP", "DATE", "DATETIME", "TIME", ">":
			default:
				return true
			}
			i = scanStringLiteral(src, i)
			prev = ""
		case isIdentStart(c):
			j := i + 1
			for j < len(src) && isIdentCont(src[j]) {
				j++
			}
			prev = src[i:j]
			i = j
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			i++
		default:
			prev = string(c) // ">" closes RANGE<...>, a typed literal
			i++
		}
	}
	return false
}

var constTypeKinds = map[string]googlesql.TypeKind{
	"TIMESTAMP": googlesql.TypeKindTypeTimestamp,
	"DATE":      googlesql.TypeKindTypeDate,
	"DATETIME":  googlesql.TypeKindTypeDatetime,
	"TIME":      googlesql.TypeKindTypeTime,
	"STRING":    googlesql.TypeKindTypeString,
}

func constType(kind googlesql.TypeKind) *Type {
	for name, k := range constTypeKinds {
		if k == kind {
			return &Type{Name: name, Kind: int(kind)}
		}
	}
	return nil
}

// evalConstantInUTC evaluates src, a constant expression made of
// string / date-time literals and CAST / SAFE_CAST, with the runtime
// cast (UTC), then coerces the result to want (the folded literal's
// type; an untyped string literal coerced to TIMESTAMP is the implicit
// case). A nil value means SQL NULL.
func evalConstantInUTC(src string, want googlesql.TypeKind) (value.Value, error) {
	p := &constParser{s: src}
	v, kind, err := p.expr()
	if err != nil {
		return nil, err
	}
	p.skipSpace()
	if p.i != len(p.s) {
		return nil, errors.New("trailing input")
	}
	if v == nil || kind == want {
		return v, nil
	}
	return castConst(v, kind, want, false)
}

func castConst(v value.Value, from, to googlesql.TypeKind, safe bool) (value.Value, error) {
	ft, tt := constType(from), constType(to)
	if ft == nil || tt == nil {
		return nil, errors.New("unsupported cast")
	}
	return CAST(v, ft, tt, safe)
}

type constParser struct {
	s string
	i int
}

func (p *constParser) skipSpace() {
	for p.i < len(p.s) {
		switch p.s[p.i] {
		case ' ', '\t', '\n', '\r':
			p.i++
		default:
			return
		}
	}
}

func (p *constParser) ident() string {
	p.skipSpace()
	if p.i >= len(p.s) || !isIdentStart(p.s[p.i]) {
		return ""
	}
	j := p.i + 1
	for j < len(p.s) && isIdentCont(p.s[j]) {
		j++
	}
	w := p.s[p.i:j]
	p.i = j
	return w
}

func (p *constParser) expect(c byte) error {
	p.skipSpace()
	if p.i >= len(p.s) || p.s[p.i] != c {
		return errors.New("unexpected token")
	}
	p.i++
	return nil
}

// expr := '(' expr ')' | [SAFE_]CAST '(' expr AS type ')' | NULL
//
//	| TYPE string | string
func (p *constParser) expr() (value.Value, googlesql.TypeKind, error) {
	p.skipSpace()
	if p.i >= len(p.s) {
		return nil, 0, errors.New("unexpected end")
	}
	switch c := p.s[p.i]; {
	case c == '(':
		p.i++
		v, k, err := p.expr()
		if err != nil {
			return nil, 0, err
		}
		return v, k, p.expect(')')
	case c == '\'' || c == '"':
		s, err := p.stringLit()
		if err != nil {
			return nil, 0, err
		}
		return value.StringValue(s), googlesql.TypeKindTypeString, nil
	case (c == 'r' || c == 'R') && p.i+1 < len(p.s) && (p.s[p.i+1] == '\'' || p.s[p.i+1] == '"'):
		s, err := p.stringLit()
		if err != nil {
			return nil, 0, err
		}
		return value.StringValue(s), googlesql.TypeKindTypeString, nil
	}
	word := strings.ToUpper(p.ident())
	switch word {
	case "CAST", "SAFE_CAST":
		if err := p.expect('('); err != nil {
			return nil, 0, err
		}
		v, from, err := p.expr()
		if err != nil {
			return nil, 0, err
		}
		if !strings.EqualFold(p.ident(), "AS") {
			return nil, 0, errors.New("expected AS")
		}
		to, ok := constTypeKinds[strings.ToUpper(p.ident())]
		if !ok {
			return nil, 0, errors.New("unsupported cast target")
		}
		if err := p.expect(')'); err != nil {
			return nil, 0, err
		}
		if v == nil {
			return nil, to, nil
		}
		out, err := castConst(v, from, to, word == "SAFE_CAST")
		if err != nil {
			return nil, 0, err
		}
		return out, to, nil
	case "NULL":
		return nil, googlesql.TypeKindTypeString, nil
	case "TIMESTAMP", "DATE", "DATETIME", "TIME":
		s, err := p.stringLit()
		if err != nil {
			return nil, 0, err
		}
		kind := constTypeKinds[word]
		out, err := castConst(value.StringValue(s), googlesql.TypeKindTypeString, kind, false)
		if err != nil {
			return nil, 0, err
		}
		return out, kind, nil
	}
	return nil, 0, errors.New("unsupported expression")
}

// stringLit parses a GoogleSQL string literal (quoted, triple-quoted,
// raw) at the current position.
func (p *constParser) stringLit() (string, error) {
	p.skipSpace()
	raw := false
	if p.i < len(p.s) && (p.s[p.i] == 'r' || p.s[p.i] == 'R') {
		raw = true
		p.i++
	}
	if p.i >= len(p.s) || (p.s[p.i] != '\'' && p.s[p.i] != '"') {
		return "", errors.New("expected string literal")
	}
	q := p.s[p.i]
	delim := string(q)
	if strings.HasPrefix(p.s[p.i:], strings.Repeat(delim, 3)) {
		delim = strings.Repeat(delim, 3)
	}
	p.i += len(delim)
	var b strings.Builder
	for p.i < len(p.s) {
		if strings.HasPrefix(p.s[p.i:], delim) {
			p.i += len(delim)
			return b.String(), nil
		}
		c := p.s[p.i]
		if c != '\\' {
			b.WriteByte(c)
			p.i++
			continue
		}
		if p.i+1 >= len(p.s) {
			return "", errors.New("unterminated escape")
		}
		e := p.s[p.i+1]
		if raw {
			b.WriteByte(c)
			b.WriteByte(e)
			p.i += 2
			continue
		}
		switch e {
		case '\\', '\'', '"', '`', '?':
			b.WriteByte(e)
		case 'n':
			b.WriteByte('\n')
		case 't':
			b.WriteByte('\t')
		case 'r':
			b.WriteByte('\r')
		case 'x', 'X':
			if p.i+4 > len(p.s) {
				return "", errors.New("bad hex escape")
			}
			n, err := strconv.ParseUint(p.s[p.i+2:p.i+4], 16, 8)
			if err != nil {
				return "", err
			}
			b.WriteByte(byte(n))
			p.i += 4
			continue
		default:
			return "", errors.New("unsupported escape")
		}
		p.i += 2
	}
	return "", errors.New("unterminated string literal")
}

// refoldableStmt reports whether building node's statement action is
// free of side effects, so the statement can be re-analyzed and
// formatted a second time when a folded literal needs it.
func refoldableStmt(node googlesql.ResolvedStatementNode) bool {
	kind, _ := node.NodeKind()
	switch kind {
	case googlesql.ResolvedNodeKindResolvedQueryStmt,
		googlesql.ResolvedNodeKindResolvedInsertStmt,
		googlesql.ResolvedNodeKindResolvedUpdateStmt,
		googlesql.ResolvedNodeKindResolvedDeleteStmt,
		googlesql.ResolvedNodeKindResolvedMergeStmt:
		return true
	}
	return false
}
