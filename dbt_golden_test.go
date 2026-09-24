package googlesqlite_test

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"os"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestDBTGolden replays every probe of the flipto-dbt differential
// harness (scripts/emulator_diff) against the driver and compares the
// result with the answer real BigQuery gave for the same SQL. The data
// comes from testdata/dbt_golden/probes.jsonl (see the README there);
// the comparison reproduces the harness's compare.py:
//
//   - column types must match after folding the legacy synonyms
//     (INTEGER = INT64, FLOAT = FLOAT64, BOOLEAN = BOOL, RECORD =
//     STRUCT); top-level column names are not compared, nested STRUCT
//     field names are; NULLABLE and REQUIRED are the same, REPEATED is
//     compared;
//   - rows are a multiset; FLOAT64 values match within a relative
//     tolerance of 1e-12, NaN equals NaN, infinities match exactly;
//   - a BigQuery error only requires the driver to fail too (the message
//     is not compared); a driver error where BigQuery answered fails.
//
// Probes that do not match today are listed, one per line with a
// reason, in testdata/dbt_golden/known_divergences.txt. A listed probe
// that starts to match fails the test, so the list only shrinks.
func TestDBTGolden(t *testing.T) {
	probes := loadDBTGoldenProbes(t, "testdata/dbt_golden/probes.jsonl")
	known := loadDBTGoldenKnown(t, "testdata/dbt_golden/known_divergences.txt")
	ids := map[string]bool{}
	for _, p := range probes {
		ids[p.ID] = true
	}
	for id := range known {
		if !ids[id] {
			t.Errorf("known_divergences.txt lists %q, which is not a probe", id)
		}
	}

	db, err := sql.Open("googlesqlite", ":memory:?_test=dbt_golden")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)

	// Group subtests by construct (the id before the first '-') so that
	// `-run TestDBTGolden/lower` selects one construct.
	var groups []string
	byGroup := map[string][]*dbtGoldenProbe{}
	for _, p := range probes {
		g, _, _ := strings.Cut(p.ID, "-")
		if _, ok := byGroup[g]; !ok {
			groups = append(groups, g)
		}
		byGroup[g] = append(byGroup[g], p)
	}
	for _, g := range groups {
		t.Run(g, func(t *testing.T) {
			for _, p := range byGroup[g] {
				t.Run(p.ID, func(t *testing.T) {
					diff := runDBTGoldenProbe(db, p)
					reason, listed := known[p.ID]
					switch {
					case diff != "" && !listed:
						t.Errorf("%s\n  sql: %s\n  %s", p.ID, p.SQL, diff)
					case diff == "" && listed:
						t.Errorf("%s now matches BigQuery; remove it from known_divergences.txt (was: %s)", p.ID, reason)
					}
				})
			}
		})
	}
}

type dbtGoldenField struct {
	Name   string            `json:"name"`
	Type   string            `json:"type"`
	Mode   string            `json:"mode"`
	Fields []*dbtGoldenField `json:"fields"`
}

type dbtGoldenProbe struct {
	ID       string `json:"id"`
	SQL      string `json:"sql"`
	BigQuery struct {
		Error  string            `json:"error"`
		Schema []*dbtGoldenField `json:"schema"`
		Rows   json.RawMessage   `json:"rows"`
	} `json:"bigquery"`
}

func loadDBTGoldenProbes(t *testing.T, path string) []*dbtGoldenProbe {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var out []*dbtGoldenProbe
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<16), 1<<24)
	for sc.Scan() {
		if len(bytes.TrimSpace(sc.Bytes())) == 0 {
			continue
		}
		p := &dbtGoldenProbe{}
		if err := json.Unmarshal(sc.Bytes(), p); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		out = append(out, p)
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// loadDBTGoldenKnown reads "<probe id> <reason>" lines; '#' starts a
// comment line.
func loadDBTGoldenKnown(t *testing.T, path string) map[string]string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for line := range strings.SplitSeq(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		id, reason, _ := strings.Cut(line, " ")
		reason = strings.TrimSpace(reason)
		if reason == "" {
			t.Errorf("known_divergences.txt: %q has no reason", id)
		}
		if _, dup := out[id]; dup {
			t.Errorf("known_divergences.txt: %q listed twice", id)
		}
		out[id] = reason
	}
	return out
}

// runDBTGoldenProbe returns "" when the driver agrees with BigQuery and
// a description of the difference otherwise.
func runDBTGoldenProbe(db *sql.DB, p *dbtGoldenProbe) string {
	schema, rows, err := queryDBTGolden(db, p.SQL)
	bq := p.BigQuery
	if bq.Error != "" {
		if err != nil {
			return ""
		}
		return fmt.Sprintf("BigQuery errors (%s); the driver returned %s", bq.Error, fmtGoldenRows(rows))
	}
	if err != nil {
		return fmt.Sprintf("driver error: %v", err)
	}
	want := normGoldenSchema(bq.Schema)
	got := normGoldenSchema(schema)
	if !sameGoldenTypes(want, got) {
		return fmt.Sprintf("types: bigquery=%s driver=%s", fmtGoldenSchema(want), fmtGoldenSchema(got))
	}
	wantRows, derr := decodeGoldenRows(bq.Rows)
	if derr != nil {
		return fmt.Sprintf("bad recorded rows: %v", derr)
	}
	if !goldenRowsEqual(wantRows, rows) {
		return fmt.Sprintf("rows: bigquery=%s driver=%s", fmtGoldenRows(wantRows), fmtGoldenRows(rows))
	}
	return ""
}

// driverGoldenType is the JSON that Rows.ColumnTypeDatabaseTypeName
// returns.
type driverGoldenType struct {
	Name        string            `json:"name"`
	Kind        int               `json:"kind"`
	ElementType *driverGoldenType `json:"elementType"`
	FieldTypes  []struct {
		Name string            `json:"name"`
		Type *driverGoldenType `json:"type"`
	} `json:"fieldTypes"`
}

// GoogleSQL TypeKind values used by the conversion below.
const (
	goldenKindInt64      = 3
	goldenKindBool       = 6
	goldenKindDouble     = 8
	goldenKindString     = 9
	goldenKindBytes      = 10
	goldenKindDate       = 11
	goldenKindTimestamp  = 20
	goldenKindTime       = 21
	goldenKindDatetime   = 22
	goldenKindArray      = 17
	goldenKindStruct     = 18
	goldenKindNumeric    = 24
	goldenKindBigNumeric = 25
)

func queryDBTGolden(db *sql.DB, query string) ([]*dbtGoldenField, [][]any, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	cts, err := rows.ColumnTypes()
	if err != nil {
		return nil, nil, err
	}
	types := make([]*driverGoldenType, len(cts))
	schema := make([]*dbtGoldenField, len(cts))
	for i, ct := range cts {
		typ := &driverGoldenType{}
		if err := json.Unmarshal([]byte(ct.DatabaseTypeName()), typ); err != nil {
			return nil, nil, fmt.Errorf("column %d type %q: %w", i, ct.DatabaseTypeName(), err)
		}
		types[i] = typ
		schema[i] = goldenFieldFromDriver(ct.Name(), typ)
	}
	var out [][]any
	for rows.Next() {
		vals := make([]any, len(cts))
		ptrs := make([]any, len(cts))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, nil, err
		}
		row := make([]any, len(cts))
		for i, v := range vals {
			nv, err := normDriverGolden(types[i], v)
			if err != nil {
				return nil, nil, fmt.Errorf("column %d: %w", i, err)
			}
			row[i] = nv
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	return schema, out, nil
}

func goldenFieldFromDriver(name string, typ *driverGoldenType) *dbtGoldenField {
	f := &dbtGoldenField{Name: name}
	if typ.Kind == goldenKindArray && typ.ElementType != nil {
		f.Mode = "REPEATED"
		typ = typ.ElementType
	}
	switch typ.Kind {
	case goldenKindDouble:
		f.Type = "FLOAT64"
	case goldenKindStruct:
		f.Type = "STRUCT"
		for _, ft := range typ.FieldTypes {
			f.Fields = append(f.Fields, goldenFieldFromDriver(ft.Name, ft.Type))
		}
	default:
		f.Type = typ.Name
	}
	return f
}

// Normalized values. Both sides are converted to these Go values:
// nil, bool, string, goldenInt, goldenFloat, goldenTagged (NUMERIC,
// DATE, DATETIME, TIMESTAMP, TIME, BYTES with a canonical text),
// goldenStruct and []any.
type (
	goldenInt    string
	goldenFloat  struct{ v float64 }
	goldenTagged struct{ tag, text string }
	goldenStruct []goldenStructField
)

type goldenStructField struct {
	name string
	v    any
}

// pyISO renders t like Python's datetime.isoformat() on a naive value:
// microseconds only when they are non-zero.
func pyISO(t time.Time) string {
	s := t.Format("2006-01-02T15:04:05")
	if us := t.Nanosecond() / 1000; us != 0 {
		s += fmt.Sprintf(".%06d", us)
	}
	return s
}

// goldenNumeric reproduces the harness's Decimal(...).normalize():
// Python's default decimal context rounds to 28 significant digits
// (ROUND_HALF_EVEN), so NUMERIC '99999999999999999999999999999.999999999'
// is recorded as 1E+29. The value is compared as an exact rational.
func goldenNumeric(s string) (goldenTagged, error) {
	s = strings.TrimSpace(s)
	mant, exp := s, 0
	if i := strings.IndexAny(s, "eE"); i >= 0 {
		e, err := strconv.Atoi(s[i+1:])
		if err != nil {
			return goldenTagged{}, fmt.Errorf("bad numeric %q", s)
		}
		mant, exp = s[:i], e
	}
	neg := strings.HasPrefix(mant, "-")
	mant = strings.TrimLeft(mant, "+-")
	if i := strings.IndexByte(mant, '.'); i >= 0 {
		exp -= len(mant) - i - 1
		mant = mant[:i] + mant[i+1:]
	}
	coef, ok := new(big.Int).SetString(mant, 10)
	if !ok {
		return goldenTagged{}, fmt.Errorf("bad numeric %q", s)
	}
	const prec = 28
	if extra := len(coef.String()) - prec; coef.Sign() != 0 && extra > 0 {
		div := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(extra)), nil)
		q, r := new(big.Int).QuoRem(coef, div, new(big.Int))
		switch c := new(big.Int).Lsh(r, 1).Cmp(div); {
		case c > 0, c == 0 && q.Bit(0) == 1:
			q.Add(q, big.NewInt(1))
		}
		coef, exp = q, exp+extra
	}
	if neg {
		coef.Neg(coef)
	}
	r := new(big.Rat).SetInt(coef)
	pow := new(big.Rat).SetInt(new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(abs(exp))), nil))
	if exp >= 0 {
		r.Mul(r, pow)
	} else {
		r.Quo(r, pow)
	}
	return goldenTagged{"numeric", r.RatString()}, nil
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

func normDriverGolden(typ *driverGoldenType, v any) (any, error) {
	if typ.Kind == goldenKindArray {
		// The BigQuery API returns a NULL array as [].
		if v == nil {
			return []any{}, nil
		}
		elems, ok := v.([]any)
		if !ok {
			return nil, fmt.Errorf("ARRAY value %T", v)
		}
		out := make([]any, len(elems))
		for i, e := range elems {
			ne, err := normDriverGolden(typ.ElementType, e)
			if err != nil {
				return nil, err
			}
			out[i] = ne
		}
		return out, nil
	}
	if v == nil {
		return nil, nil
	}
	str := func() (string, error) {
		switch x := v.(type) {
		case string:
			return x, nil
		case []byte:
			return string(x), nil
		}
		return "", fmt.Errorf("%s value %T", typ.Name, v)
	}
	switch typ.Kind {
	case goldenKindStruct:
		fields, ok := v.([]any)
		if !ok || len(fields) != len(typ.FieldTypes) {
			return nil, fmt.Errorf("STRUCT value %T %v", v, v)
		}
		out := make(goldenStruct, len(fields))
		for i, fv := range fields {
			nv, err := normDriverGolden(typ.FieldTypes[i].Type, fv)
			if err != nil {
				return nil, err
			}
			out[i] = goldenStructField{typ.FieldTypes[i].Name, nv}
		}
		return out, nil
	case goldenKindInt64:
		switch x := v.(type) {
		case int64:
			return goldenInt(fmt.Sprint(x)), nil
		case int:
			return goldenInt(fmt.Sprint(x)), nil
		}
		return nil, fmt.Errorf("INT64 value %T", v)
	case goldenKindDouble:
		x, ok := v.(float64)
		if !ok {
			return nil, fmt.Errorf("FLOAT64 value %T", v)
		}
		return goldenFloat{x}, nil
	case goldenKindBool:
		x, ok := v.(bool)
		if !ok {
			return nil, fmt.Errorf("BOOL value %T", v)
		}
		return x, nil
	case goldenKindString:
		return str()
	case goldenKindNumeric, goldenKindBigNumeric:
		s, err := str()
		if err != nil {
			return nil, err
		}
		return goldenNumeric(s)
	case goldenKindDate:
		s, err := str()
		return goldenTagged{"date", s}, err
	case goldenKindTime:
		s, err := str()
		if err != nil {
			return nil, err
		}
		tm, err := time.Parse("15:04:05.999999999", s)
		if err != nil {
			return nil, err
		}
		return goldenTagged{"time", pyISO(tm)[11:]}, nil
	case goldenKindDatetime:
		s, err := str()
		if err != nil {
			return nil, err
		}
		tm, err := time.Parse("2006-01-02T15:04:05.999999999", strings.Replace(s, " ", "T", 1))
		if err != nil {
			return nil, err
		}
		return goldenTagged{"datetime", pyISO(tm)}, nil
	case goldenKindTimestamp:
		s, err := str()
		if err != nil {
			return nil, err
		}
		tm, err := time.Parse("2006-01-02 15:04:05.999999999-07", s)
		if err != nil {
			return nil, err
		}
		return goldenTagged{"timestamp", pyISO(tm.UTC())}, nil
	case goldenKindBytes:
		s, err := str()
		return goldenTagged{"bytes", s}, err
	}
	// JSON, INTERVAL and other types: the harness saw them as opaque
	// values; none of the recorded answers has such a column.
	s, err := str()
	return goldenTagged{"other:" + typ.Name, s}, err
}

func decodeGoldenRows(raw json.RawMessage) ([][]any, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var rows [][]any
	if err := dec.Decode(&rows); err != nil {
		return nil, err
	}
	for _, r := range rows {
		for i, v := range r {
			nv, err := normRecordedGolden(v)
			if err != nil {
				return nil, err
			}
			r[i] = nv
		}
	}
	return rows, nil
}

// normRecordedGolden converts a value in the harness's normalized JSON
// form (compare.norm_value) to the Go form above.
func normRecordedGolden(v any) (any, error) {
	switch x := v.(type) {
	case nil, bool, string:
		return x, nil
	case json.Number:
		return goldenInt(x.String()), nil
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			ne, err := normRecordedGolden(e)
			if err != nil {
				return nil, err
			}
			out[i] = ne
		}
		return out, nil
	case map[string]any:
		if len(x) != 1 {
			return nil, fmt.Errorf("value %v", x)
		}
		for tag, inner := range x {
			switch tag {
			case "float":
				switch f := inner.(type) {
				case json.Number:
					fv, err := f.Float64()
					return goldenFloat{fv}, err
				case string:
					switch f {
					case "nan":
						return goldenFloat{math.NaN()}, nil
					case "inf":
						return goldenFloat{math.Inf(1)}, nil
					case "-inf":
						return goldenFloat{math.Inf(-1)}, nil
					}
				}
			case "numeric":
				if s, ok := inner.(string); ok {
					return goldenNumeric(s)
				}
			case "date", "datetime", "timestamp", "time", "bytes":
				if s, ok := inner.(string); ok {
					return goldenTagged{tag, s}, nil
				}
			case "struct":
				pairs, ok := inner.([]any)
				if !ok {
					break
				}
				out := make(goldenStruct, len(pairs))
				for i, pr := range pairs {
					kv, ok := pr.([]any)
					if !ok || len(kv) != 2 {
						return nil, fmt.Errorf("struct field %v", pr)
					}
					name, _ := kv[0].(string)
					nv, err := normRecordedGolden(kv[1])
					if err != nil {
						return nil, err
					}
					out[i] = goldenStructField{name, nv}
				}
				return out, nil
			case "other":
				return goldenTagged{"other", fmt.Sprint(inner)}, nil
			}
			return nil, fmt.Errorf("value %v", x)
		}
	}
	return nil, fmt.Errorf("value %T %v", v, v)
}

type goldenSchemaField struct {
	name, typ string
	repeated  bool
	nested    []goldenSchemaField
}

var goldenTypeSynonyms = map[string]string{"INTEGER": "INT64", "FLOAT": "FLOAT64", "BOOLEAN": "BOOL", "RECORD": "STRUCT"}

func normGoldenSchema(fields []*dbtGoldenField) []goldenSchemaField {
	out := make([]goldenSchemaField, 0, len(fields))
	for _, f := range fields {
		typ := strings.ToUpper(f.Type)
		if s, ok := goldenTypeSynonyms[typ]; ok {
			typ = s
		}
		out = append(out, goldenSchemaField{f.Name, typ, strings.EqualFold(f.Mode, "REPEATED"), normGoldenSchema(f.Fields)})
	}
	return out
}

// sameGoldenTypes compares types; top-level names are ignored (an
// unaliased column is f0_ on BigQuery and $col1 on the driver), nested
// names are compared.
func sameGoldenTypes(a, b []goldenSchemaField) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].typ != b[i].typ || a[i].repeated != b[i].repeated || !sameGoldenNested(a[i].nested, b[i].nested) {
			return false
		}
	}
	return true
}

func sameGoldenNested(a, b []goldenSchemaField) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].name != b[i].name {
			return false
		}
	}
	return sameGoldenTypes(a, b)
}

func fmtGoldenSchema(s []goldenSchemaField) string {
	parts := make([]string, len(s))
	for i, f := range s {
		t := f.typ
		if len(f.nested) > 0 {
			t += "<" + fmtGoldenSchema(f.nested) + ">"
		}
		if f.repeated {
			t = "ARRAY<" + t + ">"
		}
		parts[i] = f.name + ":" + t
	}
	return strings.Join(parts, ", ")
}

func goldenValuesEqual(a, b any) bool {
	switch x := a.(type) {
	case nil:
		return b == nil
	case goldenFloat:
		y, ok := b.(goldenFloat)
		if !ok {
			return false
		}
		if math.IsNaN(x.v) || math.IsNaN(y.v) {
			return math.IsNaN(x.v) && math.IsNaN(y.v)
		}
		if math.IsInf(x.v, 0) || math.IsInf(y.v, 0) {
			return x.v == y.v
		}
		// math.isclose(rel_tol=1e-12, abs_tol=1e-300)
		return math.Abs(x.v-y.v) <= math.Max(1e-12*math.Max(math.Abs(x.v), math.Abs(y.v)), 1e-300)
	case []any:
		y, ok := b.([]any)
		if !ok || len(x) != len(y) {
			return false
		}
		for i := range x {
			if !goldenValuesEqual(x[i], y[i]) {
				return false
			}
		}
		return true
	case goldenStruct:
		y, ok := b.(goldenStruct)
		if !ok || len(x) != len(y) {
			return false
		}
		for i := range x {
			if x[i].name != y[i].name || !goldenValuesEqual(x[i].v, y[i].v) {
				return false
			}
		}
		return true
	}
	return a == b
}

func goldenRowEqual(a, b []any) bool { return goldenValuesEqual(a, b) }

// goldenRowsEqual is multiset equality: sort both sides by canonical
// text and compare pairwise, then fall back to a greedy match (two
// nearly equal floats may sort differently).
func goldenRowsEqual(a, b [][]any) bool {
	if len(a) != len(b) {
		return false
	}
	sa, sb := sortedGoldenRows(a), sortedGoldenRows(b)
	pairwise := true
	for i := range sa {
		if !goldenRowEqual(sa[i], sb[i]) {
			pairwise = false
			break
		}
	}
	if pairwise {
		return true
	}
	pool := append([][]any(nil), sb...)
	for _, x := range sa {
		found := false
		for i, y := range pool {
			if goldenRowEqual(x, y) {
				pool = append(pool[:i], pool[i+1:]...)
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func sortedGoldenRows(rows [][]any) [][]any {
	out := append([][]any(nil), rows...)
	keys := make(map[int]string, len(out))
	idx := make([]int, len(out))
	for i := range out {
		idx[i] = i
		keys[i] = goldenCanon(out[i])
	}
	sort.SliceStable(idx, func(i, j int) bool { return keys[idx[i]] < keys[idx[j]] })
	res := make([][]any, len(out))
	for i, k := range idx {
		res[i] = out[k]
	}
	return res
}

func goldenCanon(v any) string {
	switch x := v.(type) {
	case nil:
		return "null"
	case bool:
		return fmt.Sprint(x)
	case string:
		b, _ := json.Marshal(x)
		return string(b)
	case goldenInt:
		return string(x)
	case goldenFloat:
		return fmt.Sprintf("{float:%v}", x.v)
	case goldenTagged:
		return fmt.Sprintf("{%s:%s}", x.tag, x.text)
	case goldenStruct:
		parts := make([]string, len(x))
		for i, f := range x {
			parts[i] = f.name + ":" + goldenCanon(f.v)
		}
		return "{" + strings.Join(parts, ", ") + "}"
	case []any:
		parts := make([]string, len(x))
		for i, e := range x {
			parts[i] = goldenCanon(e)
		}
		return "[" + strings.Join(parts, ", ") + "]"
	}
	return fmt.Sprintf("%#v", v)
}

func fmtGoldenRows(rows [][]any) string {
	parts := make([]string, len(rows))
	for i, r := range sortedGoldenRows(rows) {
		parts[i] = goldenCanon(r)
	}
	s := "[" + strings.Join(parts, ", ") + "]"
	if len(s) > 400 {
		s = s[:400] + "..."
	}
	return s
}
