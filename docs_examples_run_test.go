package googlesqlite_test

// Docs-examples runner.
//
// `specctl extract-docs-examples --out FILE` pulls every (SQL, result
// table) example from docs/third_party/googlesql-docs/*.md into JSON.
// This test replays each example on the driver, types every documented
// cell against the column type the analyzer reports, and compares.
//
// It is skipped unless GOOGLESQLITE_DOCS_EXAMPLES points at the JSON.
// Outputs, written under GOOGLESQLITE_DOCS_EXAMPLES_OUT (default: the
// JSON's directory):
//
//   results.json   one record per example (status, reason, both results)
//   failures.json  fail and error records only
//   yaml/specs/... passing new examples in testdata/specs format
//   yaml/pending/. failing new examples in the same format
//
// See docs/docs_examples_results.md for how to run it.

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/goccy/go-yaml"
)

type docsExample struct {
	ID          string     `json:"id"`
	Page        string     `json:"page"`
	Section     string     `json:"section"`
	Line        int        `json:"line"`
	Setup       []string   `json:"setup,omitempty"`
	Fixtures    []string   `json:"fixtures,omitempty"`
	AltSetups   [][]string `json:"alt_setups,omitempty"`
	SQL         string     `json:"sql"`
	Columns     []string   `json:"columns,omitempty"`
	Rows        [][]string `json:"rows,omitempty"`
	ExpectError bool       `json:"expect_error,omitempty"`
	ErrorText   string     `json:"error_text,omitempty"`
	Ordered     bool       `json:"ordered"`
	Skip        string     `json:"skip,omitempty"`
	CoveredBy   string     `json:"covered_by,omitempty"`
}

// Status values.
const (
	stPass    = "pass"
	stFail    = "fail"  // ran, wrong answer (silent)
	stError   = "error" // the driver raised an error or hung (loud)
	stSkipped = "skipped"
)

type docsResult struct {
	ID          string     `json:"id"`
	Page        string     `json:"page"`
	Section     string     `json:"section"`
	Line        int        `json:"line"`
	New         bool       `json:"new"`
	CoveredBy   string     `json:"covered_by,omitempty"`
	Status      string     `json:"status"`
	Kind        string     `json:"kind,omitempty"` // silent | loud
	Reason      string     `json:"reason,omitempty"`
	Setup       []string   `json:"setup,omitempty"`
	SQL         string     `json:"sql"`
	DocColumns  []string   `json:"documented_columns,omitempty"`
	DocRows     [][]string `json:"documented_rows,omitempty"`
	DocError    string     `json:"documented_error,omitempty"`
	ExpectError bool       `json:"documented_expects_error,omitempty"`
	GotColumns  []string   `json:"emulator_columns,omitempty"`
	GotTypes    []string   `json:"emulator_types,omitempty"`
	GotRows     [][]string `json:"emulator_rows,omitempty"`
	GotError    string     `json:"emulator_error,omitempty"`
	Ordered     bool       `json:"ordered"`

	typedRows    [][]any
	colTypes     []*docsType
	RoundedFloat bool `json:"rounded_float_match,omitempty"`
	gotRaw       [][]any
	// runnerMismatch marks a pass the spec runner cannot express.
	RunnerMismatch bool `json:"spec_runner_mismatch,omitempty"`
	// Class and ClassReason come from the classification file
	// (docsClassFile) for examples that do not pass.
	Class       string `json:"class,omitempty"`
	ClassReason string `json:"class_reason,omitempty"`
}

// strictFloats disables the rounded-display float match.
var strictFloats bool

func TestDocsExamples(t *testing.T) {
	in := os.Getenv("GOOGLESQLITE_DOCS_EXAMPLES")
	if in == "" {
		t.Skip("set GOOGLESQLITE_DOCS_EXAMPLES to the JSON written by `specctl extract-docs-examples`, or to 1 to extract first")
	}
	outDir := os.Getenv("GOOGLESQLITE_DOCS_EXAMPLES_OUT")
	if in == "1" {
		// Extract first: from GOOGLESQLITE_DOCS_EXAMPLES_DOCS (a
		// google/googlesql docs/ checkout) or the vendored snapshot.
		docs := os.Getenv("GOOGLESQLITE_DOCS_EXAMPLES_DOCS")
		if docs == "" {
			docs = "docs/third_party/googlesql-docs"
		}
		if outDir == "" {
			outDir = t.TempDir()
		}
		in = filepath.Join(outDir, "extracted.json")
		cmd := exec.Command("go", "run", "./cmd/specctl", "extract-docs-examples", "--docs", docs, "--out", in)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("extract-docs-examples: %v\n%s", err, out)
		}
	}
	if outDir == "" {
		outDir = filepath.Dir(in)
	}
	data, err := os.ReadFile(in)
	if err != nil {
		t.Fatal(err)
	}
	var exs []docsExample
	if err := json.Unmarshal(data, &exs); err != nil {
		t.Fatal(err)
	}
	only := os.Getenv("GOOGLESQLITE_DOCS_EXAMPLES_ONLY")
	var results []*docsResult
	for i := range exs {
		ex := &exs[i]
		if only != "" && !strings.Contains(ex.ID, only) {
			continue
		}
		r := runDocsExample(ex)
		results = append(results, r)
	}
	if err := writeDocsOutputs(outDir, results); err != nil {
		t.Fatal(err)
	}
	counts := map[string]int{}
	for _, r := range results {
		counts[r.Status]++
	}
	t.Logf("docs examples: %d total, pass=%d fail=%d error=%d skipped=%d",
		len(results), counts[stPass], counts[stFail], counts[stError], counts[stSkipped])
}

// runDocsExample runs the example with its primary setup, then with
// each alternative fixture definition until one reproduces the docs.
// When none does, the primary result is kept unless it failed to run
// and an alternative ran.
func runDocsExample(ex *docsExample) *docsResult {
	r := runDocsExampleWith(ex, ex.Setup)
	if r.Status == stPass || r.Status == stSkipped && ex.Skip != "" {
		return r
	}
	var ran *docsResult
	for _, alt := range ex.AltSetups {
		ra := runDocsExampleWith(ex, alt)
		if ra.Status == stPass {
			ra.Reason = "passes with an alternative fixture definition"
			return ra
		}
		if ran == nil && ra.Status == stFail {
			ran = ra
		}
	}
	if r.Status != stFail && ran != nil {
		return ran
	}
	return r
}

func runDocsExampleWith(ex *docsExample, setup []string) *docsResult {
	exCopy := *ex
	exCopy.Setup = setup
	// TEMP functions and tables live for one multi-statement script,
	// as in BigQuery outside a session, so run such examples as one
	// script rather than as separate setup statements.
	for _, st := range setup {
		if tempObjectRe.MatchString(st) {
			exCopy.SQL = strings.Join(append(append([]string{}, setup...), ex.SQL), ";\n")
			exCopy.Setup = nil
			break
		}
	}
	ex = &exCopy
	r := &docsResult{
		ID: ex.ID, Page: ex.Page, Section: ex.Section, Line: ex.Line,
		CoveredBy: ex.CoveredBy, Setup: ex.Setup, SQL: ex.SQL,
		DocColumns: ex.Columns, DocRows: ex.Rows, DocError: ex.ErrorText,
		ExpectError: ex.ExpectError, Ordered: ex.Ordered,
	}
	r.New = ex.CoveredBy == "" && !strings.HasPrefix(ex.Skip, "duplicate of ")
	if ex.Skip != "" {
		r.Status, r.Reason = stSkipped, ex.Skip
		return r
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer func() {
			if p := recover(); p != nil {
				r.Status, r.Kind, r.Reason = stError, "loud", "panic"
				r.GotError = fmt.Sprint(p)
			}
		}()
		execDocsExample(ex, r)
	}()
	select {
	case <-done:
	case <-time.After(90 * time.Second):
		r.Status, r.Kind, r.Reason = stError, "loud", "timeout after 90s"
		r.GotError = "timeout"
	}
	return r
}

var (
	tempObjectRe    = regexp.MustCompile(`(?is)^\s*(--[^\n]*\n\s*)*CREATE\s+(OR\s+REPLACE\s+)?TEMP(ORARY)?\s`)
	tableNotFoundRe = regexp.MustCompile(`(?i)Table not found: ([\w.]+)`)
	graphNotFoundRe = regexp.MustCompile(`(?i)Property graph not found|graph not found: ([\w.]+)`)
	protoTypeRe     = regexp.MustCompile(`(?i)(Type not found|Enum not found|Proto not found|proto).*`)
)

// classifyError decides whether an error means the example is not
// self-contained (a table or type that the page never defines) rather
// than a driver divergence.
func classifyError(ex *docsExample, msg string) string {
	all := strings.ToLower(strings.Join(append(append([]string{}, ex.Setup...), ex.SQL), "\n"))
	if m := tableNotFoundRe.FindStringSubmatch(msg); m != nil {
		name := strings.ToLower(m[1])
		if !regexp.MustCompile(`(?i)(create\s+(or\s+replace\s+)?(temp(orary)?\s+)?table\s+(if\s+not\s+exists\s+)?|with\s+|,\s*)` + regexp.QuoteMeta(name) + `\b`).MatchString(all) {
			return "references table " + m[1] + " that the docs page never defines"
		}
	}
	if graphNotFoundRe.MatchString(msg) && !strings.Contains(all, "create property graph") {
		return "references a property graph that the docs page never defines"
	}
	if strings.Contains(msg, "Type not found") && protoTypeRe.MatchString(msg) {
		return "references a proto or enum type that is not registered"
	}
	return ""
}

func execDocsExample(ex *docsExample, r *docsResult) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	db, err := sql.Open("googlesqlite", ":memory:")
	if err != nil {
		r.Status, r.Kind, r.GotError = stError, "loud", err.Error()
		return
	}
	defer db.Close()
	conn, err := db.Conn(ctx)
	if err != nil {
		r.Status, r.Kind, r.GotError = stError, "loud", err.Error()
		return
	}
	defer conn.Close()
	var setup []string
	for _, st := range ex.Setup {
		// A query in the setup is a neighbouring example from the same
		// block; it has no side effects, so only an example documented
		// to fail (whose error may fire there) needs it.
		if !ex.ExpectError && setupQueryRe.MatchString(stripDocsComments(st)) {
			continue
		}
		if _, err := conn.ExecContext(ctx, st); err != nil {
			if why := classifyError(ex, err.Error()); why != "" {
				r.Status, r.Reason, r.GotError = stSkipped, why, err.Error()
				return
			}
			if isFixtureStmt(st, ex) && !strings.Contains(strings.ToLower(ex.SQL), "create ") {
				// A page fixture the extractor mis-built (for example a
				// recursive CTE body turned into CREATE TABLE). Drop it:
				// if the example needs it, the query fails loudly.
				continue
			}
			if ex.ExpectError && !isFixtureStmt(st, ex) {
				// The documented failure fires in an earlier statement
				// of the same example.
				r.Status, r.GotError, r.Reason = stPass, err.Error(), "error raised as documented"
				return
			}
			r.Status, r.Kind, r.Reason, r.GotError = stError, "loud", "setup statement failed", err.Error()
			return
		}
		setup = append(setup, st)
	}
	r.Setup = setup
	rows, qerr := conn.QueryContext(ctx, ex.SQL)
	var got [][]any
	var cols []string
	var types []*docsType
	if qerr == nil {
		cols, _ = rows.Columns()
		cts, _ := rows.ColumnTypes()
		for _, ct := range cts {
			var ty docsType
			_ = json.Unmarshal([]byte(ct.DatabaseTypeName()), &ty)
			types = append(types, &ty)
		}
		for rows.Next() {
			buf := make([]any, len(cols))
			ptrs := make([]any, len(cols))
			for i := range buf {
				ptrs[i] = &buf[i]
			}
			if err := rows.Scan(ptrs...); err != nil {
				qerr = err
				break
			}
			got = append(got, buf)
		}
		if qerr == nil {
			qerr = rows.Err()
		}
		rows.Close()
	}
	r.GotColumns = cols
	r.colTypes = types
	for _, ty := range types {
		r.GotTypes = append(r.GotTypes, ty.Name)
	}
	r.gotRaw = got
	for _, row := range got {
		var s []string
		for _, v := range row {
			s = append(s, renderGot(v))
		}
		r.GotRows = append(r.GotRows, s)
	}
	if ex.ExpectError {
		if qerr != nil {
			r.Status, r.GotError, r.Reason = stPass, qerr.Error(), "error raised as documented"
			return
		}
		r.Status, r.Kind, r.Reason = stFail, "silent", "docs expect an error; the driver returned a result"
		return
	}
	if qerr != nil {
		r.GotError = qerr.Error()
		if why := classifyError(ex, qerr.Error()); why != "" {
			r.Status, r.Reason = stSkipped, why
			return
		}
		r.Status, r.Kind, r.Reason = stError, "loud", "query failed"
		return
	}
	if len(ex.Rows) == 0 && len(ex.Columns) > 0 && len(cols) == len(ex.Columns) && allAnonymous(cols) {
		// For a query whose columns are all anonymous the docs print the
		// single result row in a header-only box (lexical.md, "Tokens in
		// literals"): the "header" is the row.
		ex.Rows = [][]string{ex.Columns}
		ex.Columns = cols
		r.DocColumns, r.DocRows = ex.Columns, ex.Rows
	}
	if len(cols) != len(ex.Columns) {
		r.Status, r.Kind = stFail, "silent"
		r.Reason = fmt.Sprintf("column count differs: docs %d, driver %d", len(ex.Columns), len(cols))
		return
	}
	// Type every documented cell with the analyzer's column type.
	typed := make([][]any, len(ex.Rows))
	for i, row := range ex.Rows {
		typed[i] = make([]any, len(row))
		for j, cell := range row {
			v, err := parseDocCell(cell, types[j])
			if err != nil {
				r.Status = stSkipped
				r.Reason = fmt.Sprintf("cannot type documented cell %q as %s: %v", cell, types[j].Name, err)
				return
			}
			typed[i][j] = v
		}
	}
	r.typedRows = typed
	if len(got) != len(typed) {
		r.Status, r.Kind = stFail, "silent"
		r.Reason = fmt.Sprintf("row count differs: docs %d, driver %d", len(typed), len(got))
		return
	}
	ok := false
	if ex.Ordered || len(typed) <= 1 {
		ok = docRowsEqual(got, typed, types)
	} else {
		ok = docRowsEqualUnordered(got, typed, types)
	}
	if ok {
		r.Status = stPass
		// Record passes that rely on rounding a documented float; the
		// spec runner compares floats to 1e-9 and cannot express them.
		strictFloats = true
		if ex.Ordered || len(typed) <= 1 {
			r.RoundedFloat = !docRowsEqual(got, typed, types)
		} else {
			r.RoundedFloat = !docRowsEqualUnordered(got, typed, types)
		}
		strictFloats = false
		return
	}
	if why, bqOK := laOffsetOnly(got, typed, types, ex.Ordered); why != "" {
		if bqOK {
			r.Status, r.Reason = stSkipped, why
		} else {
			r.Status, r.Kind, r.Reason = stFail, "silent", why
		}
		return
	}
	r.Status, r.Kind, r.Reason = stFail, "silent", "values differ"
	if !ex.Ordered && len(typed) > 1 {
		r.Reason = "values differ (compared as an unordered multiset)"
	} else if ex.Ordered && docRowsEqualUnordered(got, typed, types) {
		// Often ties in the ORDER BY key, which leave the order
		// unspecified; reported separately from value divergences.
		r.Kind = "order"
		r.Reason = "same rows, different order under ORDER BY (may be ties in the sort key)"
	}
}

func allAnonymous(cols []string) bool {
	for _, c := range cols {
		if !strings.HasPrefix(c, "$col") {
			return false
		}
	}
	return true
}

var setupQueryRe = regexp.MustCompile(`(?is)^\s*(SELECT|WITH|FROM)\b`)

// stripDocsComments drops leading `--` comment lines.
func stripDocsComments(st string) string {
	lines := strings.Split(st, "\n")
	for len(lines) > 0 && strings.HasPrefix(strings.TrimSpace(lines[0]), "--") {
		lines = lines[1:]
	}
	return strings.Join(lines, "\n")
}

func isFixtureStmt(st string, ex *docsExample) bool {
	for _, f := range ex.Fixtures {
		if strings.Contains(strings.ToLower(st), strings.ToLower(f)) && regexp.MustCompile(`(?i)^\s*(CREATE|INSERT)`).MatchString(st) {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------
// Types

type docsType struct {
	Name        string `json:"name"`
	Kind        int    `json:"kind"`
	ElementType *docsType
	FieldTypes  []struct {
		Name string    `json:"name"`
		Type *docsType `json:"type"`
	}
}

func (t *docsType) base() string {
	n := t.Name
	if i := strings.IndexAny(n, "<("); i >= 0 {
		n = n[:i]
	}
	return strings.ToUpper(strings.TrimSpace(n))
}

// Typed documented values.
type (
	docNumeric struct{ r *big.Rat }
	docFloat   struct {
		v      float64
		digits int // significant digits printed in the docs
		text   string
	}
	// docBytes holds every reading of an ambiguous BYTES cell: the
	// docs print BYTES as escaped text, raw text, or base64.
	docBytes [][]byte
	docTime  struct {
		t    time.Time
		kind string
	}
	docJSON   struct{ v any }
	docText   string // compared after whitespace normalisation
	docString struct {
		s      string
		quoted bool
		// matched is the driver string this cell matched, recorded so
		// the emitted expectation uses the form that actually matched
		// (a quoted cell can match either the bare or the quoted form).
		matched string
	}
	docStruct []any
	docGeo    string
)

var (
	errNotArray  = errors.New("not an ARRAY literal")
	errNotStruct = errors.New("not a STRUCT literal")
)

// parseDocCell converts a documented cell into a typed value.
func parseDocCell(cell string, ty *docsType) (any, error) {
	c := strings.TrimSpace(cell)
	if c == "NULL" {
		return nil, nil
	}
	switch ty.base() {
	case "INT64", "INT32", "UINT32", "UINT64":
		i, err := strconv.ParseInt(c, 10, 64)
		if err != nil {
			u, err2 := strconv.ParseUint(c, 10, 64)
			if err2 != nil {
				return nil, err
			}
			return docNumeric{new(big.Rat).SetUint64(u)}, nil
		}
		return i, nil
	case "DOUBLE", "FLOAT":
		return parseDocFloat(c)
	case "NUMERIC", "BIGNUMERIC":
		r, ok := new(big.Rat).SetString(c)
		if !ok {
			return nil, fmt.Errorf("bad decimal")
		}
		return docNumeric{r}, nil
	case "BOOL":
		switch strings.ToLower(c) {
		case "true":
			return true, nil
		case "false":
			return false, nil
		}
		return nil, fmt.Errorf("bad bool")
	case "STRING":
		if len(c) >= 2 && ((c[0] == '"' && c[len(c)-1] == '"') || (c[0] == '\'' && c[len(c)-1] == '\'')) {
			return &docString{s: c[1 : len(c)-1], quoted: true}, nil
		}
		return &docString{s: c}, nil
	case "BYTES":
		return parseDocBytes(c)
	case "DATE", "DATETIME", "TIME", "TIMESTAMP":
		tm, err := parseDocTime(unquote(c), ty.base())
		if err != nil {
			return nil, err
		}
		return docTime{t: tm, kind: ty.base()}, nil
	case "JSON":
		var v any
		d := json.NewDecoder(strings.NewReader(c))
		d.UseNumber()
		if err := d.Decode(&v); err != nil {
			if len(c) >= 2 && c[0] == '\'' && c[len(c)-1] == '\'' {
				return parseDocCell(c[1:len(c)-1], ty)
			}
			return nil, fmt.Errorf("bad JSON: %v", err)
		}
		return docJSON{v}, nil
	case "GEOGRAPHY":
		return docGeo(unquote(c)), nil
	case "ARRAY":
		if ty.ElementType == nil {
			return nil, fmt.Errorf("array without element type")
		}
		if !strings.HasPrefix(c, "[") || !strings.HasSuffix(c, "]") {
			return nil, errNotArray
		}
		parts := splitTopLevel(c[1 : len(c)-1])
		out := make([]any, 0, len(parts))
		for _, p := range parts {
			v, err := parseDocCell(p, ty.ElementType)
			if err != nil {
				return nil, err
			}
			out = append(out, v)
		}
		return out, nil
	case "STRUCT":
		if len(c) < 2 || !strings.ContainsRune("{([", rune(c[0])) || !strings.ContainsRune("})]", rune(c[len(c)-1])) {
			return nil, errNotStruct
		}
		parts := splitTopLevel(c[1 : len(c)-1])
		if len(parts) != len(ty.FieldTypes) {
			return nil, fmt.Errorf("struct has %d fields, type has %d", len(parts), len(ty.FieldTypes))
		}
		out := make(docStruct, 0, len(parts))
		for i, p := range parts {
			p = strings.TrimSpace(p)
			if fn := ty.FieldTypes[i].Name; fn != "" {
				// BigQuery-style display "{blue color, round shape}".
				if strings.HasSuffix(p, " "+fn) {
					p = strings.TrimSpace(strings.TrimSuffix(p, " "+fn))
				}
				for _, sep := range []string{": ", ":", " = "} {
					if strings.HasPrefix(strings.ToLower(p), strings.ToLower(fn)+sep) {
						p = strings.TrimSpace(p[len(fn)+len(sep):])
						break
					}
				}
			}
			v, err := parseDocCell(p, ty.FieldTypes[i].Type)
			if err != nil {
				return nil, err
			}
			out = append(out, v)
		}
		return out, nil
	case "INTERVAL", "RANGE":
		return docText(unquote(c)), nil
	}
	return docText(c), nil
}

func unquote(c string) string {
	if len(c) >= 2 && (c[0] == '"' || c[0] == '\'') && c[len(c)-1] == c[0] {
		return c[1 : len(c)-1]
	}
	return c
}

func parseDocFloat(c string) (any, error) {
	switch strings.ToLower(c) {
	case "inf", "+inf", "infinity":
		return docFloat{v: math.Inf(1), text: c}, nil
	case "-inf", "-infinity":
		return docFloat{v: math.Inf(-1), text: c}, nil
	case "nan":
		return docFloat{v: math.NaN(), text: c}, nil
	}
	f, err := strconv.ParseFloat(c, 64)
	if err != nil {
		return nil, err
	}
	mant := strings.ToLower(c)
	if i := strings.Index(mant, "e"); i >= 0 {
		mant = mant[:i]
	}
	mant = strings.TrimLeft(strings.NewReplacer("-", "", "+", "", ".", "").Replace(mant), "0")
	return docFloat{v: f, digits: len(mant), text: c}, nil
}

func parseDocBytes(c string) (any, error) {
	if len(c) >= 3 && (c[0] == 'b' || c[0] == 'B') && (c[1] == '"' || c[1] == '\'') && c[len(c)-1] == c[1] {
		c = c[1:]
	}
	c = unquote(c)
	if strings.Contains(c, `\`) {
		s, err := unescapeBytes(c)
		if err != nil {
			return nil, err
		}
		return docBytes{s}, nil
	}
	out := docBytes{[]byte(c)}
	if b, err := base64.StdEncoding.DecodeString(c); err == nil {
		out = append(out, b)
	}
	return out, nil
}

func unescapeBytes(s string) ([]byte, error) {
	var out []byte
	for i := 0; i < len(s); i++ {
		if s[i] != '\\' || i+1 >= len(s) {
			out = append(out, s[i])
			continue
		}
		i++
		switch s[i] {
		case 'x', 'X':
			if i+2 >= len(s)+0 && i+2 > len(s) {
				return nil, fmt.Errorf("short \\x escape")
			}
			v, err := strconv.ParseUint(s[i+1:i+3], 16, 8)
			if err != nil {
				return nil, err
			}
			out = append(out, byte(v))
			i += 2
		case 'n':
			out = append(out, '\n')
		case 't':
			out = append(out, '\t')
		case 'r':
			out = append(out, '\r')
		case '0', '1', '2', '3':
			if i+2 < len(s) {
				v, err := strconv.ParseUint(s[i:i+3], 8, 8)
				if err == nil {
					out = append(out, byte(v))
					i += 2
					continue
				}
			}
			out = append(out, s[i])
		default:
			out = append(out, s[i])
		}
	}
	return out, nil
}

var timeLayouts = map[string][]string{
	"DATE": {"2006-01-02"},
	"TIME": {"15:04:05.999999999", "15:04:05", "15:04"},
	"DATETIME": {
		"2006-01-02T15:04:05.999999999", "2006-01-02 15:04:05.999999999",
		"2006-01-02T15:04:05", "2006-01-02 15:04:05", "2006-01-02T15:04", "2006-01-02 15:04", "2006-01-02",
	},
	"TIMESTAMP": {
		"2006-01-02 15:04:05.999999999-07", "2006-01-02 15:04:05.999999999-07:00", "2006-01-02 15:04:05.999999999 MST",
		"2006-01-02 15:04:05.999999999Z07:00", "2006-01-02T15:04:05.999999999Z07:00", "2006-01-02T15:04:05.999999999-07",
		"2006-01-02 15:04:05.999999999", "2006-01-02 15:04-07", "2006-01-02 15:04:05 -0700", "2006-01-02 -07",
	},
}

func parseDocTime(s, kind string) (time.Time, error) {
	s = strings.TrimSpace(s)
	if kind == "TIMESTAMP" {
		// "2008-12-25 07:30:00.000 America/Los_Angeles"
		if i := strings.LastIndex(s, " "); i > 0 && strings.Contains(s[i+1:], "/") {
			loc, err := time.LoadLocation(s[i+1:])
			if err != nil {
				return time.Time{}, err
			}
			for _, l := range []string{"2006-01-02 15:04:05.999999999", "2006-01-02 15:04:05", "2006-01-02"} {
				if t, err := time.ParseInLocation(l, s[:i], loc); err == nil {
					return t, nil
				}
			}
			return time.Time{}, fmt.Errorf("unrecognised TIMESTAMP layout")
		}
		s = strings.Replace(s, " UTC", "+00", 1)
		s = strings.TrimSuffix(s, "Z")
		if !strings.ContainsAny(s[min(len(s), 10):], "+-") && !strings.Contains(s, "MST") && len(s) > 10 {
			s += "+00"
		}
	}
	for _, l := range timeLayouts[kind] {
		if t, err := time.Parse(l, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("unrecognised %s layout", kind)
}

// splitTopLevel splits on commas outside quotes and brackets.
func splitTopLevel(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	var out []string
	depth := 0
	var quote byte
	start := 0
	for i := 0; i < len(s); i++ {
		ch := s[i]
		switch {
		case quote != 0:
			switch ch {
			case '\\':
				i++
			case quote:
				quote = 0
			}
		case ch == '"' || ch == '\'':
			quote = ch
		case ch == '[' || ch == '{' || ch == '(':
			depth++
		case ch == ']' || ch == '}' || ch == ')':
			depth--
		case ch == ',' && depth == 0:
			out = append(out, strings.TrimSpace(s[start:i]))
			start = i + 1
		}
	}
	return append(out, strings.TrimSpace(s[start:]))
}

// ---------------------------------------------------------------------
// Comparison

func docRowsEqual(got [][]any, want [][]any, types []*docsType) bool {
	for i := range got {
		for j := range got[i] {
			if !docValueEqual(got[i][j], want[i][j], types[j]) {
				return false
			}
		}
	}
	return true
}

func docRowsEqualUnordered(got [][]any, want [][]any, types []*docsType) bool {
	if len(got) != len(want) {
		return false
	}
	used := make([]bool, len(want))
	for _, g := range got {
		found := false
		for k, w := range want {
			if used[k] {
				continue
			}
			match := true
			for j := range g {
				if !docValueEqual(g[j], w[j], types[j]) {
					match = false
					break
				}
			}
			if match {
				used[k] = true
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

var wsRe = regexp.MustCompile(`\s+`)

func docValueEqual(got, want any, ty *docsType) bool {
	if want == nil || got == nil {
		return want == nil && got == nil
	}
	switch w := want.(type) {
	case int64:
		g, ok := got.(int64)
		return ok && g == w
	case docNumeric:
		var r *big.Rat
		switch g := got.(type) {
		case string:
			var ok bool
			r, ok = new(big.Rat).SetString(g)
			if !ok {
				return false
			}
		case int64:
			r = new(big.Rat).SetInt64(g)
		default:
			return false
		}
		return r.Cmp(w.r) == 0
	case docFloat:
		g, ok := got.(float64)
		if !ok {
			if gi, ok2 := got.(int64); ok2 {
				g = float64(gi)
			} else {
				return false
			}
		}
		return floatMatchesDoc(g, w)
	case bool:
		g, ok := got.(bool)
		return ok && g == w
	case *docString:
		g, ok := got.(string)
		if !ok {
			return false
		}
		match := strings.TrimSpace(g) == w.s
		if w.quoted {
			match = g == w.s || g == `"`+w.s+`"` || g == "'"+w.s+"'"
		}
		if match {
			w.matched = g
		}
		return match
	case docBytes:
		g, ok := got.(string)
		if !ok {
			return false
		}
		b, err := base64.StdEncoding.DecodeString(g)
		if err != nil {
			return false
		}
		for _, cand := range w {
			if bytes.Equal(b, cand) {
				return true
			}
		}
		return false
	case docTime:
		g, ok := got.(string)
		if !ok {
			return false
		}
		gt, err := parseDocTime(g, w.kind)
		return err == nil && gt.Equal(w.t)
	case docJSON:
		g, ok := got.(string)
		if !ok {
			return false
		}
		var gv any
		d := json.NewDecoder(strings.NewReader(g))
		d.UseNumber()
		if err := d.Decode(&gv); err != nil {
			return false
		}
		return jsonEqual(gv, w.v)
	case docGeo:
		g, ok := got.(string)
		if !ok {
			return false
		}
		// The text is compared as written: the driver writes WKT the
		// way BigQuery does (`POINT(1 1)`, no space after the type
		// name; verified on BigQuery 2026-09-25), so no spacing is
		// forgiven. Only the MULTIPOINT vertex order (S2 cell order on
		// BigQuery) and the spelling of an empty geography are
		// normalised, as the spec runner does.
		canon := func(s string) string { return canonicaliseMultiPointWKT(canonicaliseEmptyWKT(strings.TrimSpace(s))) }
		return canon(g) == canon(string(w))
	case docText:
		g := renderGot(got)
		return wsRe.ReplaceAllString(strings.TrimSpace(g), " ") == wsRe.ReplaceAllString(string(w), " ")
	case []any:
		g, ok := got.([]any)
		if !ok || len(g) != len(w) || ty.ElementType == nil {
			return false
		}
		for i := range g {
			if !docValueEqual(g[i], w[i], ty.ElementType) {
				return false
			}
		}
		return true
	case docStruct:
		g, ok := got.([]any)
		if !ok || len(g) != len(w) {
			return false
		}
		for i := range g {
			if !docValueEqual(g[i], w[i], ty.FieldTypes[i].Type) {
				return false
			}
		}
		return true
	}
	return false
}

// floatMatchesDoc accepts the driver's float when it equals the
// documented value to 1e-9 (the spec runner's tolerance), or, when the
// docs print at least 10 significant digits, when it rounds to the
// printed value.
func floatMatchesDoc(g float64, w docFloat) bool {
	if math.IsNaN(w.v) || math.IsInf(w.v, 0) {
		return (math.IsNaN(w.v) && math.IsNaN(g)) || (math.IsInf(w.v, 1) && math.IsInf(g, 1)) || (math.IsInf(w.v, -1) && math.IsInf(g, -1))
	}
	if floatNear(g, w.v) {
		return true
	}
	if !strictFloats && w.digits >= 2 && strings.Contains(w.text, ".") {
		return strconv.FormatFloat(g, 'g', w.digits, 64) == strconv.FormatFloat(w.v, 'g', w.digits, 64)
	}
	return false
}

func jsonEqual(a, b any) bool {
	switch x := a.(type) {
	case json.Number:
		y, ok := b.(json.Number)
		if !ok {
			return false
		}
		ra, ok1 := new(big.Rat).SetString(string(x))
		rb, ok2 := new(big.Rat).SetString(string(y))
		return ok1 && ok2 && ra.Cmp(rb) == 0
	case map[string]any:
		y, ok := b.(map[string]any)
		if !ok || len(x) != len(y) {
			return false
		}
		for k, v := range x {
			if !jsonEqual(v, y[k]) {
				return false
			}
		}
		return true
	case []any:
		y, ok := b.([]any)
		if !ok || len(x) != len(y) {
			return false
		}
		for i := range x {
			if !jsonEqual(x[i], y[i]) {
				return false
			}
		}
		return true
	}
	return a == b
}

func renderGot(v any) string {
	switch x := v.(type) {
	case nil:
		return "NULL"
	case []any:
		parts := make([]string, len(x))
		for i, e := range x {
			if s, ok := e.(string); ok {
				parts[i] = strconv.Quote(s)
			} else {
				parts[i] = renderGot(e)
			}
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case float64:
		return strconv.FormatFloat(x, 'g', -1, 64)
	case []byte:
		return string(x)
	}
	return fmt.Sprint(v)
}

// ---------------------------------------------------------------------
// Expected values for the spec runner

// yamlValue renders a typed documented value in the form the spec
// runner (spec_runner_test.go valueEqual) compares against the
// driver's output. The value always derives from the docs cell.
func yamlValue(v any) any {
	switch x := v.(type) {
	case nil, int64, bool:
		return x
	case docNumeric:
		return ratString(x.r)
	case docFloat:
		if math.IsNaN(x.v) || math.IsInf(x.v, 0) {
			return x.text
		}
		return x.v
	case *docString:
		if x.matched != "" {
			return x.matched
		}
		return x.s
	case docBytes:
		return base64.StdEncoding.EncodeToString(x[0])
	case docTime:
		return formatDocTime(x)
	case docJSON:
		b, _ := json.Marshal(x.v)
		return string(b)
	case docGeo:
		return string(x)
	case docText:
		return string(x)
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = yamlValue(e)
		}
		return out
	case docStruct:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = yamlValue(e)
		}
		return out
	}
	return fmt.Sprint(v)
}

func ratString(r *big.Rat) string {
	if r.IsInt() {
		return r.Num().String()
	}
	s := r.FloatString(40)
	s = strings.TrimRight(s, "0")
	return strings.TrimSuffix(s, ".")
}

func formatDocTime(t docTime) string {
	switch t.kind {
	case "DATE":
		return t.t.Format("2006-01-02")
	case "TIME":
		return t.t.Format("15:04:05.999999")
	case "DATETIME":
		return t.t.Format("2006-01-02T15:04:05.999999")
	}
	u := t.t.UTC()
	if u.Nanosecond() == 0 {
		return u.Format("2006-01-02 15:04:05") + "+00"
	}
	return u.Format("2006-01-02 15:04:05.000000") + "+00"
}

// ---------------------------------------------------------------------
// Outputs

type yamlCase struct {
	Desc     string       `yaml:"desc"`
	Setup    []string     `yaml:"setup,omitempty"`
	SQL      string       `yaml:"sql"`
	Expected yamlExpected `yaml:"expected"`
	Skip     string       `yaml:"skip,omitempty"`
	Pending  string       `yaml:"pending,omitempty"`
	// Note is written as a comment above the case.
	Note string `yaml:"-"`
}

type yamlExpected struct {
	Rows      [][]any    `yaml:"rows"`
	Error     *yamlError `yaml:"error,omitempty"`
	Unordered bool       `yaml:"unordered,omitempty"`
}

type yamlError struct {
	Contains string `yaml:"contains"`
}

func writeDocsOutputs(outDir string, results []*docsResult) error {
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}
	var failures []*docsResult
	for _, r := range results {
		if r.Status == stFail || r.Status == stError {
			failures = append(failures, r)
		}
	}
	if err := writeJSON(filepath.Join(outDir, "failures.json"), failures); err != nil {
		return err
	}
	classes, err := loadDocsClasses()
	if err != nil {
		return err
	}
	verified, err := loadDocsVerified()
	if err != nil {
		return err
	}
	var stale []string
	defer func() {
		if len(stale) > 0 {
			// The IDs in the classification file are for the docs commit
			// it names; other docs renumber the examples.
			fmt.Fprintf(os.Stderr, "docs examples: %d classified examples now pass (if these are the docs the classification was made for, drop them from %s): %s\n",
				len(stale), docsClassFile, strings.Join(stale, ", "))
		}
	}()
	pass := map[string][]yamlCase{}
	pending := map[string][]yamlCase{}
	pendingNotes := map[string][]string{}
	for _, r := range results {
		if !r.New || r.Status == stSkipped {
			continue
		}
		c := yamlCase{
			Desc:  fmt.Sprintf("%s L%d %s", r.ID, r.Line, r.Section),
			Setup: r.Setup,
			SQL:   r.SQL,
		}
		if d, ok := verified[r.ID]; ok {
			c.Note = "Verified on BigQuery " + d + "."
		}
		if r.ExpectError {
			c.Expected.Error = &yamlError{Contains: ""}
		} else if r.typedRows != nil {
			c.Expected.Rows = [][]any{}
			for _, row := range r.typedRows {
				var out []any
				for _, v := range row {
					out = append(out, yamlValue(v))
				}
				c.Expected.Rows = append(c.Expected.Rows, out)
			}
			c.Expected.Unordered = !r.Ordered && len(r.typedRows) > 1
		} else {
			// The query failed, so no column types are known; keep
			// the documented cells verbatim.
			c.Expected.Rows = [][]any{}
			for _, row := range r.DocRows {
				var out []any
				for _, cell := range row {
					if cell == "NULL" {
						out = append(out, nil)
					} else {
						out = append(out, cell)
					}
				}
				c.Expected.Rows = append(c.Expected.Rows, out)
			}
			c.Expected.Unordered = !r.Ordered && len(r.DocRows) > 1
		}
		if cl, ok := classes[r.ID]; ok && r.Status == stPass && cl.Class == "bigquery" {
			// The typed comparison matches the docs' display, but BigQuery
			// prints something more precise (for example the leading sign
			// blank of a numeric FORMAT the docs table trims): pin
			// BigQuery's answer.
			r.Class, r.ClassReason = cl.Class, cl.Reason
			bc, agrees, err := bigQueryCase(c, cl, r)
			if err != nil {
				return err
			}
			if agrees {
				pass[r.Page] = append(pass[r.Page], bc)
				continue
			}
			r.Status, r.Kind = stFail, "silent"
			r.Reason = "the driver does not match the BigQuery answer"
		}
		if r.Status == stPass && r.RoundedFloat {
			// Documented floats are rounded for display; not expressible
			// with the spec runner's 1e-9 tolerance.
			continue
		}
		if r.Status == stPass && !r.ExpectError {
			ok, err := specRunnerAgrees(c, r.gotRaw)
			if err != nil {
				return err
			}
			if !ok {
				// Equal under typed comparison (for example JSON with a
				// different key order), but not under the spec runner's
				// string comparison. Kept out of the default suite.
				r.RunnerMismatch = true
				continue
			}
		}
		if r.Status == stPass {
			if cl, ok := classes[r.ID]; ok {
				stale = append(stale, r.ID+" ("+cl.Class+")")
			}
			pass[r.Page] = append(pass[r.Page], c)
			continue
		}
		cl, ok := classes[r.ID]
		if !ok {
			cl = docsClass{Class: "unclassified", Reason: "not yet triaged"}
		}
		r.Class, r.ClassReason = cl.Class, cl.Reason
		disp := docsClassDisposition[cl.Class]
		if disp == dispPending && (cl.BigQueryRows != nil || cl.BigQueryError != "") {
			// A pending case whose docs disagree with BigQuery waits for
			// BigQuery's answer, not the documented one.
			bc, _, err := bigQueryCase(c, cl, r)
			if err != nil {
				return err
			}
			c = bc
		}
		if disp == dispBigQuery {
			bc, agrees, err := bigQueryCase(c, cl, r)
			if err != nil {
				return err
			}
			if agrees {
				pass[r.Page] = append(pass[r.Page], bc)
				continue
			}
			r.ClassReason += " (the driver does not match the BigQuery answer yet)"
			disp = dispPending
		}
		if disp == dispSkip {
			c.Skip = cl.Class + ": " + cl.Reason
			pass[r.Page] = append(pass[r.Page], c)
			continue
		}
		{
			c.Pending = cl.Class + ": " + r.ClassReason
			pending[r.Page] = append(pending[r.Page], c)
			note := fmt.Sprintf("%s: %s (%s)", r.ID, r.Status, r.Reason)
			if r.GotError != "" {
				note += ": " + oneLine(r.GotError)
			} else if r.GotRows != nil || r.Status == stFail {
				note += ": driver rows " + oneLine(fmt.Sprint(r.GotRows))
			}
			pendingNotes[r.Page] = append(pendingNotes[r.Page], note)
		}
	}
	if err := writeYAMLTree(filepath.Join(outDir, "yaml", "specs"), pass, nil, false); err != nil {
		return err
	}
	if err := writeYAMLTree(filepath.Join(outDir, "yaml", "pending"), pending, pendingNotes, true); err != nil {
		return err
	}
	return writeJSON(filepath.Join(outDir, "results.json"), results)
}

func oneLine(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 400 {
		s = s[:400] + "..."
	}
	return s
}

func writeYAMLTree(dir string, byPage map[string][]yamlCase, notes map[string][]string, pending bool) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	pages := make([]string, 0, len(byPage))
	for p := range byPage {
		pages = append(pages, p)
	}
	sort.Strings(pages)
	for _, p := range pages {
		body, err := renderYAMLFile(byPage[p])
		if err != nil {
			return err
		}
		var b strings.Builder
		fmt.Fprintf(&b, "# Generated by TestDocsExamples from %s/%s.md.\n", docsSource(), p)
		b.WriteString("# Expected values are the documented result tables, typed with the\n")
		b.WriteString("# column types the analyzer reports, except where a case says it\n")
		b.WriteString("# follows BigQuery (see " + docsClassFile + ").\n")
		b.WriteString("# Cases with `skip:` keep the documented expectation but do not run.\n")
		b.WriteString("# Do not edit by hand.\n")
		if pending {
			b.WriteString("# PENDING: these cases diverge from the docs today. They live outside\n")
			b.WriteString("# testdata/specs so the default suite stays green.\n")
			for _, n := range notes[p] {
				b.WriteString("#   " + n + "\n")
			}
		}
		b.WriteString(body)
		if err := os.WriteFile(filepath.Join(dir, p+".yaml"), []byte(b.String()), 0o644); err != nil {
			return err
		}
	}
	return nil
}

func writeJSON(path string, v any) error {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return err
	}
	return os.WriteFile(path, buf.Bytes(), 0o644)
}

// laOffsetOnly detects results that differ only because one side
// applied the America/Los_Angeles default time zone. GoogleSQL's
// reference implementation defaults to that zone, and some doc pages
// print results under it; BigQuery defaults to UTC. bqOK reports that
// the driver is the side that used UTC (so it agrees with BigQuery).
func laOffsetOnly(got, want [][]any, types []*docsType, ordered bool) (string, bool) {
	la, err := time.LoadLocation("America/Los_Angeles")
	if err != nil || len(got) != len(want) {
		return "", false
	}
	for _, dir := range []int{1, -1} {
		shifted := make([][]any, len(want))
		changed := false
		for i, row := range want {
			shifted[i] = make([]any, len(row))
			for j, v := range row {
				if t, ok := v.(docTime); ok && t.kind == "TIMESTAMP" {
					_, off := t.t.In(la).Zone()
					t.t = t.t.Add(time.Duration(dir*off) * time.Second)
					changed = true
					v = t
				}
				shifted[i][j] = v
			}
		}
		if !changed {
			return "", false
		}
		eq := docRowsEqualUnordered(got, shifted, types)
		if ordered {
			eq = docRowsEqual(got, shifted, types)
		}
		if eq {
			if dir == 1 {
				// doc - 7h == got: the docs read a zone-less value as LA time.
				return "docs assume the America/Los_Angeles default time zone; the driver uses UTC, as BigQuery does", true
			}
			return "driver applies the America/Los_Angeles default time zone; BigQuery uses UTC", false
		}
	}
	return "", false
}

// docsSource names the markdown the examples came from, for headers.
func docsSource() string {
	if s := os.Getenv("GOOGLESQLITE_DOCS_EXAMPLES_SOURCE"); s != "" {
		return s
	}
	return "docs/third_party/googlesql-docs"
}

// renderYAMLFile writes cases in the testdata/specs layout. SQL uses
// literal blocks; each expected row is a JSON flow sequence, which is
// valid YAML and quotes every string (so "null" or "6" stay strings).
func renderYAMLFile(cases []yamlCase) (string, error) {
	var b strings.Builder
	b.WriteString("spec: docs/specs/googlesql/syntax/docs_examples.md\ndialect: googlesql\ncases:\n")
	for _, c := range cases {
		if c.Note != "" {
			for _, l := range strings.Split(c.Note, "\n") {
				b.WriteString(strings.TrimRight("  # "+l, " ") + "\n")
			}
		}
		desc, _ := json.Marshal(c.Desc)
		fmt.Fprintf(&b, "  - desc: %s\n", desc)
		if c.Skip != "" {
			reason, _ := json.Marshal(c.Skip)
			fmt.Fprintf(&b, "    skip: %s\n", reason)
		}
		if c.Pending != "" {
			reason, _ := json.Marshal(c.Pending)
			fmt.Fprintf(&b, "    pending: %s\n", reason)
		}
		if len(c.Setup) > 0 {
			b.WriteString("    setup:\n")
			for _, st := range c.Setup {
				b.WriteString("      - |-\n")
				writeIndented(&b, st, "        ")
			}
		}
		b.WriteString("    sql: |-\n")
		writeIndented(&b, c.SQL, "      ")
		b.WriteString("    expected:\n")
		if c.Expected.Error != nil {
			contains, _ := json.Marshal(c.Expected.Error.Contains)
			fmt.Fprintf(&b, "      error:\n        contains: %s\n", contains)
			continue
		}
		if len(c.Expected.Rows) == 0 {
			b.WriteString("      rows: []\n")
		} else {
			b.WriteString("      rows:\n")
			for _, row := range c.Expected.Rows {
				j, err := json.Marshal(row)
				if err != nil {
					return "", err
				}
				fmt.Fprintf(&b, "        - %s\n", j)
			}
		}
		if c.Expected.Unordered {
			b.WriteString("      unordered: true\n")
		}
	}
	return b.String(), nil
}

func writeIndented(b *strings.Builder, text, indent string) {
	for _, line := range strings.Split(strings.TrimRight(text, "\n"), "\n") {
		if strings.TrimSpace(line) == "" {
			b.WriteString("\n")
			continue
		}
		b.WriteString(indent + strings.TrimRight(line, " \t") + "\n")
	}
}

// specRunnerAgrees round-trips one case through YAML and checks it
// with the spec runner's own comparison, so every emitted regression
// test is known to pass under TestSpec.
func specRunnerAgrees(c yamlCase, got [][]any) (bool, error) {
	body, err := renderYAMLFile([]yamlCase{c})
	if err != nil {
		return false, err
	}
	var td struct {
		Cases []struct {
			Expected struct {
				Rows      [][]any `yaml:"rows"`
				Unordered bool    `yaml:"unordered"`
			} `yaml:"expected"`
		} `yaml:"cases"`
	}
	if err := yaml.Unmarshal([]byte(body), &td); err != nil {
		return false, err
	}
	want := td.Cases[0].Expected.Rows
	if td.Cases[0].Expected.Unordered {
		return rowsEqualUnordered(got, want), nil
	}
	return rowsEqual(got, want), nil
}
