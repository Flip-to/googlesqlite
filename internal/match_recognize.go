package internal

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/goccy/go-googlesql"

	"github.com/goccy/googlesqlite/internal/value"
)

// MATCH_RECOGNIZE lowering.
//
// A ResolvedMatchRecognizeScan is formatted as four nested stages:
//
//  1. the input scan, extended with the analytic columns that back
//     PREV / NEXT inside DEFINE (formatAnalyticScan);
//  2. a window call googlesqlite_match_recognize(spec, longest,
//     bounds..., define predicates...) OVER (PARTITION BY ... ORDER BY
//     ... ROWS BETWEEN CURRENT ROW AND UNBOUNDED FOLLOWING). The Go
//     window function buffers the partition's predicate bits, runs the
//     row pattern once, and returns for each row a small JSON list of
//     [partition_id, match_number, match_row_number, variable] entries
//     (one per match the row belongs to; an empty match is attached to
//     its start row with match_row_number 0);
//  3. json_each expands every row into one row per entry, exposing
//     the $match_number / $match_row_number / $classifier columns;
//  4. a GROUP BY (partition_id, match_number) computes the measure
//     aggregates. Each aggregate carries a FILTER clause that drops the
//     empty-match placeholder row and, for a measure group bound to a
//     pattern variable (`MAX(a.z)`), rows classified otherwise.

const (
	mrResColumn = "__mr_res"
	mrPidColumn = "__mr_pid"
	mrVarColumn = "__mr_var"
)

// mrPatternNode is the JSON-serialised row pattern handed to the
// googlesqlite_match_recognize window function.
type mrPatternNode struct {
	T  string           `json:"t"` // v, cat, alt, q, start, end, empty
	V  int              `json:"v,omitempty"`
	C  []*mrPatternNode `json:"c,omitempty"`
	Lo int              `json:"lo,omitempty"` // bound argument index + 1; 0 = absent
	Hi int              `json:"hi,omitempty"` // bound argument index + 1; 0 = absent
	R  bool             `json:"r,omitempty"`
}

type mrSpec struct {
	P    *mrPatternNode `json:"p"`
	Skip int            `json:"s"` // 0: SKIP PAST LAST ROW, 1: SKIP TO NEXT ROW
	NB   int            `json:"nb"`
	NV   int            `json:"nv"`
}

type MatchRecognizeScanNode struct {
	node *googlesql.ResolvedMatchRecognizeScan
}

func newMatchRecognizeScanNode(n *googlesql.ResolvedMatchRecognizeScan) *MatchRecognizeScanNode {
	return &MatchRecognizeScanNode{node: n}
}

type mrPatternBuilder struct {
	ctx      context.Context
	varIndex map[string]int
	bounds   []string
}

func (b *mrPatternBuilder) bound(expr googlesql.ResolvedExprNode) (int, error) {
	if expr == nil {
		return 0, nil
	}
	sql, err := newNode(expr).FormatSQL(b.ctx)
	if err != nil {
		return 0, err
	}
	b.bounds = append(b.bounds, sql)
	return len(b.bounds), nil
}

func (b *mrPatternBuilder) build(p googlesql.ResolvedMatchRecognizePatternExprNode) (*mrPatternNode, error) {
	switch p := p.(type) {
	case *googlesql.ResolvedMatchRecognizePatternVariableRef:
		name := strings.ToLower(m1(p.Name()))
		idx, ok := b.varIndex[name]
		if !ok {
			return nil, fmt.Errorf("MATCH_RECOGNIZE: undefined pattern variable %q", m1(p.Name()))
		}
		return &mrPatternNode{T: "v", V: idx}, nil
	case *googlesql.ResolvedMatchRecognizePatternEmpty:
		return &mrPatternNode{T: "empty"}, nil
	case *googlesql.ResolvedMatchRecognizePatternAnchor:
		if m1(p.Mode()) == googlesql.ResolvedMatchRecognizePatternAnchorEnums_ModeEnd {
			return &mrPatternNode{T: "end"}, nil
		}
		return &mrPatternNode{T: "start"}, nil
	case *googlesql.ResolvedMatchRecognizePatternOperation:
		out := &mrPatternNode{T: "cat"}
		if m1(p.OpType()) == googlesql.ResolvedMatchRecognizePatternOperationEnums_MatchRecognizePatternOperationTypeAlternate {
			out.T = "alt"
		}
		for _, op := range m1(p.OperandList()) {
			c, err := b.build(op)
			if err != nil {
				return nil, err
			}
			out.C = append(out.C, c)
		}
		return out, nil
	case *googlesql.ResolvedMatchRecognizePatternQuantification:
		body, err := b.build(m1(p.Operand()))
		if err != nil {
			return nil, err
		}
		lo, err := b.bound(m1(p.LowerBound()))
		if err != nil {
			return nil, err
		}
		hi, err := b.bound(m1(p.UpperBound()))
		if err != nil {
			return nil, err
		}
		return &mrPatternNode{T: "q", C: []*mrPatternNode{body}, Lo: lo, Hi: hi, R: m1(p.IsReluctant())}, nil
	}
	return nil, fmt.Errorf("MATCH_RECOGNIZE: unsupported pattern node %T", p)
}

// mrAppendFilter appends `FILTER (WHERE cond)` to a formatted aggregate
// call. Only a plain `name(args)` call can carry the clause.
func mrAppendFilter(agg, cond string) (string, error) {
	s := strings.TrimSpace(agg)
	open := strings.IndexByte(s, '(')
	if open <= 0 || !strings.HasSuffix(s, ")") {
		return "", fmt.Errorf("MATCH_RECOGNIZE: unsupported measure aggregate form %q", agg)
	}
	for _, r := range s[:open] {
		if !(r == '_' || r == '.' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9') {
			return "", fmt.Errorf("MATCH_RECOGNIZE: unsupported measure aggregate form %q", agg)
		}
	}
	// The opening parenthesis must close at the very end.
	depth := 0
	inStr := byte(0)
	for i := open; i < len(s); i++ {
		c := s[i]
		if inStr != 0 {
			if c == inStr {
				inStr = 0
			}
			continue
		}
		switch c {
		case '\'', '"', '`':
			inStr = c
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 && i != len(s)-1 {
				return "", fmt.Errorf("MATCH_RECOGNIZE: unsupported measure aggregate form %q", agg)
			}
		}
	}
	return fmt.Sprintf("%s FILTER (WHERE %s)", s, cond), nil
}

func (n *MatchRecognizeScanNode) FormatSQL(ctx context.Context) (string, error) {
	if n.node == nil {
		return "", nil
	}
	inputScan := m1(n.node.InputScan())

	// Stage 1: input plus analytic (PREV / NEXT) columns.
	var stage1 string
	var err error
	if groups := m1(n.node.AnalyticFunctionGroupList()); len(groups) != 0 {
		cols := append([]*googlesql.ResolvedColumn(nil), scanColumnList(inputScan)...)
		for _, g := range groups {
			for _, fn := range m1(g.AnalyticFunctionList()) {
				cols = append(cols, m1(fn.Column()))
			}
		}
		stage1, err = formatAnalyticScan(ctx, inputScan, groups, cols)
	} else {
		stage1, err = newNode(inputScan).FormatSQL(ctx)
	}
	if err != nil {
		return "", err
	}
	from1, err := formatInput(stage1)
	if err != nil {
		return "", err
	}

	// Stage 2: pattern matching window.
	defs := m1(n.node.PatternVariableDefinitionList())
	b := &mrPatternBuilder{ctx: ctx, varIndex: map[string]int{}}
	varNames := make([]string, len(defs))
	predicates := make([]string, len(defs))
	for i, def := range defs {
		name := m1(def.Name())
		varNames[i] = name
		b.varIndex[strings.ToLower(name)] = i
		pred, err := newNode(m1(def.Predicate())).FormatSQL(ctx)
		if err != nil {
			return "", err
		}
		predicates[i] = pred
	}
	pattern, err := b.build(m1(n.node.Pattern()))
	if err != nil {
		return "", err
	}
	spec := mrSpec{P: pattern, NB: len(b.bounds), NV: len(defs)}
	if m1(n.node.AfterMatchSkipMode()) == googlesql.ResolvedMatchRecognizeScanEnums_AfterMatchSkipModeNextRow {
		spec.Skip = 1
	}
	specJSON, err := json.Marshal(spec)
	if err != nil {
		return "", err
	}
	longest := "false"
	for _, opt := range m1(n.node.OptionList()) {
		if strings.EqualFold(m1(opt.Name()), "use_longest_match") {
			longest, err = newNode(m1(opt.Value())).FormatSQL(ctx)
			if err != nil {
				return "", err
			}
		}
	}
	var partitionKeys []string
	if pb := m1(n.node.PartitionBy()); pb != nil {
		collations := m1(pb.CollationList())
		for i, ref := range m1(pb.PartitionByList()) {
			col := m1(ref.Column())
			key := fmt.Sprintf("`%s`", uniqueColumnName(ctx, col))
			if isIntervalType(col.Type()) {
				key = fmt.Sprintf("googlesqlite_group_by(%s)", key)
			}
			if i < len(collations) {
				if spec := collationName(collations[i]); spec != "" {
					key = collationKeySQL(key, spec)
				}
			}
			partitionKeys = append(partitionKeys, key)
		}
	}
	var orderKeys []string
	if ob := m1(n.node.OrderBy()); ob != nil {
		for _, item := range m1(ob.OrderByItemList()) {
			key := fmt.Sprintf("`%s`", uniqueColumnName(ctx, m1(m1(item.ColumnRef()).Column())))
			if spec := collationName(m1(item.Collation())); spec != "" {
				key = collationKeySQL(key, spec)
			}
			switch m1(item.NullOrder()) {
			case googlesql.ResolvedOrderByItemEnums_NullOrderModeNullsFirst:
				orderKeys = append(orderKeys, fmt.Sprintf("(%s IS NOT NULL)", key))
			case googlesql.ResolvedOrderByItemEnums_NullOrderModeNullsLast:
				orderKeys = append(orderKeys, fmt.Sprintf("(%s IS NULL)", key))
			}
			dir := ""
			if m1(item.IsDescending()) {
				dir = " DESC"
			}
			orderKeys = append(orderKeys, fmt.Sprintf("%s COLLATE googlesqlite_collate%s", key, dir))
		}
	}
	var over []string
	if len(partitionKeys) != 0 {
		over = append(over, "PARTITION BY "+strings.Join(partitionKeys, ","))
	}
	if len(orderKeys) != 0 {
		over = append(over, "ORDER BY "+strings.Join(orderKeys, ","))
	}
	over = append(over, "ROWS BETWEEN CURRENT ROW AND UNBOUNDED FOLLOWING")
	callArgs := []string{sqlStringLiteral(string(specJSON)), longest}
	callArgs = append(callArgs, b.bounds...)
	callArgs = append(callArgs, predicates...)
	stage2 := fmt.Sprintf(
		"SELECT *, googlesqlite_match_recognize(%s) OVER (%s) AS `%s` %s",
		strings.Join(callArgs, ","), strings.Join(over, " "), mrResColumn, from1,
	)

	// Stage 3: one row per (row, match) membership.
	mnName := uniqueColumnName(ctx, m1(n.node.MatchNumberColumn()))
	mrnName := uniqueColumnName(ctx, m1(n.node.MatchRowNumberColumn()))
	clsName := uniqueColumnName(ctx, m1(n.node.ClassifierColumn()))
	classifier := "NULL"
	if len(varNames) != 0 {
		var whens []string
		for i, name := range varNames {
			lit, err := literalFromValue(value.StringValue(name))
			if err != nil {
				return "", err
			}
			whens = append(whens, fmt.Sprintf("WHEN %d THEN %s", i, lit))
		}
		classifier = fmt.Sprintf("CASE json_extract(`__mr_j`.value,'$[3]') %s END", strings.Join(whens, " "))
	}
	stage3 := fmt.Sprintf(
		"SELECT `__mr_b`.*, json_extract(`__mr_j`.value,'$[0]') AS `%s`, json_extract(`__mr_j`.value,'$[1]') AS `%s`, json_extract(`__mr_j`.value,'$[2]') AS `%s`, json_extract(`__mr_j`.value,'$[3]') AS `%s`, %s AS `%s` FROM (%s) AS `__mr_b`, json_each(`__mr_b`.`%s`) AS `__mr_j`",
		mrPidColumn, mnName, mrnName, mrVarColumn, classifier, clsName, stage2, mrResColumn,
	)

	// Stage 4: measures.
	aggs := map[string]string{}
	for _, group := range m1(n.node.MeasureGroupList()) {
		cond := fmt.Sprintf("`%s` > 0", mrnName)
		if ref := m1(group.PatternVariableRef()); ref != nil {
			idx, ok := b.varIndex[strings.ToLower(m1(ref.Name()))]
			if !ok {
				return "", fmt.Errorf("MATCH_RECOGNIZE: undefined pattern variable %q", m1(ref.Name()))
			}
			cond += fmt.Sprintf(" AND `%s` = %d", mrVarColumn, idx)
		}
		for _, agg := range m1(group.AggregateList()) {
			sql, err := mrFormatMeasureAggregate(ctx, m1(agg.Expr()), mrnName)
			if err != nil {
				return "", err
			}
			sql, err = mrAppendFilter(sql, cond)
			if err != nil {
				return "", err
			}
			aggs[uniqueColumnName(ctx, m1(agg.Column()))] = sql
		}
	}
	var columns []string
	for _, col := range m1(n.node.ColumnList()) {
		name := uniqueColumnName(ctx, col)
		if sql, ok := aggs[name]; ok {
			columns = append(columns, fmt.Sprintf("%s AS `%s`", sql, name))
			continue
		}
		columns = append(columns, fmt.Sprintf("`%s`", name))
	}
	if len(columns) == 0 {
		columns = append(columns, "NULL")
	}
	return fmt.Sprintf(
		"SELECT %s FROM (%s) GROUP BY `%s`, `%s`",
		strings.Join(columns, ","), stage3, mrPidColumn, mnName,
	), nil
}

// mrFormatMeasureAggregate formats one measure aggregate. The
// MATCH_RECOGNIZE-only FIRST(x) / LAST(x) aggregates return x from the
// first / last row of the match (or of the rows mapped to the
// measure group's variable); they lower to ANY_VALUE(x HAVING MIN /
// MAX match_row_number), which is unique within a match.
func mrFormatMeasureAggregate(ctx context.Context, expr googlesql.ResolvedExprNode, mrnName string) (string, error) {
	if call, ok := expr.(*googlesql.ResolvedAggregateFunctionCall); ok {
		name := strings.ToLower(m1(m1(call.Function()).Name()))
		if name == "first" || name == "last" {
			args := m1(call.ArgumentList())
			if len(args) != 1 {
				return "", fmt.Errorf("MATCH_RECOGNIZE: %s expects one argument", strings.ToUpper(name))
			}
			arg, err := newNode(args[0]).FormatSQL(ctx)
			if err != nil {
				return "", err
			}
			kind := "'MIN'"
			if name == "last" {
				kind = "'MAX'"
			}
			kindLit, err := literalFromValue(value.StringValue(strings.Trim(kind, "'")))
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("googlesqlite_having_any_value(%s,`%s`,%s)", arg, mrnName, kindLit), nil
		}
	}
	return newNode(expr).FormatSQL(ctx)
}

func sqlStringLiteral(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

// ---------------------------------------------------------------------
// Runtime: googlesqlite_match_recognize window function.
// ---------------------------------------------------------------------

const (
	mrOpVar = iota
	mrOpSplit
	mrOpJmp
	mrOpStart
	mrOpEnd
	mrOpMatch
)

// mrMaxProgram bounds the compiled pattern size (counted quantifiers
// are unrolled) so a huge `{n,m}` cannot exhaust memory.
const mrMaxProgram = 1 << 18

type mrInst struct {
	op   uint8
	v    int
	x, y int
}

type mrProgram struct {
	insts []mrInst
	skip  int
	nv    int
	nb    int
}

type mrCompiler struct {
	insts  []mrInst
	bounds []value.Value
}

func (c *mrCompiler) emit(in mrInst) (int, error) {
	if len(c.insts) >= mrMaxProgram {
		return 0, fmt.Errorf("MATCH_RECOGNIZE pattern is too large after expanding quantifier bounds")
	}
	c.insts = append(c.insts, in)
	return len(c.insts) - 1, nil
}

func (c *mrCompiler) boundValue(idx int) (int64, bool, error) {
	if idx == 0 {
		return 0, false, nil
	}
	v := c.bounds[idx-1]
	if v == nil {
		return 0, false, fmt.Errorf("MATCH_RECOGNIZE quantifier bound must not be NULL")
	}
	i, err := v.ToInt64()
	if err != nil {
		return 0, false, err
	}
	if i < 0 {
		return 0, false, fmt.Errorf("MATCH_RECOGNIZE quantifier bound must not be negative: %d", i)
	}
	return i, true, nil
}

func (c *mrCompiler) compile(p *mrPatternNode) error {
	switch p.T {
	case "v":
		_, err := c.emit(mrInst{op: mrOpVar, v: p.V})
		return err
	case "empty":
		return nil
	case "start":
		_, err := c.emit(mrInst{op: mrOpStart})
		return err
	case "end":
		_, err := c.emit(mrInst{op: mrOpEnd})
		return err
	case "cat":
		for _, ch := range p.C {
			if err := c.compile(ch); err != nil {
				return err
			}
		}
		return nil
	case "alt":
		var jumps []int
		for i, ch := range p.C {
			if i == len(p.C)-1 {
				if err := c.compile(ch); err != nil {
					return err
				}
				break
			}
			split, err := c.emit(mrInst{op: mrOpSplit})
			if err != nil {
				return err
			}
			c.insts[split].x = split + 1
			if err := c.compile(ch); err != nil {
				return err
			}
			j, err := c.emit(mrInst{op: mrOpJmp})
			if err != nil {
				return err
			}
			jumps = append(jumps, j)
			c.insts[split].y = len(c.insts)
		}
		for _, j := range jumps {
			c.insts[j].x = len(c.insts)
		}
		return nil
	case "q":
		lo, _, err := c.boundValue(p.Lo)
		if err != nil {
			return err
		}
		hi, hasHi, err := c.boundValue(p.Hi)
		if err != nil {
			return err
		}
		if hasHi && hi < lo {
			return fmt.Errorf("MATCH_RECOGNIZE quantifier upper bound %d is smaller than lower bound %d", hi, lo)
		}
		if lo > mrMaxProgram || hasHi && hi > mrMaxProgram {
			return fmt.Errorf("MATCH_RECOGNIZE pattern is too large after expanding quantifier bounds")
		}
		body := p.C[0]
		for i := int64(0); i < lo; i++ {
			if err := c.compile(body); err != nil {
				return err
			}
		}
		if !hasHi {
			loop, err := c.emit(mrInst{op: mrOpSplit})
			if err != nil {
				return err
			}
			if err := c.compile(body); err != nil {
				return err
			}
			if _, err := c.emit(mrInst{op: mrOpJmp, x: loop}); err != nil {
				return err
			}
			c.setSplit(loop, loop+1, len(c.insts), p.R)
			return nil
		}
		var splits []int
		for i := lo; i < hi; i++ {
			s, err := c.emit(mrInst{op: mrOpSplit})
			if err != nil {
				return err
			}
			splits = append(splits, s)
			if err := c.compile(body); err != nil {
				return err
			}
		}
		for _, s := range splits {
			c.setSplit(s, s+1, len(c.insts), p.R)
		}
		return nil
	}
	return fmt.Errorf("MATCH_RECOGNIZE: unknown pattern node %q", p.T)
}

// setSplit points a split at the loop body and the exit; the greedy
// form prefers the body, the reluctant form prefers the exit.
func (c *mrCompiler) setSplit(at, body, exit int, reluctant bool) {
	if reluctant {
		c.insts[at].x, c.insts[at].y = exit, body
	} else {
		c.insts[at].x, c.insts[at].y = body, exit
	}
}

var (
	mrSpecCache    sync.Map // spec JSON -> *mrSpec
	mrPartitionSeq atomic.Int64
)

func mrParseSpec(s string) (*mrSpec, error) {
	if v, ok := mrSpecCache.Load(s); ok {
		return v.(*mrSpec), nil
	}
	var spec mrSpec
	if err := json.Unmarshal([]byte(s), &spec); err != nil {
		return nil, fmt.Errorf("MATCH_RECOGNIZE: invalid pattern spec: %w", err)
	}
	mrSpecCache.Store(s, &spec)
	return &spec, nil
}

func mrCompile(spec *mrSpec, bounds []value.Value) (*mrProgram, error) {
	c := &mrCompiler{bounds: bounds}
	if err := c.compile(spec.P); err != nil {
		return nil, err
	}
	if _, err := c.emit(mrInst{op: mrOpMatch}); err != nil {
		return nil, err
	}
	return &mrProgram{insts: c.insts, skip: spec.Skip, nv: spec.NV, nb: spec.NB}, nil
}

type mrPath struct {
	prev *mrPath
	v    int32
}

type mrThread struct {
	pc   int
	path *mrPath
}

type mrEntry struct {
	mn, mrn int64
	v       int32
}

// matchRecognizeWindow is the googlesqlite_match_recognize window
// aggregate. It is evaluated with the frame ROWS BETWEEN CURRENT ROW
// AND UNBOUNDED FOLLOWING, so every row of the partition has been
// stepped before the first Done, and the number of Inverse calls
// identifies the current row.
type matchRecognizeWindow struct {
	pid      int64
	prog     *mrProgram
	longest  bool
	bits     []bool // row-major, prog.nv per row
	n        int
	inverses int
	results  [][]mrEntry
	computed bool
}

func newMatchRecognizeWindow() any {
	return &matchRecognizeWindow{pid: mrPartitionSeq.Add(1)}
}

func mrTruthy(v value.Value) (bool, error) {
	if v == nil {
		return false, nil
	}
	return v.ToBool()
}

func (w *matchRecognizeWindow) Step(args ...any) error {
	if len(args) < 2 {
		return fmt.Errorf("googlesqlite_match_recognize: missing arguments")
	}
	if w.prog == nil {
		specText, ok := args[0].(string)
		if !ok {
			return fmt.Errorf("googlesqlite_match_recognize: pattern spec must be text")
		}
		spec, err := mrParseSpec(specText)
		if err != nil {
			return err
		}
		if len(args) != 2+spec.NB+spec.NV {
			return fmt.Errorf("googlesqlite_match_recognize: expected %d arguments, got %d", 2+spec.NB+spec.NV, len(args))
		}
		head, err := value.ConvertArgs(args[1 : 2+spec.NB]...)
		if err != nil {
			return err
		}
		if w.longest, err = mrTruthy(head[0]); err != nil {
			return err
		}
		if w.prog, err = mrCompile(spec, head[1:]); err != nil {
			return err
		}
	}
	preds, err := value.ConvertArgs(args[2+w.prog.nb:]...)
	if err != nil {
		return err
	}
	for _, p := range preds {
		b, err := mrTruthy(p)
		if err != nil {
			return err
		}
		w.bits = append(w.bits, b)
	}
	w.n++
	w.computed = false
	return nil
}

func (w *matchRecognizeWindow) Inverse(_ ...any) error {
	w.inverses++
	return nil
}

func (w *matchRecognizeWindow) Done() (any, error) {
	if w.prog == nil {
		return nil, nil
	}
	if !w.computed {
		w.compute()
		w.computed = true
	}
	idx := w.inverses
	if idx >= len(w.results) || len(w.results[idx]) == 0 {
		return nil, nil
	}
	var sb strings.Builder
	sb.WriteByte('[')
	for i, e := range w.results[idx] {
		if i > 0 {
			sb.WriteByte(',')
		}
		sb.WriteByte('[')
		sb.WriteString(strconv.FormatInt(w.pid, 10))
		sb.WriteByte(',')
		sb.WriteString(strconv.FormatInt(e.mn, 10))
		sb.WriteByte(',')
		sb.WriteString(strconv.FormatInt(e.mrn, 10))
		sb.WriteByte(',')
		sb.WriteString(strconv.FormatInt(int64(e.v), 10))
		sb.WriteByte(']')
	}
	sb.WriteByte(']')
	return sb.String(), nil
}

func (w *matchRecognizeWindow) compute() {
	w.results = make([][]mrEntry, w.n)
	vm := &mrVM{w: w, visited: make([]uint32, len(w.prog.insts))}
	var mn int64
	for s := 0; s < w.n; {
		end, path, ok := vm.run(s)
		if !ok {
			s++
			continue
		}
		mn++
		if end == s {
			w.results[s] = append(w.results[s], mrEntry{mn: mn, mrn: 0, v: -1})
			s++
			continue
		}
		vars := make([]int32, end-s)
		for i, p := len(vars)-1, path; i >= 0; i, p = i-1, p.prev {
			vars[i] = p.v
		}
		for i, v := range vars {
			w.results[s+i] = append(w.results[s+i], mrEntry{mn: mn, mrn: int64(i + 1), v: v})
		}
		if w.prog.skip == 1 {
			s++
		} else {
			s = end
		}
	}
	vm.clist, vm.nlist = nil, nil
}

// mrVM is a Pike VM: threads are kept in priority order and
// deduplicated per position, so a match attempt costs
// O(rows * program) regardless of how ambiguous the pattern is.
type mrVM struct {
	w       *matchRecognizeWindow
	visited []uint32
	gen     uint32
	clist   []mrThread
	nlist   []mrThread
}

func (vm *mrVM) nextGen() {
	vm.gen++
	if vm.gen == 0 {
		for i := range vm.visited {
			vm.visited[i] = 0
		}
		vm.gen = 1
	}
}

func (vm *mrVM) add(list []mrThread, pc int, path *mrPath, pos int) []mrThread {
	if vm.visited[pc] == vm.gen {
		return list
	}
	vm.visited[pc] = vm.gen
	in := vm.w.prog.insts[pc]
	switch in.op {
	case mrOpJmp:
		return vm.add(list, in.x, path, pos)
	case mrOpSplit:
		list = vm.add(list, in.x, path, pos)
		return vm.add(list, in.y, path, pos)
	case mrOpStart:
		if pos == 0 {
			return vm.add(list, pc+1, path, pos)
		}
		return list
	case mrOpEnd:
		if pos == vm.w.n {
			return vm.add(list, pc+1, path, pos)
		}
		return list
	}
	return append(list, mrThread{pc: pc, path: path})
}

func (vm *mrVM) run(start int) (int, *mrPath, bool) {
	w := vm.w
	nv := w.prog.nv
	var (
		bestEnd  int
		bestPath *mrPath
		found    bool
	)
	vm.nextGen()
	vm.clist = vm.add(vm.clist[:0], 0, nil, start)
	for pos := start; len(vm.clist) != 0; pos++ {
		vm.nextGen()
		vm.nlist = vm.nlist[:0]
	threads:
		for _, th := range vm.clist {
			in := w.prog.insts[th.pc]
			switch in.op {
			case mrOpMatch:
				if w.longest {
					if !found || pos > bestEnd {
						bestEnd, bestPath, found = pos, th.path, true
					}
					continue
				}
				bestEnd, bestPath, found = pos, th.path, true
				break threads
			case mrOpVar:
				if pos < w.n && w.bits[pos*nv+in.v] {
					vm.nlist = vm.add(vm.nlist, th.pc+1, &mrPath{prev: th.path, v: int32(in.v)}, pos+1)
				}
			}
		}
		vm.clist, vm.nlist = vm.nlist, vm.clist
	}
	return bestEnd, bestPath, found
}
