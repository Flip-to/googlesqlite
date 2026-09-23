package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/goccy/googlesqlite/internal/specmeta"
)

func init() {
	register(command{
		name:    "extract-docs-examples",
		summary: "extract every (SQL, result table) example from the upstream reference pages into JSON",
		run: func(_ context.Context, args []string) error {
			return runExtractDocsExamples(args)
		},
	})
}

// docExample is one runnable example pulled from an upstream reference
// page. The JSON form is consumed by the docs-examples runner
// (docs_examples_run_test.go in the root package), which types the raw
// cells against the column types the analyzer reports.
type docExample struct {
	ID      string `json:"id"`
	Page    string `json:"page"`
	Section string `json:"section"`
	Line    int    `json:"line"`
	// Setup holds statements executed before SQL on the same
	// connection: page-level fixtures (a `WITH T AS (...) SELECT * FROM
	// T` block or CREATE TABLE/INSERT statements) and earlier
	// statements of a multi-statement example.
	Setup    []string `json:"setup,omitempty"`
	Fixtures []string `json:"fixtures,omitempty"`
	// AltSetups are alternative setups the runner tries when Setup does
	// not reproduce the documented result: other definitions of the
	// same fixture name on the page or on other pages (the docs often
	// link a table defined elsewhere).
	AltSetups [][]string `json:"alt_setups,omitempty"`
	ownSetup  []string
	SQL       string `json:"sql"`
	// Columns and Rows are the documented result table, verbatim
	// (cells trimmed). Rows is empty for an empty result.
	Columns []string   `json:"columns,omitempty"`
	Rows    [][]string `json:"rows,omitempty"`
	// ExpectError marks examples the docs say must fail, either via a
	// `-- Error:` comment or prose such as "produces an error".
	ExpectError bool   `json:"expect_error,omitempty"`
	ErrorText   string `json:"error_text,omitempty"`
	// Ordered is true when the outermost query has an ORDER BY, so the
	// documented row order is part of the contract.
	Ordered bool `json:"ordered"`
	// Skip is non-empty when the example cannot be checked
	// deterministically; the value is the reason.
	Skip string `json:"skip,omitempty"`
	// CoveredBy names the existing testdata file whose case has the
	// same normalised SQL, if any.
	CoveredBy string `json:"covered_by,omitempty"`
}

func runExtractDocsExamples(args []string) error {
	docsDir := "docs/third_party/googlesql-docs"
	out := ""
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--docs":
			i++
			docsDir = args[i]
		case "--out":
			i++
			out = args[i]
		default:
			return fmt.Errorf("unknown flag %q", args[i])
		}
	}
	if out == "" {
		return fmt.Errorf("--out is required")
	}
	root, err := projectRoot()
	if err != nil {
		return err
	}
	if !filepath.IsAbs(docsDir) {
		docsDir = filepath.Join(root, docsDir)
	}
	pages, err := filepath.Glob(filepath.Join(docsDir, "*.md"))
	if err != nil {
		return err
	}
	sort.Strings(pages)
	// functions-and-operators.md concatenates the per-function pages;
	// process it last so duplicates are attributed to the specific page.
	sort.SliceStable(pages, func(i, j int) bool {
		return !isAggregatePage(pages[i]) && isAggregatePage(pages[j])
	})
	covered, err := loadExistingSQL(root)
	if err != nil {
		return err
	}
	var all []docExample
	seen := map[string]string{}
	type pageResult struct {
		exs []docExample
		fx  []fixture
	}
	var results []pageResult
	var global []fixture
	for _, p := range pages {
		body, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		page := strings.TrimSuffix(filepath.Base(p), ".md")
		exs, fx := extractPageExamplesFx(page, string(body))
		for i := range fx {
			fx[i].page = page
		}
		results = append(results, pageResult{exs, fx})
		global = append(global, fx...)
	}
	for _, pr := range results {
		exs := pr.exs
		for i := range exs {
			addAltSetups(&exs[i], pr.fx, global)
		}
		for i := range exs {
			if f, ok := covered[normalizeSQL(exs[i].SQL)]; ok {
				exs[i].CoveredBy = f
			}
			key := normalizeSQL(strings.Join(append(append([]string{}, exs[i].Setup...), exs[i].SQL), ";"))
			if first, ok := seen[key]; ok {
				if exs[i].Skip == "" {
					exs[i].Skip = "duplicate of " + first
				}
			} else {
				seen[key] = exs[i].ID
			}
		}
		all = append(all, exs...)
	}
	data, err := json.MarshalIndent(all, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(out, data, 0o644); err != nil {
		return err
	}
	skipped, coveredN := 0, 0
	for _, e := range all {
		if e.Skip != "" {
			skipped++
		}
		if e.CoveredBy != "" {
			coveredN++
		}
	}
	fmt.Fprintf(os.Stderr, "extract-docs-examples: %d examples from %d pages (%d already covered, %d skipped)\n",
		len(all), len(pages), coveredN, skipped)
	return nil
}

func isAggregatePage(path string) bool {
	return filepath.Base(path) == "functions-and-operators.md"
}

// loadExistingSQL maps the normalised SQL of every case under
// testdata/specs to the file that holds it.
func loadExistingSQL(root string) (map[string]string, error) {
	out := map[string]string{}
	base := filepath.Join(root, "testdata", "specs")
	err := filepath.WalkDir(base, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || filepath.Ext(path) != ".yaml" {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		if strings.Contains(rel, "/docs_examples/") {
			// Generated by this pipeline; do not count as prior coverage.
			return nil
		}
		td, err := specmeta.LoadTestdata(root, rel)
		if err != nil {
			return nil
		}
		for _, c := range td.Cases {
			k := normalizeSQL(c.SQL)
			if _, ok := out[k]; !ok {
				out[k] = rel
			}
		}
		return nil
	})
	return out, err
}

// normalizeSQL canonicalises SQL for duplicate detection: comments are
// dropped, whitespace collapsed, keywords lowercased, single quotes
// folded into double quotes, and a trailing semicolon removed.
func normalizeSQL(s string) string {
	s = stripSQLComments(s)
	s = strings.ReplaceAll(s, "'", `"`)
	s = strings.ToLower(strings.Join(strings.Fields(s), " "))
	s = strings.TrimSuffix(strings.TrimSpace(s), ";")
	s = strings.TrimSpace(s)
	// Collapse spacing around punctuation so `f( x )` equals `f(x)`.
	s = punctSpaceRe.ReplaceAllString(s, "$1")
	return s
}

var punctSpaceRe = regexp.MustCompile(`\s*([(),\[\]=<>+*/-])\s*`)

// ---------------------------------------------------------------------
// Markdown walking

type mdBlock struct {
	lang   string
	attrs  string
	body   string
	line   int // 1-based line of the first body line
	prose  string
	sectio string
}

var (
	headingRe = regexp.MustCompile(`^(#{1,6})\s+(.*?)\s*(\{#.*\})?$`)
	fenceRe   = regexp.MustCompile("^\\s*```\\s*([A-Za-z0-9_-]*)\\s*(\\{[^}]*\\})?\\s*$")
)

// splitMarkdownBlocks returns every fenced code block with its language,
// attributes, the heading it lives under, and the prose paragraph(s)
// immediately preceding it.
func splitMarkdownBlocks(md string) []mdBlock {
	lines := strings.Split(strings.ReplaceAll(md, "\r\n", "\n"), "\n")
	var out []mdBlock
	section := ""
	var prose []string
	for i := 0; i < len(lines); i++ {
		l := lines[i]
		if m := fenceRe.FindStringSubmatch(l); m != nil && strings.HasPrefix(strings.TrimSpace(l), "```") {
			j := i + 1
			for j < len(lines) && strings.TrimSpace(lines[j]) != "```" {
				j++
			}
			out = append(out, mdBlock{
				lang:   m[1],
				attrs:  m[2],
				body:   strings.Join(lines[i+1:min(j, len(lines))], "\n"),
				line:   i + 2,
				prose:  strings.Join(prose, " "),
				sectio: section,
			})
			prose = nil
			i = j
			continue
		}
		if m := headingRe.FindStringSubmatch(l); m != nil {
			section = strings.Trim(strings.TrimSpace(m[2]), "`")
			prose = nil
			continue
		}
		if t := strings.TrimSpace(l); t != "" {
			// Keep only the paragraph directly above the next block.
			if i > 0 && strings.TrimSpace(lines[i-1]) == "" {
				prose = nil
			}
			prose = append(prose, t)
		}
	}
	return out
}

// ---------------------------------------------------------------------
// Example extraction

var (
	errorMarkerRe = regexp.MustCompile(`(?i)^--\s*(?:error\b[:.]?|(?:this\s+)?(?:produces|returns|throws|raises)\s+an\s+error[:.]?)\s*(.*)$`)
	errorProseRe  = regexp.MustCompile(`(?i)(produces? an error|returns? an error|results? in an error|raises? an error|throws? an error|causes? an error|is an error|an error is (?:produced|returned|raised)|following (?:query|statement|example) (?:is invalid|fails)|is invalid:?$|isn't valid|is not valid)`)
	scriptRe      = regexp.MustCompile(`(?is)^\s*(DECLARE|BEGIN|IF\s|LOOP|WHILE|REPEAT|FOR\s+\w+\s+IN|CALL|EXECUTE\s+IMMEDIATE|SET\s+\w+\s*=|CREATE\s+(OR\s+REPLACE\s+)?PROCEDURE|RAISE|RETURN|BREAK|CONTINUE|LEAVE|ITERATE|CASE\s+WHEN\b.*\bTHEN\b.*END\s+CASE)`)
	bareCTERe     = regexp.MustCompile(`(?is)^\s*WITH\s+([A-Za-z_]\w*)\s+AS\s*\((.*)\)\s*;?\s*$`)
	cteFixtureRe  = regexp.MustCompile(`(?is)^\s*WITH\s+([A-Za-z_]\w*)\s+AS\s*\((.*)\)\s*SELECT\s+\*\s+FROM\s+([A-Za-z_]\w*)\s*;?\s*$`)
	createTableRe = regexp.MustCompile(`(?is)^\s*CREATE\s+(?:OR\s+REPLACE\s+)?(?:TEMP(?:ORARY)?\s+)?TABLE\s+(?:IF\s+NOT\s+EXISTS\s+)?([A-Za-z_][\w.]*)`)
	createFuncRe  = regexp.MustCompile(`(?is)^\s*CREATE\s+(?:OR\s+REPLACE\s+)?(?:TEMP(?:ORARY)?\s+)?(?:AGGREGATE\s+|TABLE\s+)?FUNCTION\s+(?:IF\s+NOT\s+EXISTS\s+)?([A-Za-z_][\w.]*)`)
	insertRe      = regexp.MustCompile(`(?is)^\s*INSERT\s+(?:INTO\s+)?([A-Za-z_][\w.]*)`)
	paramRe       = regexp.MustCompile(`(^|[^@\w])@[A-Za-z_]\w*`)
)

type fixture struct {
	name  string
	stmts []string
	line  int
	page  string
}

// pendingSQL is a SQL-only block awaiting a result table in the next
// code block (the docs sometimes put "The results look like this:"
// between the two).
type pendingSQL struct {
	blk   mdBlock
	sql   string
	setup []string
}

// extractPageExamples parses one markdown page into examples.
func extractPageExamples(page, md string) []docExample {
	exs, _ := extractPageExamplesFx(page, md)
	return exs
}

// extractPageExamplesFx also returns the page's fixture definitions.
func extractPageExamplesFx(page, md string) ([]docExample, []fixture) {
	blocks := splitMarkdownBlocks(md)
	var out []docExample
	var fixtures []fixture
	var pending *pendingSQL

	addExample := func(b mdBlock, lineOff int, setup []string, sql string, cols []string, rows [][]string, expErr bool, errText string, tableSkip string) {
		ex := docExample{
			Page:        page,
			Section:     b.sectio,
			Line:        b.line + lineOff,
			SQL:         sql,
			Setup:       setup,
			Columns:     cols,
			Rows:        rows,
			ExpectError: expErr,
			ErrorText:   errText,
		}
		ex.Ordered = hasTopLevelOrderBy(sql)
		ex.Skip = tableSkip
		out = append(out, ex)
	}

	for _, b := range blocks {
		if b.lang != "googlesql" && b.lang != "sql" && b.lang != "" {
			pending = nil
			continue
		}
		bad := strings.Contains(b.attrs, ".bad")
		body := b.body
		trimmed := strings.TrimSpace(body)
		if trimmed == "" {
			continue
		}
		if b.lang == "" {
			// Unlabelled fences hold grammar or output; only a pure
			// result table directly after a pending SQL block matters.
			if !(pending != nil && isTableStart(trimmed)) {
				pending = nil
				continue
			}
		}
		// A block that is only a result table pairs with the previous
		// SQL-only block.
		if isTableStart(trimmed) {
			segs := segmentBlock(body)
			if pending != nil && len(segs) >= 1 && strings.TrimSpace(segs[0].sql) == "" && segs[0].kind == segTable {
				cols, rows, skip := parseResultTable(segs[0].table)
				addExample(pending.blk, 0, pending.setup, pending.sql, cols, rows, false, "", skip)
			}
			pending = nil
			continue
		}
		pending = nil

		segs := segmentBlock(body)
		var carried []string
		hasMarker := false
		for _, s := range segs {
			if s.kind != segTrailing {
				hasMarker = true
			}
		}
		for _, s := range segs {
			stmts := splitStatements(s.sql)
			if len(stmts) == 1 && s.kind == segTrailing {
				stmts = splitOnCommentedParagraphs(stmts[0])
			}
			if s.kind == segTrailing {
				for _, st := range stmts {
					registerFixture(&fixtures, st, b.line+s.line)
				}
				if len(stmts) == 0 {
					continue
				}
				if hasMarker {
					continue
				}
				last := stmts[len(stmts)-1]
				setup := append(append([]string{}, carried...), stmts[:len(stmts)-1]...)
				var queries []string
				commentedErr := false
				for _, st := range stmts {
					if isQueryStatement(st) {
						queries = append(queries, st)
						if commentErrRe.MatchString(strings.ToLower(leadingComment(st))) {
							commentedErr = true
						}
					}
				}
				if commentedErr || errorProseRe.MatchString(b.prose) {
					// Per-statement leading comments ("-- This works",
					// "-- Produces an error") decide which statement fails;
					// without them, only a single-query block is usable.
					for _, st := range queries {
						c := strings.ToLower(leadingComment(st))
						switch {
						case c != "" && commentErrRe.MatchString(c):
							addExample(b, s.line, carried, st, nil, nil, true, "", "")
						case c != "":
							// Documented as working, with no result shown.
						case len(queries) == 1:
							addExample(b, s.line, setup, st, nil, nil, true, "", "")
						}
					}
					if len(queries) == 0 && bad {
						addExample(b, s.line, setup, last, nil, nil, true, "", "")
					}
					continue
				}
				// A fixture definition is not paired with the next table:
				// the docs show each fixture's contents separately.
				if isQueryStatement(last) && !cteFixtureRe.MatchString(last) {
					pending = &pendingSQL{blk: b, sql: last, setup: setup}
				}
				continue
			}
			if len(stmts) == 0 {
				continue
			}
			last := stmts[len(stmts)-1]
			setup := append(append([]string{}, carried...), stmts[:len(stmts)-1]...)
			for _, st := range stmts {
				registerFixture(&fixtures, st, b.line+s.line)
			}
			if s.kind == segError {
				addExample(b, s.line, setup, last, nil, nil, true, s.errText, "")
			} else {
				cols, rows, skip := parseResultTable(s.table)
				if bad && skip == "" {
					skip = "block is marked {.bad} but carries a result table"
				}
				addExample(b, s.line, setup, last, cols, rows, false, "", skip)
			}
			// DDL/DML in this segment stays visible to later segments.
			for _, st := range stmts {
				if !isQueryStatement(st) {
					carried = append(carried, st)
				}
			}
		}
	}

	// Attach page fixtures, classify, and number.
	for i := range out {
		ex := &out[i]
		ex.ID = fmt.Sprintf("%s#%d", page, i+1)
		ex.ownSetup = append([]string{}, ex.Setup...)
		attachFixtures(ex, fixtures)
		if ex.Skip == "" && bareCTERe.MatchString(stripSQLComments(ex.SQL)) {
			ex.Skip = "fixture definition without a query"
		}
		if ex.Skip == "" {
			ex.Skip = skipReason(ex)
		}
	}
	return out, fixtures
}

// addAltSetups lists alternative fixture definitions for every fixture
// name the example references: other definitions on the same page
// first, then definitions on other pages.
func addAltSetups(ex *docExample, pageFx, global []fixture) {
	code := stripSQLStrings(stripSQLComments(strings.Join(append(append([]string{}, ex.ownSetup...), ex.SQL), "\n")))
	chosen := map[string][]string{}
	var names []string
	for _, n := range ex.Fixtures {
		f := pickFixture(pageFx, n, ex.Line)
		if f != nil {
			chosen[strings.ToLower(n)] = f.stmts
			names = append(names, n)
		}
	}
	// Names only defined on other pages.
	for _, n := range fixtureNames(global) {
		ln := strings.ToLower(n)
		if _, ok := chosen[ln]; ok || !referencesTable(code, n) {
			continue
		}
		names = append(names, n)
		chosen[ln] = nil
	}
	seen := map[string]bool{strings.Join(ex.Setup, "\x00"): true}
	for _, n := range names {
		ln := strings.ToLower(n)
		var cands []fixture
		for _, f := range pageFx {
			if strings.EqualFold(f.name, n) && f.line != ex.Line {
				cands = append(cands, f)
			}
		}
		for _, f := range global {
			if strings.EqualFold(f.name, n) && f.page != ex.Page {
				cands = append(cands, f)
			}
		}
		for _, c := range cands {
			var setup []string
			for _, m := range names {
				lm := strings.ToLower(m)
				if lm == ln {
					setup = append(setup, c.stmts...)
				} else {
					setup = append(setup, chosen[lm]...)
				}
			}
			setup = append(setup, ex.ownSetup...)
			key := strings.Join(setup, "\x00")
			if seen[key] || len(ex.AltSetups) >= 8 {
				continue
			}
			seen[key] = true
			ex.AltSetups = append(ex.AltSetups, setup)
		}
	}
}

// referencesTable reports whether code uses name as a table (after
// FROM, JOIN, or a comma in a FROM list) without defining it locally.
func referencesTable(code, name string) bool {
	q := regexp.QuoteMeta(name)
	if !regexp.MustCompile(`(?i)\b(FROM|JOIN)\s+` + q + `\b`).MatchString(code) {
		return false
	}
	return !regexp.MustCompile(`(?i)(\bWITH\s+|,\s*)` + q + `\s+AS\s*\(|\bTABLE\s+(IF\s+NOT\s+EXISTS\s+)?` + q + `\b`).MatchString(code)
}

// splitOnCommentedParagraphs splits a semicolon-free block such as
//
//	-- one year
//	INTERVAL '1' YEAR
//
//	-- Produces an error ...
//	SELECT INTERVAL 'x' YEAR
//
// into its comment-led paragraphs. Blocks without that shape are kept.
func splitOnCommentedParagraphs(st string) []string {
	paras := regexp.MustCompile(`\n\s*\n`).Split(st, -1)
	if len(paras) < 2 {
		return []string{st}
	}
	for _, p := range paras {
		if !strings.HasPrefix(strings.TrimSpace(p), "--") {
			return []string{st}
		}
	}
	var out []string
	for _, p := range paras {
		out = appendStmt(out, p)
	}
	return out
}

var errorPrefixRe = regexp.MustCompile(`(?i)^--s*errorb`)

var commentErrRe = regexp.MustCompile(`error|doesn't work|does not work|invalid|isn't valid|not valid|fails|not allowed|not supported`)

// leadingComment returns the `--` comment lines that open a statement.
func leadingComment(st string) string {
	var out []string
	for _, l := range strings.Split(st, "\n") {
		t := strings.TrimSpace(l)
		if !strings.HasPrefix(t, "--") {
			break
		}
		out = append(out, strings.TrimSpace(strings.TrimPrefix(t, "--")))
	}
	return strings.Join(out, " ")
}

func isTableStart(s string) bool {
	return strings.HasPrefix(s, "/*-") || strings.HasPrefix(s, "/*+") || isBareTableBorder(s)
}

// isBareTableBorder matches a result table drawn without the comment
// wrapper, e.g. "+--------+".
func isBareTableBorder(s string) bool {
	return len(s) >= 3 && s[0] == '+' && s[1] == '-' && strings.Trim(s, "+-") == ""
}

// registerFixture records a statement that defines a table other
// examples on the page may reference.
func registerFixture(fx *[]fixture, stmt string, line int) {
	if m := cteFixtureRe.FindStringSubmatch(stmt); m != nil && strings.EqualFold(m[1], m[3]) && balanced(m[2]) {
		*fx = append(*fx, fixture{
			name:  m[1],
			stmts: []string{fmt.Sprintf("CREATE TABLE %s AS %s", m[1], strings.TrimSpace(m[2]))},
			line:  line,
		})
		return
	}
	if m := bareCTERe.FindStringSubmatch(stripSQLComments(stmt)); m != nil && balanced(m[2]) {
		*fx = append(*fx, fixture{
			name:  m[1],
			stmts: []string{fmt.Sprintf("CREATE TABLE %s AS %s", m[1], strings.TrimSpace(m[2]))},
			line:  line,
		})
		return
	}
	if m := createTableRe.FindStringSubmatch(stmt); m != nil {
		*fx = append(*fx, fixture{name: m[1], stmts: []string{stmt}, line: line})
		return
	}
	if m := createFuncRe.FindStringSubmatch(stripSQLComments(stmt)); m != nil {
		// A function defined in one block and called from a later one.
		*fx = append(*fx, fixture{name: m[1], stmts: []string{stmt}, line: line})
		return
	}
	if m := insertRe.FindStringSubmatch(stmt); m != nil {
		// Attach to the most recent definition of the same table.
		for i := len(*fx) - 1; i >= 0; i-- {
			if strings.EqualFold((*fx)[i].name, m[1]) {
				(*fx)[i].stmts = append((*fx)[i].stmts, stmt)
				return
			}
		}
	}
}

func balanced(s string) bool {
	depth := 0
	for _, r := range stripSQLStrings(s) {
		switch r {
		case '(':
			depth++
		case ')':
			depth--
			if depth < 0 {
				return false
			}
		}
	}
	return depth == 0
}

// attachFixtures prepends the page fixtures an example references but
// does not define itself. The nearest preceding definition wins; a
// definition later on the page is used when none precedes.
func attachFixtures(ex *docExample, fx []fixture) {
	text := strings.Join(append(append([]string{}, ex.Setup...), ex.SQL), "\n")
	code := stripSQLStrings(stripSQLComments(text))
	seen := map[string]bool{}
	var pre []string
	for _, name := range fixtureNames(fx) {
		lname := strings.ToLower(name)
		if seen[lname] {
			continue
		}
		wordRe := regexp.MustCompile(`(?i)(^|[^\w.])` + regexp.QuoteMeta(name) + `($|[^\w])`)
		if !wordRe.MatchString(code) {
			continue
		}
		// Defined locally as a CTE, table, or alias target.
		localRe := regexp.MustCompile(`(?i)(\bWITH\s+|,\s*)` + regexp.QuoteMeta(name) + `\s+AS\s*\(|\bTABLE\s+(IF\s+NOT\s+EXISTS\s+)?` + regexp.QuoteMeta(name) + `\b`)
		if localRe.MatchString(code) {
			continue
		}
		f := pickFixture(fx, name, ex.Line)
		if f == nil {
			continue
		}
		// A fixture whose own definition is this very example is not a
		// dependency.
		if f.line == ex.Line {
			continue
		}
		seen[lname] = true
		pre = append(pre, f.stmts...)
		ex.Fixtures = append(ex.Fixtures, f.name)
	}
	if len(pre) > 0 {
		ex.Setup = append(pre, ex.Setup...)
	}
}

func fixtureNames(fx []fixture) []string {
	var names []string
	seen := map[string]bool{}
	for _, f := range fx {
		if !seen[strings.ToLower(f.name)] {
			seen[strings.ToLower(f.name)] = true
			names = append(names, f.name)
		}
	}
	return names
}

func pickFixture(fx []fixture, name string, line int) *fixture {
	var best *fixture
	for i := range fx {
		f := &fx[i]
		if !strings.EqualFold(f.name, name) {
			continue
		}
		if f.line <= line {
			best = f
		}
	}
	if best != nil {
		return best
	}
	for i := range fx {
		if strings.EqualFold(fx[i].name, name) {
			return &fx[i]
		}
	}
	return nil
}

// ---------------------------------------------------------------------
// Block segmentation

type segKind int

const (
	segTable segKind = iota
	segError
	segTrailing
)

type segment struct {
	kind    segKind
	sql     string
	table   string
	errText string
	line    int // 0-based line offset of the segment start in the block
}

// segmentBlock cuts a code block at each result marker: an ASCII
// result table comment or a `-- Error:` comment. The SQL before a
// marker belongs to that marker. Text after the last marker is a
// trailing segment.
func segmentBlock(body string) []segment {
	lines := strings.Split(body, "\n")
	var segs []segment
	var buf []string
	start := 0
	for i := 0; i < len(lines); i++ {
		t := strings.TrimSpace(lines[i])
		if isTableStart(t) && !insideOpenString(strings.Join(buf, "\n")) {
			j := i
			if isBareTableBorder(t) {
				// A bare table ends at its last "|" or "+" line.
				for j+1 < len(lines) {
					n := strings.TrimSpace(lines[j+1])
					if !strings.HasPrefix(n, "|") && !strings.HasPrefix(n, "+") {
						break
					}
					j++
				}
			} else {
				for j < len(lines) && !strings.Contains(lines[j], "*/") {
					j++
				}
			}
			segs = append(segs, segment{kind: segTable, sql: strings.Join(buf, "\n"), table: strings.Join(lines[i:min(j+1, len(lines))], "\n"), line: start})
			buf = nil
			i = j
			start = j + 1
			continue
		}
		if m := errorMarkerRe.FindStringSubmatch(t); m != nil && strings.TrimSpace(stripSQLComments(strings.Join(buf, "\n"))) != "" && !isLeadingComment(lines, i, buf) {
			msg := strings.TrimSpace(m[1])
			// The message may wrap onto following comment lines.
			for i+1 < len(lines) {
				n := strings.TrimSpace(lines[i+1])
				if !strings.HasPrefix(n, "--") || errorMarkerRe.MatchString(n) {
					break
				}
				msg += " " + strings.TrimSpace(strings.TrimPrefix(n, "--"))
				i++
			}
			segs = append(segs, segment{kind: segError, sql: strings.Join(buf, "\n"), errText: msg, line: start})
			buf = nil
			start = i + 1
			continue
		}
		buf = append(buf, lines[i])
	}
	if strings.TrimSpace(strings.Join(buf, "\n")) != "" {
		segs = append(segs, segment{kind: segTrailing, sql: strings.Join(buf, "\n"), line: start})
	}
	return segs
}

// isLeadingComment reports whether the comment at lines[i] introduces
// the next statement ("-- This produces an error ...\nSELECT ...")
// rather than annotating the previous one: the previous statement is
// already terminated and SQL follows the comment directly.
func isLeadingComment(lines []string, i int, buf []string) bool {
	if errorPrefixRe.MatchString(strings.TrimSpace(lines[i])) {
		// "-- Error: ..." always annotates the statement above it.
		return false
	}
	prev := strings.TrimSpace(stripSQLComments(strings.Join(buf, "\n")))
	afterBlank := i > 0 && strings.TrimSpace(lines[i-1]) == ""
	if !strings.HasSuffix(prev, ";") && !afterBlank {
		return false
	}
	for j := i + 1; j < len(lines); j++ {
		n := strings.TrimSpace(lines[j])
		if strings.HasPrefix(n, "--") {
			continue
		}
		return n != "" && !isTableStart(n)
	}
	return false
}

func insideOpenString(s string) bool {
	st := newScanner(s)
	for st.next() {
	}
	return st.inString
}

// ---------------------------------------------------------------------
// Result tables

// parseResultTable parses an ASCII result table. A non-empty skip
// reason is returned when the table cannot be mapped to rows reliably.
func parseResultTable(t string) (cols []string, rows [][]string, skip string) {
	lines := strings.Split(t, "\n")
	var data [][]string
	sawEmpty := false
	for _, line := range lines {
		l := strings.TrimSpace(line)
		l = strings.TrimPrefix(l, "/*")
		l = strings.TrimSuffix(l, "*/")
		l = strings.TrimSpace(l)
		if l == "" {
			continue
		}
		if strings.HasPrefix(l, "|") && strings.HasSuffix(l, "|") && len(l) >= 2 {
			inner := l[1 : len(l)-1]
			if strings.Trim(inner, "-+=| ") == "" && strings.ContainsAny(inner, "-=") {
				continue
			}
			data = append(data, splitCells(inner))
			continue
		}
		if strings.Trim(l, "-+= ") == "" {
			continue
		}
		low := strings.ToLower(l)
		if strings.Contains(low, "empty") || strings.Contains(low, "no rows") {
			sawEmpty = true
			continue
		}
		// A line that is part of a cell continuing past the pipe box.
		return nil, nil, "result table has a line outside the pipe grid"
	}
	if len(data) == 0 {
		return nil, nil, "result table has no header"
	}
	cols = data[0]
	for _, r := range data[1:] {
		if len(r) != len(cols) {
			return cols, nil, fmt.Sprintf("result row has %d cells, header has %d (multi-line or pipe-containing cell)", len(r), len(cols))
		}
		for _, c := range r {
			if c == "..." || strings.HasSuffix(c, "...") {
				return cols, nil, "result table elides values with ..."
			}
		}
		rows = append(rows, r)
	}
	_ = sawEmpty
	return cols, rows, ""
}

// splitCells splits a table row on `|`, keeping pipes that sit inside
// quoted strings or brackets.
func splitCells(inner string) []string {
	var cells []string
	var cur strings.Builder
	depth := 0
	var quote rune
	for _, r := range inner {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			}
		case r == '"' || r == '\'':
			quote = r
		case r == '[' || r == '{' || r == '(':
			depth++
		case r == ']' || r == '}' || r == ')':
			if depth > 0 {
				depth--
			}
		case r == '|' && depth == 0:
			cells = append(cells, strings.TrimSpace(cur.String()))
			cur.Reset()
			continue
		}
		cur.WriteRune(r)
	}
	cells = append(cells, strings.TrimSpace(cur.String()))
	return cells
}

// ---------------------------------------------------------------------
// Classification

var nondetRules = []struct {
	re     *regexp.Regexp
	reason string
}{
	{regexp.MustCompile(`(?i)\bRAND\s*\(`), "RAND() is non-deterministic"},
	{regexp.MustCompile(`(?i)\bCURRENT_(DATE|TIME|TIMESTAMP|DATETIME)\b`), "CURRENT_* depends on the clock"},
	{regexp.MustCompile(`(?i)\b(GENERATE_UUID|NEW_UUID)\s*\(`), "UUID generation is non-deterministic"},
	{regexp.MustCompile(`(?i)\bSESSION_USER\s*\(`), "SESSION_USER depends on the caller"},
	{regexp.MustCompile(`(?i)\bANY_VALUE\s*\(`), "ANY_VALUE picks an arbitrary row"},
	{regexp.MustCompile(`(?i)\bTABLESAMPLE\b`), "TABLESAMPLE is random"},
	{regexp.MustCompile(`(?i)\bDIFFERENTIAL_PRIVACY\b|\bANON_\w+\s*\(`), "differential privacy adds noise"},
	{regexp.MustCompile(`(?i)\bKLL_QUANTILES\.|\bKLL\w*\(`), "KLL sketches are approximate"},
}

// skipReason decides whether an extracted example can be checked
// deterministically.
func skipReason(ex *docExample) string {
	all := strings.Join(append(append([]string{}, ex.Setup...), ex.SQL), ";\n")
	code := stripSQLStrings(stripSQLComments(all))
	for _, st := range append(append([]string{}, ex.Setup...), ex.SQL) {
		if scriptRe.MatchString(stripSQLComments(st)) {
			return "procedural script statement"
		}
	}
	if paramRe.MatchString(code) {
		return "uses query parameters"
	}
	for _, r := range nondetRules {
		if r.re.MatchString(code) {
			return r.reason
		}
	}
	if aggWithoutOrder(code, "ARRAY_AGG") {
		return "ARRAY_AGG without ORDER BY has unspecified element order"
	}
	if aggWithoutOrder(code, "STRING_AGG") {
		return "STRING_AGG without ORDER BY has unspecified element order"
	}
	if aggWithoutOrder(code, "ARRAY_CONCAT_AGG") {
		return "ARRAY_CONCAT_AGG without ORDER BY has unspecified element order"
	}
	if regexp.MustCompile(`(?i)\bAPPROX_\w+\s*\(`).MatchString(code) && largeInput(code) {
		return "APPROX_* over a large input is approximate"
	}
	if limitWithoutOrder(code) {
		return "LIMIT without ORDER BY picks arbitrary rows"
	}
	if !ex.ExpectError && ex.Columns == nil {
		return "no result table"
	}
	if !isQueryStatement(ex.SQL) && !ex.ExpectError {
		return "final statement is not a query"
	}
	return ""
}

var genArrayRe = regexp.MustCompile(`(?i)GENERATE_ARRAY\s*\(\s*(-?\d+)\s*,\s*(-?\d+)`)

func largeInput(code string) bool {
	for _, m := range genArrayRe.FindAllStringSubmatch(code, -1) {
		a, _ := strconv.Atoi(m[1])
		b, _ := strconv.Atoi(m[2])
		if b-a > 1000 {
			return true
		}
	}
	return false
}

// aggWithoutOrder reports whether any call to fn lacks an ORDER BY in
// its argument list.
func aggWithoutOrder(code, fn string) bool {
	re := regexp.MustCompile(`(?i)\b` + fn + `\s*\(`)
	for _, loc := range re.FindAllStringIndex(code, -1) {
		args := parenBody(code, loc[1]-1)
		if !regexp.MustCompile(`(?i)\bORDER\s+BY\b`).MatchString(args) {
			return true
		}
	}
	return false
}

// parenBody returns the text inside the parenthesis opening at open.
func parenBody(s string, open int) string {
	depth := 0
	for i := open; i < len(s); i++ {
		switch s[i] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return s[open+1 : i]
			}
		}
	}
	return s[open+1:]
}

var limitRe = regexp.MustCompile(`(?i)\bLIMIT\s+\d+`)

func limitWithoutOrder(code string) bool {
	if !limitRe.MatchString(code) {
		return false
	}
	return !regexp.MustCompile(`(?i)\bORDER\s+BY\b`).MatchString(code)
}

var queryStartRe = regexp.MustCompile(`(?is)^\s*(\(\s*)*(SELECT|WITH|FROM|VALUES|TABLE\s|GRAPH\s|MATCH\s)`)

func isQueryStatement(s string) bool {
	return queryStartRe.MatchString(stripSQLComments(s))
}

// hasTopLevelOrderBy reports whether the outermost query orders its
// result: an ORDER BY at parenthesis depth zero (including a pipe
// `|> ORDER BY`).
func hasTopLevelOrderBy(sql string) bool {
	code := stripSQLStrings(stripSQLComments(sql))
	depth := 0
	up := strings.ToUpper(code)
	for i := 0; i < len(up); i++ {
		switch up[i] {
		case '(':
			depth++
		case ')':
			depth--
		case 'O':
			if depth == 0 && strings.HasPrefix(up[i:], "ORDER") && (i == 0 || !isWordByte(up[i-1])) {
				rest := strings.TrimLeft(up[i+5:], " \t\n")
				if strings.HasPrefix(rest, "BY") {
					return true
				}
			}
		}
	}
	return false
}

func isWordByte(b byte) bool {
	return b == '_' || (b >= 'A' && b <= 'Z') || (b >= 'a' && b <= 'z') || (b >= '0' && b <= '9')
}

// ---------------------------------------------------------------------
// Lexical helpers

// scanner walks SQL text tracking strings and comments.
type scanner struct {
	s         string
	i         int
	inString  bool
	quote     string
	inLine    bool
	inBlock   bool
	cur       byte
	isCode    bool // cur is outside strings and comments
	stringEnd bool
}

func newScanner(s string) *scanner { return &scanner{s: s, i: -1} }

func (sc *scanner) next() bool {
	sc.i++
	if sc.i >= len(sc.s) {
		return false
	}
	c := sc.s[sc.i]
	sc.cur = c
	sc.isCode = false
	switch {
	case sc.inLine:
		if c == '\n' {
			sc.inLine = false
			sc.isCode = true
		}
	case sc.inBlock:
		if c == '*' && sc.i+1 < len(sc.s) && sc.s[sc.i+1] == '/' {
			sc.inBlock = false
			sc.i++
		}
	case sc.inString:
		if c == '\\' && sc.quote != "`" && sc.i+1 < len(sc.s) {
			sc.i++
			return true
		}
		if strings.HasPrefix(sc.s[sc.i:], sc.quote) {
			sc.i += len(sc.quote) - 1
			sc.inString = false
		}
	default:
		switch {
		case c == '-' && sc.i+1 < len(sc.s) && sc.s[sc.i+1] == '-':
			sc.inLine = true
		case c == '#':
			sc.inLine = true
		case c == '/' && sc.i+1 < len(sc.s) && sc.s[sc.i+1] == '*':
			sc.inBlock = true
			sc.i++
		case strings.HasPrefix(sc.s[sc.i:], `"""`) || strings.HasPrefix(sc.s[sc.i:], `'''`):
			sc.inString = true
			sc.quote = sc.s[sc.i : sc.i+3]
			sc.i += 2
		case c == '"' || c == '\'' || c == '`':
			sc.inString = true
			sc.quote = string(c)
		default:
			sc.isCode = true
		}
	}
	return true
}

// stripSQLComments removes -- , # and /* */ comments, keeping strings.
func stripSQLComments(s string) string {
	var b strings.Builder
	sc := newScanner(s)
	prevI := 0
	for sc.next() {
		if sc.isCode || sc.inString || (!sc.inLine && !sc.inBlock && !sc.isCode && sc.i < len(sc.s) && prevWasString(sc)) {
			b.WriteString(sc.s[prevI : sc.i+1])
		} else if sc.cur == '\n' {
			b.WriteByte('\n')
		}
		prevI = sc.i + 1
	}
	return b.String()
}

// prevWasString is true right after a string literal closed on this
// step (the closing quote is not code, but must be kept).
func prevWasString(sc *scanner) bool {
	c := sc.s[sc.i]
	return c == '"' || c == '\'' || c == '`'
}

// stripSQLStrings replaces the contents of string literals with
// spaces, leaving empty quotes, so keyword scans do not match inside
// literals.
func stripSQLStrings(s string) string {
	b := []byte(s)
	sc := newScanner(s)
	for sc.next() {
		if sc.inString {
			start := sc.i + 1
			for sc.next() && sc.inString {
			}
			end := sc.i - len(sc.quote) + 1
			for k := start; k < end && k < len(b); k++ {
				if b[k] != '\n' {
					b[k] = ' '
				}
			}
		}
	}
	return string(b)
}

// splitStatements splits on top-level semicolons, dropping empty and
// comment-only statements.
func splitStatements(s string) []string {
	var out []string
	sc := newScanner(s)
	last := 0
	for sc.next() {
		if sc.isCode && sc.cur == ';' {
			out = appendStmt(out, s[last:sc.i])
			last = sc.i + 1
		}
	}
	out = appendStmt(out, s[last:])
	return out
}

func appendStmt(out []string, st string) []string {
	if strings.TrimSpace(stripSQLComments(st)) == "" {
		return out
	}
	return append(out, strings.TrimSpace(st))
}
