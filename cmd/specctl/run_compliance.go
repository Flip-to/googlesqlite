package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	_ "github.com/goccy/googlesqlite"

	"github.com/goccy/googlesqlite/cmd/specctl/compliancetest"
)

func init() {
	register(command{
		name:    "run-compliance",
		summary: "run the GoogleSQL compliance .test suite through the driver and report divergences",
		run:     runCompliance,
	})
}

// Case outcome statuses.
const (
	statusPass  = "pass"
	statusFail  = "fail"  // wrong result, no error from the driver
	statusError = "error" // the driver errored where rows were expected
	statusSkip  = "skip"
)

// Failure kinds, ordered by how likely they are to be silent wrong
// answers in production.
const (
	kindWrongValues   = "wrong_values"            // same shape, different values
	kindExpectedError = "expected_error_got_rows" // should have failed, returned rows
	kindRowCount      = "row_count"               // different number of rows
	kindShape         = "shape"                   // column count / type shape
	kindDriverError   = "driver_error"            // loud error
	kindTimeout       = "timeout"
	kindPanic         = "panic"
)

var kindScore = map[string]int{
	kindWrongValues:   100,
	kindExpectedError: 90,
	kindRowCount:      80,
	kindShape:         60,
	kindPanic:         20,
	kindDriverError:   10,
	kindTimeout:       5,
}

type caseResult struct {
	File       string   `json:"file"`
	Index      int      `json:"index"`
	Name       string   `json:"name"`
	Features   []string `json:"features,omitempty"`
	Status     string   `json:"status"`
	Kind       string   `json:"kind,omitempty"`
	Silent     bool     `json:"silent"`
	Score      int      `json:"score,omitempty"`
	SkipReason string   `json:"skip_reason,omitempty"`
	Reason     string   `json:"reason,omitempty"`
	SQL        string   `json:"sql,omitempty"`
	Expected   string   `json:"expected,omitempty"`
	Emulator   string   `json:"emulator,omitempty"`
	// ErrorClassNote is set on passing expected-error cases whose
	// error class (analysis vs runtime) differs from the suite's.
	ErrorClassNote string   `json:"error_class_note,omitempty"`
	Constructs     []string `json:"dbt_constructs,omitempty"`
	Prepare        bool     `json:"prepare,omitempty"`
}

var verboseLog bool

type setupState struct {
	stmts []string // successful setup statements, for replay
	// temp holds successful CREATE TEMP FUNCTION setup statements. The
	// driver scopes temp objects to the script that creates them (as
	// BigQuery does), so they are prepended to every later case.
	temp   []string
	failed map[string]string // lower-case object name -> reason
}

func runCompliance(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("run-compliance", flag.ContinueOnError)
	dir := fs.String("dir", "", "path to googlesql/compliance/testdata")
	outJSON := fs.String("json", "compliance_results.json", "output JSON with every non-passing, non-skipped case")
	allJSON := fs.String("all-json", "", "optional output JSON with every case outcome")
	outMD := fs.String("md", "compliance_summary.md", "output markdown summary")
	filter := fs.String("files", "", "regexp restricting which .test files run")
	jobs := fs.Int("j", 4, "files run in parallel")
	timeout := fs.Duration("timeout", 30*time.Second, "per-statement timeout")
	verbose := fs.Bool("v", false, "log each case to stderr before it runs")
	worker := fs.String("worker", "", "internal: run one file in this process and stream JSON lines")
	workerStart := fs.Int("start", 1, "internal: first case index for -worker")
	localUTC := fs.Bool("local-utc", false, "set the process local time zone (time.Local) to UTC before running")
	if err := fs.Parse(args); err != nil {
		return err
	}
	verboseLog = *verbose
	if *localUTC {
		time.Local = time.UTC
	}
	if *worker != "" {
		return runWorker(ctx, *worker, *workerStart, *timeout)
	}
	if *dir == "" {
		return errors.New("-dir is required")
	}
	workerArgs := []string{"-timeout", timeout.String()}
	if *localUTC {
		workerArgs = append(workerArgs, "-local-utc")
	}
	files, err := filepath.Glob(filepath.Join(*dir, "*.test"))
	if err != nil {
		return err
	}
	if *filter != "" {
		re, err := regexp.Compile(*filter)
		if err != nil {
			return err
		}
		kept := files[:0]
		for _, f := range files {
			if re.MatchString(filepath.Base(f)) {
				kept = append(kept, f)
			}
		}
		files = kept
	}
	sort.Strings(files)

	var (
		mu      sync.Mutex
		results []caseResult
		wg      sync.WaitGroup
		next    int64 = -1
	)
	for w := 0; w < *jobs; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				i := int(atomic.AddInt64(&next, 1))
				if i >= len(files) || ctx.Err() != nil {
					return
				}
				start := time.Now()
				rs := runFileIsolated(ctx, files[i], *timeout, workerArgs)
				fmt.Fprintf(os.Stderr, "%-60s %5d cases %6.1fs\n", filepath.Base(files[i]), len(rs), time.Since(start).Seconds())
				mu.Lock()
				results = append(results, rs...)
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	sort.Slice(results, func(a, b int) bool {
		if results[a].File != results[b].File {
			return results[a].File < results[b].File
		}
		return results[a].Index < results[b].Index
	})

	var failing []caseResult
	for _, r := range results {
		if r.Status == statusFail || r.Status == statusError {
			failing = append(failing, r)
		}
	}
	rankFailures(failing)
	if err := writeJSONFile(*outJSON, failing); err != nil {
		return err
	}
	if *allJSON != "" {
		if err := writeJSONFile(*allJSON, results); err != nil {
			return err
		}
	}
	md := renderComplianceSummary(results, failing)
	if err := os.WriteFile(*outMD, []byte(md), 0o644); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "wrote %s (%d failing) and %s\n", *outJSON, len(failing), *outMD)
	return nil
}

// rankFailures orders failures: dbt-relevant first, then by how
// likely the failure is a silent wrong answer.
func rankFailures(rs []caseResult) {
	sort.SliceStable(rs, func(a, b int) bool {
		da, db := len(rs[a].Constructs) > 0, len(rs[b].Constructs) > 0
		if da != db {
			return da
		}
		if rs[a].Score != rs[b].Score {
			return rs[a].Score > rs[b].Score
		}
		if rs[a].File != rs[b].File {
			return rs[a].File < rs[b].File
		}
		return rs[a].Index < rs[b].Index
	})
}

func writeJSONFile(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o644)
}

type fileRunner struct {
	path    string
	timeout time.Duration
	dbSeq   *int64
	db      *sql.DB
	conn    *sql.Conn
	setup   setupState
}

func (fr *fileRunner) open(ctx context.Context) error {
	n := atomic.AddInt64(fr.dbSeq, 1)
	db, err := sql.Open("googlesqlite", fmt.Sprintf(":memory:?_test=compliance_%d", n))
	if err != nil {
		return err
	}
	db.SetMaxOpenConns(1)
	conn, err := db.Conn(ctx)
	if err != nil {
		db.Close()
		return err
	}
	fr.db, fr.conn = db, conn
	for _, s := range fr.setup.stmts {
		if _, _, err := fr.exec(ctx, s); err != nil {
			return fmt.Errorf("replaying setup: %w", err)
		}
	}
	return nil
}

// exec runs one statement and returns its rows. timedOut is true when
// the statement exceeded the timeout; the connection is then unusable.
func (fr *fileRunner) exec(ctx context.Context, q string, args ...any) (rows [][]any, timedOut bool, err error) {
	type out struct {
		rows [][]any
		err  error
	}
	cctx, cancel := context.WithTimeout(ctx, fr.timeout)
	defer cancel()
	ch := make(chan out, 1)
	conn := fr.conn
	go func() {
		defer func() {
			if p := recover(); p != nil {
				ch <- out{err: fmt.Errorf("panic: %v", p)}
			}
		}()
		r, err := queryAll(cctx, conn, q, args...)
		ch <- out{r, err}
	}()
	select {
	case o := <-ch:
		return o.rows, false, o.err
	case <-time.After(fr.timeout + 5*time.Second):
		return nil, true, fmt.Errorf("timeout after %s", fr.timeout)
	}
}

func queryAll(ctx context.Context, conn *sql.Conn, q string, args ...any) ([][]any, error) {
	rows, err := conn.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	var out [][]any
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		out = append(out, vals)
	}
	return out, rows.Err()
}

var tempFuncRe = regexp.MustCompile(`(?is)^\s*CREATE\s+(?:OR\s+REPLACE\s+)?TEMP(?:ORARY)?\s+(?:AGGREGATE\s+)?FUNCTION[[:space:](]`)

var createConstantRe = regexp.MustCompile(`(?is)^\s*CREATE\s+(?:OR\s+REPLACE\s+)?(?:TEMP(?:ORARY)?\s+|PUBLIC\s+|PRIVATE\s+)?CONSTANT\s`)

var literalOnlyArgRe = regexp.MustCompile(`must be a string literal or query parameter|must be one of .* but is`)

// paramArgs evaluates each parameter expression and returns the values
// as named query arguments.
func (fr *fileRunner) paramArgs(ctx context.Context, params []compliancetest.Param) ([]any, error) {
	args := make([]any, 0, len(params))
	for _, p := range params {
		rows, err := queryAll(ctx, fr.conn, "SELECT "+p.Expr)
		if err != nil {
			return nil, err
		}
		if len(rows) != 1 || len(rows[0]) != 1 {
			return nil, fmt.Errorf("parameter %s: unexpected shape", p.Name)
		}
		args = append(args, sql.Named(p.Name, rows[0][0]))
	}
	return args, nil
}

var tableNotFoundRe = regexp.MustCompile(`(?:Table|Function) not found: ([\w.` + "`" + `]+)`)

func (fr *fileRunner) runCase(ctx context.Context, c compliancetest.SuiteCase) (res caseResult) {
	res = caseResult{
		File:     c.File,
		Index:    c.Index,
		Name:     c.Name,
		Features: c.AllFeatures,
		Prepare:  c.Prepare,
		Expected: compliancetest.FirstSection(c.Expected),
	}
	query := compliancetest.SubstituteParams(c.SQL, c.Params)
	res.SQL = query
	header := firstLineOf(res.Expected)
	res.Constructs = compliancetest.MatchConstructs(query, header)
	obj := strings.ToLower(compliancetest.CreatedObject(query))

	skip := func(reason string) caseResult {
		res.Status, res.SkipReason = statusSkip, reason
		if c.Prepare && obj != "" {
			fr.setup.failed[obj] = "setup skipped: " + reason
		}
		return res
	}

	if c.ParamErr != "" {
		return skip("runner: cannot parse parameters: " + c.ParamErr)
	}
	if reason := compliancetest.CaseSkipReason(c.File, c.Name); reason != "" {
		return skip(reason)
	}
	if reason := compliancetest.SkipReason(c.AllFeatures, c.Forbidden, query, header); reason != "" {
		return skip(reason)
	}
	if tz, ok := c.Attrs["default_time_zone"]; ok && !strings.EqualFold(tz, "UTC") {
		return skip("non-UTC default_time_zone " + tz + " (BigQuery sessions default to UTC)")
	}
	if mode := c.Attrs["primary_key_mode"]; mode != "" && mode != "no_primary_key" {
		return skip("primary_key_mode " + mode + " (BigQuery primary keys are NOT ENFORCED)")
	}
	if strings.HasPrefix(res.Expected, "ERROR: generic::unimplemented") {
		return skip("reference implementation limitation (expected generic::unimplemented)")
	}
	if createConstantRe.MatchString(query) {
		return skip("CREATE CONSTANT is not BigQuery DDL")
	}
	if strings.HasPrefix(res.Expected, "ScriptResult") || c.Attrs["script_mode"] != "" {
		return skip("runner: script-mode cases not supported")
	}
	expected, perr := compliancetest.ParseResult(c.Expected)
	if perr != nil && c.Prepare && strings.TrimSpace(c.Expected) == "" {
		// Setup DDL without an expected result (CREATE TEMP FUNCTION).
		expected, perr = compliancetest.Result{}, nil
	}
	if perr != nil {
		shape := firstLineOf(res.Expected)
		if strings.HasPrefix(shape, "STRUCT<num_rows_modified") || strings.HasPrefix(shape, "STRUCT<") {
			return skip("runner: DML result shape not compared")
		}
		return skip("runner: cannot parse expected value: " + perr.Error())
	}
	if deps := referencedFailed(query, fr.setup.failed); len(deps) > 0 {
		return skip("depends on " + deps[0])
	}

	run := query
	if len(fr.setup.temp) > 0 {
		run = strings.Join(fr.setup.temp, ";\n") + ";\n" + query
	}
	rows, timedOut, err := fr.exec(ctx, run)
	if !timedOut && err != nil && len(c.Params) > 0 && literalOnlyArgRe.MatchString(err.Error()) {
		// Inlining a parameter as (expr) breaks arguments that must be
		// "a string literal or query parameter" (KEYS.NEW_KEYSET's key
		// type, ...). Bind the parameters for real and rerun.
		if args, perr := fr.paramArgs(ctx, c.Params); perr == nil {
			bound := c.SQL
			if len(fr.setup.temp) > 0 {
				bound = strings.Join(fr.setup.temp, ";\n") + ";\n" + bound
			}
			rows, timedOut, err = fr.exec(ctx, bound, args...)
		}
	}
	if timedOut {
		// Closing would block on the still-running statement, so the
		// timed-out connection is abandoned and a fresh one opened.
		fr.conn, fr.db = nil, nil
		if oerr := fr.open(ctx); oerr != nil {
			res.Reason = "reopen after timeout failed: " + oerr.Error()
		}
		res.Status, res.Kind, res.Emulator = statusError, kindTimeout, err.Error()
		res.Score = kindScore[res.Kind]
		return res
	}

	if c.Prepare {
		if err != nil {
			if obj != "" {
				fr.setup.failed[obj] = "setup failed: " + oneLine(err.Error())
			}
			res.Status, res.Kind, res.Emulator = statusError, kindDriverError, err.Error()
			res.Score = kindScore[res.Kind]
			return res
		}
		if tempFuncRe.MatchString(query) {
			fr.setup.temp = append(fr.setup.temp, strings.TrimRight(strings.TrimSpace(query), ";"))
		} else {
			fr.setup.stmts = append(fr.setup.stmts, query)
		}
		// CREATE TABLE AS SELECT reports the created rows; the driver
		// returns none for DDL, so a successful setup counts as pass.
		res.Status = statusPass
		return res
	}

	if expected.Err != nil {
		if err != nil {
			res.Status = statusPass
			if note := errorClassNote(expected.Err, err); note != "" {
				res.ErrorClassNote = note
			}
			return res
		}
		res.Status, res.Kind, res.Silent = statusFail, kindExpectedError, true
		res.Reason = "expected " + strings.TrimSpace(expected.Err.Code+" "+expected.Err.Message) + " but query returned rows"
		res.Emulator = compliancetest.RenderRows(rows)
		res.Score = kindScore[res.Kind]
		return res
	}
	if err != nil {
		if m := tableNotFoundRe.FindStringSubmatch(err.Error()); m != nil {
			name := strings.ToLower(strings.Trim(m[1], "`"))
			if why, ok := fr.setup.failed[name]; ok {
				return skip("depends on " + name + " (" + why + ")")
			}
		}
		res.Status, res.Kind, res.Emulator = statusError, kindDriverError, err.Error()
		if strings.HasPrefix(err.Error(), "panic:") {
			res.Kind = kindPanic
		}
		res.Score = kindScore[res.Kind]
		return res
	}
	why := compliancetest.CompareRows(expected.Rows, rows)
	if why == "" {
		res.Status = statusPass
		return res
	}
	res.Status, res.Silent, res.Reason = statusFail, true, why
	res.Emulator = compliancetest.RenderRows(rows)
	switch {
	case strings.HasPrefix(why, "row count"):
		res.Kind = kindRowCount
	case strings.Contains(why, "column count") || strings.Contains(why, "expected STRUCT") || strings.Contains(why, "expected ARRAY") || strings.Contains(why, "value-table"):
		res.Kind = kindShape
	default:
		res.Kind = kindWrongValues
	}
	res.Score = kindScore[res.Kind]
	return res
}

// referencedFailed lists failed or skipped setup objects the query
// names, so dependent cases are skipped with an explicit reason.
func referencedFailed(q string, failed map[string]string) []string {
	if len(failed) == 0 {
		return nil
	}
	lq := strings.ToLower(q)
	var out []string
	for name, why := range failed {
		if regexp.MustCompile(`\b` + regexp.QuoteMeta(name) + `\b`).MatchString(lq) {
			out = append(out, name+" ("+why+")")
		}
	}
	sort.Strings(out)
	return out
}

// errorClassNote compares the suite's error class with the driver's
// phase. The driver prefixes analyzer failures with "failed to analyze".
func errorClassNote(exp *compliancetest.ExpectedErr, err error) string {
	analysis := strings.Contains(err.Error(), "failed to analyze") || strings.Contains(err.Error(), "failed to parse")
	switch {
	case strings.Contains(exp.Code, "out_of_range") && analysis:
		return "suite expects a runtime out_of_range error; driver failed at analysis"
	case strings.Contains(exp.Code, "invalid_argument") && !analysis:
		return "suite expects an analysis invalid_argument error; driver failed at runtime"
	}
	return ""
}

func firstLineOf(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '['); i >= 0 && strings.HasPrefix(s, "ARRAY<") {
		return strings.Join(strings.Fields(s[:i]), " ")
	}
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

func oneLine(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 200 {
		s = s[:200] + "..."
	}
	return s
}
