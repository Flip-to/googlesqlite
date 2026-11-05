package internal

import (
	"context"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/goccy/googlesqlite/internal/value"
)

type partitionExprsKey struct{}

// withPartitionExprs carries the PARTITION BY expressions of the CREATE TABLE
// statements in a query (keyed by lower-cased table name) to the spec builders,
// because the clause is stripped from the SQL before analysis.
func withPartitionExprs(ctx context.Context, m map[string]string) context.Context {
	if len(m) == 0 {
		return ctx
	}
	return context.WithValue(ctx, partitionExprsKey{}, m)
}

func partitionExprFromContext(ctx context.Context, namePath []string) string {
	m, _ := ctx.Value(partitionExprsKey{}).(map[string]string)
	if len(m) == 0 || len(namePath) == 0 {
		return ""
	}
	// A backtick-quoted `p.ds.t` arrives as one path element.
	last := namePath[len(namePath)-1]
	if idx := strings.LastIndexByte(last, '.'); idx >= 0 {
		last = last[idx+1:]
	}
	return m[strings.ToLower(last)]
}

// extractPartitionByExprs returns the top-level `PARTITION BY <expr>` clause of
// every CREATE TABLE statement in query, keyed by the lower-cased last path
// element of the table name. It mirrors the scan of stripPartitionByClause.
func extractPartitionByExprs(query string) map[string]string {
	if !strings.Contains(query, "PARTITION") && !strings.Contains(query, "partition") {
		return nil
	}
	upper := strings.ToUpper(query)
	out := map[string]string{}
	depth := 0
	lastTable := ""
	i := 0
	for i < len(query) {
		c := query[i]
		if c == '\'' || c == '"' {
			i = scanStringLiteral(query, i)
			continue
		}
		if c == '`' {
			end := i + 1
			for end < len(query) && query[end] != '`' {
				end++
			}
			if end < len(query) {
				end++
			}
			i = end
			continue
		}
		switch c {
		case '(':
			depth++
			i++
			continue
		case ')':
			depth--
			i++
			continue
		case ';':
			lastTable = ""
		}
		if !isIdentStart(c) {
			i++
			continue
		}
		j := i + 1
		for j < len(query) && isIdentCont(query[j]) {
			j++
		}
		word := upper[i:j]
		if depth == 0 && word == "TABLE" && lastTable == "" {
			lastTable = readTableName(query, j)
		}
		if depth == 0 && word == "PARTITION" && lastTable != "" {
			k := skipSpaces(query, j)
			if k+2 <= len(query) && upper[k:k+2] == "BY" && (k+2 == len(query) || !isIdentCont(query[k+2])) {
				k += 2
				start := k
				exprDepth := 0
				for k < len(query) {
					ch := query[k]
					if ch == '\'' || ch == '"' {
						k = scanStringLiteral(query, k)
						continue
					}
					if ch == '(' {
						exprDepth++
					} else if ch == ')' {
						if exprDepth == 0 {
							break
						}
						exprDepth--
					} else if exprDepth == 0 && isIdentStart(ch) {
						kEnd := k + 1
						for kEnd < len(query) && isIdentCont(query[kEnd]) {
							kEnd++
						}
						if w := upper[k:kEnd]; w == "CLUSTER" || w == "OPTIONS" || w == "AS" {
							break
						}
						k = kEnd
						continue
					} else if ch == ';' && exprDepth == 0 {
						break
					}
					k++
				}
				out[lastTable] = strings.TrimSpace(query[start:k])
				i = k
				continue
			}
		}
		i = j
	}
	return out
}

func skipSpaces(s string, i int) int {
	for i < len(s) && (s[i] == ' ' || s[i] == '\t' || s[i] == '\n' || s[i] == '\r') {
		i++
	}
	return i
}

// readTableName reads the table name after the TABLE keyword (skipping IF NOT
// EXISTS) and returns its lower-cased last path element.
func readTableName(query string, i int) string {
	i = skipSpaces(query, i)
	if strings.HasPrefix(strings.ToUpper(query[i:]), "IF NOT EXISTS") {
		i = skipSpaces(query, i+len("IF NOT EXISTS"))
	}
	start := i
	for i < len(query) {
		c := query[i]
		if c == '`' {
			i++
			for i < len(query) && query[i] != '`' {
				i++
			}
			if i < len(query) {
				i++
			}
			continue
		}
		if isIdentCont(c) || c == '.' || c == '-' {
			i++
			continue
		}
		break
	}
	name := strings.ReplaceAll(query[start:i], "`", "")
	if idx := strings.LastIndexByte(name, '.'); idx >= 0 {
		name = name[idx+1:]
	}
	return strings.ToLower(name)
}

// partitionSpec is a PARTITION BY expression the emulator can evaluate.
type partitionSpec struct {
	column string
	// kind is "time" (id from a DATE / DATETIME / TIMESTAMP value) or "range".
	kind   string
	layout string // Go time layout of the partition_id for kind "time"
	// range partitioning: start, end, interval
	start, end, interval int64
}

var (
	partColumnRe = regexp.MustCompile("^`?([A-Za-z_][A-Za-z0-9_]*)`?$")
	partFuncRe   = regexp.MustCompile(`(?is)^(DATE|TIMESTAMP_TRUNC|DATETIME_TRUNC|DATE_TRUNC)\s*\(\s*` + "`?" + `([A-Za-z_][A-Za-z0-9_]*)` + "`?" + `\s*(?:,\s*([A-Za-z]+)\s*)?(?:,\s*(?:'[^']*'|"[^"]*")\s*)?\)$`)
	partRangeRe  = regexp.MustCompile(`(?is)^RANGE_BUCKET\s*\(\s*` + "`?" + `([A-Za-z_][A-Za-z0-9_]*)` + "`?" + `\s*,\s*GENERATE_ARRAY\s*\(\s*(-?\d+)\s*,\s*(-?\d+)\s*(?:,\s*(-?\d+)\s*)?\)\s*\)$`)
)

// partitionSpecFromExpr parses a PARTITION BY expression: a column, DATE(col),
// TIMESTAMP_TRUNC / DATETIME_TRUNC / DATE_TRUNC(col, DAY|HOUR|MONTH|YEAR) and
// RANGE_BUCKET(col, GENERATE_ARRAY(start, end[, step])). It returns nil for
// anything else (empty, ingestion time, other expressions).
func partitionSpecFromExpr(expr string) *partitionSpec {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return nil
	}
	if m := partColumnRe.FindStringSubmatch(expr); m != nil {
		return timePartition(m[1], "DAY")
	}
	if m := partFuncRe.FindStringSubmatch(expr); m != nil {
		trunc := "DAY"
		if strings.EqualFold(m[1], "DATE") {
			if m[3] != "" {
				return nil
			}
		} else if m[3] != "" {
			trunc = strings.ToUpper(m[3])
		}
		return timePartition(m[2], trunc)
	}
	if m := partRangeRe.FindStringSubmatch(expr); m != nil {
		start, _ := strconv.ParseInt(m[2], 10, 64)
		end, _ := strconv.ParseInt(m[3], 10, 64)
		step := int64(1)
		if m[4] != "" {
			step, _ = strconv.ParseInt(m[4], 10, 64)
		}
		if step <= 0 || end <= start {
			return nil
		}
		return &partitionSpec{column: m[1], kind: "range", start: start, end: end, interval: step}
	}
	return nil
}

func timePartition(col, trunc string) *partitionSpec {
	layouts := map[string]string{"DAY": "20060102", "HOUR": "2006010215", "MONTH": "200601", "YEAR": "2006"}
	layout, ok := layouts[trunc]
	if !ok {
		return nil
	}
	return &partitionSpec{column: col, kind: "time", layout: layout}
}

// count groups the stored column values by partition_id. It returns the row
// count per id and the ids in order ('__NULL__' and '__UNPARTITIONED__ sort
// after the digits only by accident of ASCII; BigQuery does not guarantee an
// order either).
func (p *partitionSpec) count(vals []any) (map[string]int64, []string) {
	counts := map[string]int64{}
	for _, raw := range vals {
		counts[p.id(raw)]++
	}
	ids := make([]string, 0, len(counts))
	for id := range counts {
		ids = append(ids, id)
	}
	for i := 1; i < len(ids); i++ {
		for j := i; j > 0 && partitionLess(ids[j], ids[j-1]); j-- {
			ids[j], ids[j-1] = ids[j-1], ids[j]
		}
	}
	return counts, ids
}

// partitionLess orders numeric ids numerically (integer ranges, possibly
// negative), everything else lexically.
func partitionLess(a, b string) bool {
	ai, aerr := strconv.ParseInt(a, 10, 64)
	bi, berr := strconv.ParseInt(b, 10, 64)
	if aerr == nil && berr == nil {
		return ai < bi
	}
	return a < b
}

func (p *partitionSpec) id(raw any) string {
	if raw == nil {
		return "__NULL__"
	}
	v, err := value.DecodeValue(raw)
	if err != nil || v == nil {
		return "__NULL__"
	}
	if p.kind == "range" {
		n, err := v.ToInt64()
		if err != nil {
			return "__NULL__"
		}
		if n < p.start || n >= p.end {
			return "__UNPARTITIONED__"
		}
		return strconv.FormatInt(p.start+(n-p.start)/p.interval*p.interval, 10)
	}
	var t time.Time
	switch vv := v.(type) {
	case value.DateValue:
		t = time.Time(vv)
	case value.DatetimeValue:
		t = time.Time(vv)
	case value.TimestampValue:
		t = time.Time(vv).UTC()
	default:
		return "__NULL__"
	}
	return t.Format(p.layout)
}
