package internal

import (
	"context"
	"fmt"
	"strings"

	googlesql "github.com/goccy/go-googlesql"
)

// SQLite restricts a recursive CTE much more than GoogleSQL does: the
// self-reference must appear exactly once, directly in the FROM clause
// of a recursive SELECT (never inside a subquery), and a WITH clause
// may not start a compound-select term. The helpers in this file
// rewrite GoogleSQL recursive queries into that shape
// (with_recursive.test).

// hoistedCTEsKey carries the collector that nested WITH entries found
// inside a recursive CTE body are moved into. The enclosing
// WithScanNode emits them right before the recursive entry.
type hoistedCTEsKey struct{}

func withHoistedCTEs(ctx context.Context, dst *[]string) context.Context {
	return context.WithValue(ctx, hoistedCTEsKey{}, dst)
}

func hoistedCTEs(ctx context.Context) *[]string {
	dst, _ := ctx.Value(hoistedCTEsKey{}).(*[]string)
	return dst
}

// recursiveScanOf returns the ResolvedRecursiveScan a WITH entry body
// evaluates, looking through WITH clauses that wrap the whole
// recursive UNION (`a AS (WITH b AS (...) SELECT ... UNION ALL ...)`).
func recursiveScanOf(node googlesql.ResolvedNode) *googlesql.ResolvedRecursiveScan {
	for node != nil {
		switch n := node.(type) {
		case *googlesql.ResolvedRecursiveScan:
			return n
		case *googlesql.ResolvedWithScan:
			q, _ := n.Query()
			if q == nil {
				return nil
			}
			node = q
		default:
			return nil
		}
	}
	return nil
}

// collectRecursiveRefScans returns every ResolvedRecursiveRefScan in
// the subtree, not descending into nested recursive scans (their
// references belong to the nested CTE).
func collectRecursiveRefScans(node googlesql.ResolvedNode, out *[]*googlesql.ResolvedRecursiveRefScan) {
	if node == nil {
		return
	}
	switch n := node.(type) {
	case *googlesql.ResolvedRecursiveRefScan:
		*out = append(*out, n)
		return
	case *googlesql.ResolvedRecursiveScan:
		return
	}
	children, _ := node.GetChildNodes()
	for _, c := range children {
		collectRecursiveRefScans(c, out)
	}
}

func containsRecursiveRef(node googlesql.ResolvedNode) bool {
	var refs []*googlesql.ResolvedRecursiveRefScan
	collectRecursiveRefScans(node, &refs)
	return len(refs) > 0
}

// flatSelect is a row-wise pipeline (projections and filters) over a
// single recursive reference, collapsed into one SELECT.
type flatSelect struct {
	from  string
	where []string
	subst map[int32]string
}

func mergedSubstCtx(ctx context.Context, subst map[int32]string) context.Context {
	merged := map[int32]string{}
	for k, v := range columnIDSubstitution(ctx) {
		merged[k] = v
	}
	for k, v := range subst {
		merged[k] = v
	}
	return withColumnIDSubstitution(ctx, merged)
}

// flattenRecursivePipeline collapses ProjectScan / FilterScan layers
// over the recursive reference, substituting each computed column by
// its expression, so the reference stays in the top-level FROM clause.
// refSubst maps recursive reference column IDs to the CTE's column
// names. ok is false for any other shape.
func flattenRecursivePipeline(ctx context.Context, node googlesql.ResolvedNode, refSubst map[int32]string, top bool) (*flatSelect, bool, error) {
	switch n := node.(type) {
	case *googlesql.ResolvedRecursiveRefScan:
		subst := map[int32]string{}
		for k, v := range refSubst {
			subst[k] = v
		}
		return &flatSelect{from: fmt.Sprintf("`%s`", recursiveCteName(ctx)), subst: subst}, true, nil
	case *googlesql.ResolvedFilterScan:
		input, _ := n.InputScan()
		fs, ok, err := flattenRecursivePipeline(ctx, input, refSubst, false)
		if !ok || err != nil {
			return nil, ok, err
		}
		filter, _ := n.FilterExpr()
		if filter == nil {
			return nil, false, nil
		}
		cond, err := newNode(filter).FormatSQL(mergedSubstCtx(ctx, fs.subst))
		if err != nil {
			return nil, false, err
		}
		fs.where = append(fs.where, cond)
		return fs, true, nil
	case *googlesql.ResolvedProjectScan:
		input, _ := n.InputScan()
		fs, ok, err := flattenRecursivePipeline(ctx, input, refSubst, false)
		if !ok || err != nil {
			return nil, ok, err
		}
		exprs, _ := n.ExprList()
		exprCtx := mergedSubstCtx(ctx, fs.subst)
		computed := map[int32]string{}
		for _, cc := range exprs {
			expr, _ := cc.Expr()
			col, _ := cc.Column()
			if expr == nil || col == nil {
				return nil, false, nil
			}
			if !top && subtreeCallsNonDeterministic(expr) {
				// Substitution may evaluate the expression more
				// than once.
				return nil, false, nil
			}
			sql, err := newNode(expr).FormatSQL(exprCtx)
			if err != nil {
				return nil, false, err
			}
			id, _ := col.ColumnId()
			computed[id] = "(" + sql + ")"
		}
		for k, v := range computed {
			fs.subst[k] = v
		}
		return fs, true, nil
	}
	return nil, false, nil
}

// formatFlatRecursiveBranch renders a recursive term as one SELECT
// whose columns follow outCols. ok is false when the term is not a
// row-wise pipeline over the recursive reference.
func formatFlatRecursiveBranch(ctx context.Context, scan googlesql.ResolvedNode, outCols []*googlesql.ResolvedColumn, refSubst map[int32]string) (string, bool, error) {
	fs, ok, err := flattenRecursivePipeline(ctx, scan, refSubst, true)
	if !ok || err != nil {
		return "", ok, err
	}
	cols := make([]string, 0, len(outCols))
	for _, c := range outCols {
		id, _ := c.ColumnId()
		sql, ok := fs.subst[id]
		if !ok {
			return "", false, nil
		}
		cols = append(cols, sql)
	}
	out := fmt.Sprintf("SELECT %s FROM %s", strings.Join(cols, ","), fs.from)
	if len(fs.where) > 0 {
		out += " WHERE " + strings.Join(fs.where, " AND ")
	}
	return out, true, nil
}

// passThroughSetOperation looks through projections that only forward
// columns and returns the set operation a recursive term evaluates,
// when outCols line up positionally with the set operation's columns.
func passThroughSetOperation(scan googlesql.ResolvedNode, outCols []*googlesql.ResolvedColumn) *googlesql.ResolvedSetOperationScan {
	ids := make([]int32, len(outCols))
	for i, c := range outCols {
		ids[i], _ = c.ColumnId()
	}
	for scan != nil {
		switch n := scan.(type) {
		case *googlesql.ResolvedSetOperationScan:
			cols, _ := n.ColumnList()
			if len(cols) != len(ids) {
				return nil
			}
			for i, c := range cols {
				if id, _ := c.ColumnId(); id != ids[i] {
					return nil
				}
			}
			return n
		case *googlesql.ResolvedProjectScan:
			exprs, _ := n.ExprList()
			for _, cc := range exprs {
				col, _ := cc.Column()
				expr, _ := cc.Expr()
				ref, ok := expr.(*googlesql.ResolvedColumnRef)
				if !ok || col == nil {
					return nil
				}
				from, _ := ref.Column()
				if from == nil {
					return nil
				}
				to, _ := col.ColumnId()
				src, _ := from.ColumnId()
				for i := range ids {
					if ids[i] == to {
						ids[i] = src
					}
				}
			}
			input, _ := n.InputScan()
			scan = input
		default:
			return nil
		}
	}
	return nil
}

// formatFlatRecursiveTerm renders the recursive term in a shape SQLite
// accepts: either one flattened SELECT over the self-reference, or,
// when the term is itself a UNION, one recursive SELECT per branch
// that references the CTE, with the branches that do not moved next
// to the non-recursive term. Moving a branch is only equivalent under
// UNION DISTINCT, where re-emitting the same rows every iteration adds
// nothing. The returned SQL starts with the recursive SELECTs and may
// be preceded by "<initial SELECT> UNION ".
func (n *RecursiveScanNode) formatFlatRecursiveTerm(
	ctx context.Context,
	canonical []*googlesql.ResolvedColumn,
	distinct bool,
	formatBranch func(*googlesql.ResolvedSetOperationItem) (string, error),
) (string, bool, error) {
	item := m1(n.node.RecursiveTerm())
	if item == nil {
		return "", false, nil
	}
	scan, _ := item.Scan()
	outCols, _ := item.OutputColumnList()
	var refs []*googlesql.ResolvedRecursiveRefScan
	collectRecursiveRefScans(scan, &refs)
	refSubst := map[int32]string{}
	for _, ref := range refs {
		cols, _ := ref.ColumnList()
		if len(cols) != len(canonical) {
			return "", false, nil
		}
		for i, c := range cols {
			id, _ := c.ColumnId()
			refSubst[id] = fmt.Sprintf("`%s`", uniqueColumnName(ctx, canonical[i]))
		}
	}
	if len(refs) == 1 {
		if sql, ok, err := formatFlatRecursiveBranch(ctx, scan, outCols, refSubst); ok || err != nil {
			return sql, ok, err
		}
	}
	setop := passThroughSetOperation(scan, outCols)
	if setop == nil {
		return "", false, nil
	}
	innerOp, _ := setop.OpType()
	switch innerOp {
	case googlesql.ResolvedSetOperationScanEnums_SetOperationTypeUnionAll:
	case googlesql.ResolvedSetOperationScanEnums_SetOperationTypeUnionDistinct:
		if !distinct {
			return "", false, nil
		}
	default:
		return "", false, nil
	}
	op := " UNION ALL "
	if distinct {
		op = " UNION "
	}
	items, _ := setop.InputItemList()
	var initial, recursive []string
	for _, it := range items {
		itScan, _ := it.Scan()
		itCols, _ := it.OutputColumnList()
		if !containsRecursiveRef(itScan) {
			if !distinct {
				return "", false, nil
			}
			sql, err := formatBranch(it)
			if err != nil {
				return "", false, err
			}
			initial = append(initial, sql)
			continue
		}
		sql, ok, err := formatFlatRecursiveBranch(ctx, itScan, itCols, refSubst)
		if !ok || err != nil {
			return "", ok, err
		}
		recursive = append(recursive, sql)
	}
	if len(recursive) == 0 {
		return "", false, nil
	}
	out := strings.Join(recursive, op)
	if len(initial) > 0 {
		out = strings.Join(initial, op) + op + out
	}
	return out, true, nil
}
