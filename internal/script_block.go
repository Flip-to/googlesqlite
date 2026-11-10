package internal

import (
	"context"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"runtime"

	googlesql "github.com/goccy/go-googlesql"
)

// A BEGIN ... EXCEPTION WHEN ERROR THEN ... END block is run by scriptBlockAction, which gives it
// BigQuery's semantics: the body runs statement by statement; when one fails (analysis, DDL, DML or
// query start), the handler runs with @@error.message (and @@error.statement_text) set to the
// failure; IF / ELSEIF / ELSE, RAISE [USING MESSAGE = ...], RAISE; (re-raise) and nested blocks are
// executed inside the block. A block without a handler is still flattened into the surrounding
// script by parseScript, and IF / RAISE outside a handled block stay no-ops.
//
// Limit: a failure that only surfaces while the rows of a SELECT are read (for example a division
// by zero in the last statement of the body) happens after the block ended and is not handled.

const (
	errorMessageVar       = "error.message"
	errorStatementTextVar = "error.statement_text"
)

// scriptRaisedError is the error of a RAISE statement; its text is the message, as in BigQuery.
type scriptRaisedError struct{ msg string }

func (e *scriptRaisedError) Error() string { return e.msg }

// blockHasExceptionHandler reports whether a BEGIN ... END block has an EXCEPTION clause.
func blockHasExceptionHandler(block *googlesql.ASTBeginEndBlock) bool {
	ok, err := block.HasExceptionHandler()
	return err == nil && ok
}

type scriptBlockAction struct {
	a       *Analyzer
	ctx     context.Context
	query   string
	block   *googlesql.ASTBeginEndBlock
	funcMap map[string]*FunctionSpec
	tvfMap  map[string]*TVFSpec
	parsed  *parsedScript // owns the AST the block points into
	actions []StmtAction
	// handling is the stack of errors whose handlers are running (RAISE; re-raises the top one).
	handling []error
}

func (b *scriptBlockAction) Prepare(context.Context, *Conn) (driver.Stmt, error) { return nil, nil }

func (b *scriptBlockAction) Args() []any { return nil }

func (b *scriptBlockAction) Cleanup(ctx context.Context, conn *Conn) error {
	eg := new(ErrorGroup)
	for _, action := range b.actions {
		eg.Add(action.Cleanup(ctx, conn))
	}
	if eg.HasError() {
		return eg
	}
	return nil
}

func (b *scriptBlockAction) ExecContext(ctx context.Context, conn *Conn) (driver.Result, error) {
	defer runtime.KeepAlive(b.parsed)
	res, _, err := b.runBlock(ctx, conn, b.block, false)
	if err != nil {
		return nil, err
	}
	if res == nil {
		res = &Result{conn: conn}
	}
	return res, nil
}

func (b *scriptBlockAction) QueryContext(ctx context.Context, conn *Conn) (*Rows, error) {
	defer runtime.KeepAlive(b.parsed)
	_, rows, err := b.runBlock(ctx, conn, b.block, true)
	if err != nil {
		return nil, err
	}
	if rows == nil {
		rows = &Rows{conn: conn}
	}
	return rows, nil
}

// runBlock runs the body of block and, when it fails and the block has a handler, the handler.
func (b *scriptBlockAction) runBlock(ctx context.Context, conn *Conn, block *googlesql.ASTBeginEndBlock, query bool) (driver.Result, *Rows, error) {
	var body []googlesql.ASTStatementNode
	if list, err := block.StatementListNode(); err == nil && list != nil {
		body = collectStatementsFromList(list)
	}
	res, rows, err := b.runList(ctx, conn, body, query)
	if err == nil || ctx.Err() != nil || !blockHasExceptionHandler(block) {
		return res, rows, err
	}
	handlers, herr := block.HandlerList()
	if herr != nil || handlers == nil {
		return nil, nil, err
	}
	handler, herr := handlers.ExceptionHandlerList(0)
	if herr != nil || handler == nil {
		return nil, nil, err
	}
	list, herr := handler.StatementList()
	if herr != nil || list == nil {
		return nil, nil, err
	}
	b.handling = append(b.handling, err)
	conn.SetSystemVariable(errorMessageVar, err.Error())
	conn.SetSystemVariable(errorStatementTextVar, b.query)
	defer func() {
		b.handling = b.handling[:len(b.handling)-1]
		delete(conn.systemVars, errorMessageVar)
		delete(conn.systemVars, errorStatementTextVar)
	}()
	return b.runList(ctx, conn, collectStatementsFromList(list), query)
}

// runList runs stmts in order and returns the result (Exec mode) or rows (Query mode) of the last
// statement that produced one.
func (b *scriptBlockAction) runList(ctx context.Context, conn *Conn, stmts []googlesql.ASTStatementNode, query bool) (driver.Result, *Rows, error) {
	var (
		res  driver.Result
		rows *Rows
	)
	for _, stmt := range stmts {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		var (
			r    driver.Result
			rs   *Rows
			serr error
		)
		switch n := stmt.(type) {
		case *googlesql.ASTBeginEndBlock:
			r, rs, serr = b.runBlock(ctx, conn, n, query)
		case *googlesql.ASTIfStatement:
			r, rs, serr = b.runIf(ctx, conn, n, query)
		case *googlesql.ASTRaiseStatement:
			serr = b.raise(ctx, conn, n)
		default:
			if isScriptControlStmt(stmt) {
				continue
			}
			r, rs, serr = b.runStatement(ctx, conn, stmt, query)
		}
		if serr != nil {
			return nil, nil, serr
		}
		if r != nil {
			res = r
		}
		if rs != nil {
			rows = rs
		}
	}
	return res, rows, nil
}

func (b *scriptBlockAction) runStatement(ctx context.Context, conn *Conn, stmt googlesql.ASTStatementNode, query bool) (driver.Result, *Rows, error) {
	action, err := b.a.statementActionFunc(b.ctx, conn, stmt, nil, b.query, b.funcMap, b.tvfMap)()
	if err != nil {
		return nil, nil, err
	}
	b.actions = append(b.actions, action)
	if query {
		rows, err := action.QueryContext(ctx, conn)
		return nil, rows, err
	}
	res, err := action.ExecContext(ctx, conn)
	return res, nil, err
}

func (b *scriptBlockAction) runIf(ctx context.Context, conn *Conn, stmt *googlesql.ASTIfStatement, query bool) (driver.Result, *Rows, error) {
	cond, err := stmt.Condition()
	if err != nil {
		return nil, nil, err
	}
	ok, err := b.evalBool(ctx, conn, cond)
	if err != nil {
		return nil, nil, err
	}
	if ok {
		return b.runStatementList(ctx, conn, stmt.ThenList, query)
	}
	if clauses, err := stmt.ElseifClauses(); err == nil && clauses != nil {
		n, _ := clauses.NumChildren()
		for i := int32(0); i < n; i++ {
			clause, err := clauses.ElseifClauses(i)
			if err != nil || clause == nil {
				break
			}
			c, err := clause.Condition()
			if err != nil {
				return nil, nil, err
			}
			ok, err := b.evalBool(ctx, conn, c)
			if err != nil {
				return nil, nil, err
			}
			if ok {
				return b.runStatementList(ctx, conn, clause.Body, query)
			}
		}
	}
	return b.runStatementList(ctx, conn, stmt.ElseList, query)
}

func (b *scriptBlockAction) runStatementList(ctx context.Context, conn *Conn, get func() (*googlesql.ASTStatementList, error), query bool) (driver.Result, *Rows, error) {
	list, err := get()
	if err != nil || list == nil {
		return nil, nil, nil
	}
	return b.runList(ctx, conn, collectStatementsFromList(list), query)
}

// raise handles RAISE [USING MESSAGE = expr] and RAISE; (re-raise of the error being handled).
func (b *scriptBlockAction) raise(ctx context.Context, conn *Conn, stmt *googlesql.ASTRaiseStatement) error {
	if rethrow, _ := stmt.IsRethrow(); rethrow {
		if len(b.handling) == 0 {
			return errors.New("RAISE statement with no message must be inside an exception handler")
		}
		return b.handling[len(b.handling)-1]
	}
	msg := ""
	if expr, err := stmt.Message(); err == nil && expr != nil {
		v, err := b.evalScalar(ctx, conn, expr)
		if err != nil {
			return err
		}
		if v != nil {
			msg = fmt.Sprint(v)
		}
	}
	return &scriptRaisedError{msg: msg}
}

func (b *scriptBlockAction) evalBool(ctx context.Context, conn *Conn, expr googlesql.ASTExpressionNode) (bool, error) {
	v, err := b.evalScalar(ctx, conn, expr)
	if err != nil {
		return false, err
	}
	ok, _ := v.(bool) // NULL is false
	return ok, nil
}

// evalScalar evaluates a script expression (an IF condition, a RAISE message) with the full
// GoogleSQL analyzer as `SELECT <expr>` and returns the single value.
func (b *scriptBlockAction) evalScalar(ctx context.Context, conn *Conn, expr googlesql.ASTExpressionNode) (any, error) {
	text, err := astNodeText(b.query, expr)
	if err != nil {
		return nil, err
	}
	funcs, err := b.a.Analyze(ctx, conn, "SELECT "+text, nil)
	if err != nil {
		return nil, err
	}
	var out any
	for _, f := range funcs {
		action, err := f()
		if err != nil {
			return nil, err
		}
		rows, err := action.QueryContext(ctx, conn)
		if err != nil {
			_ = action.Cleanup(ctx, conn)
			return nil, err
		}
		dest := make([]driver.Value, max(len(rows.Columns()), 1))
		nerr := rows.Next(dest)
		_ = rows.Close()
		_ = action.Cleanup(ctx, conn)
		if nerr != nil && !errors.Is(nerr, io.EOF) {
			return nil, nerr
		}
		if nerr == nil {
			out = dest[0]
		}
	}
	return out, nil
}

type locatedNode interface {
	StartLocation() (*googlesql.ParseLocationPoint, error)
	EndLocation() (*googlesql.ParseLocationPoint, error)
}

// astNodeText returns the source text of node within query.
func astNodeText(query string, node locatedNode) (string, error) {
	startPoint, err := node.StartLocation()
	if err != nil {
		return "", err
	}
	endPoint, err := node.EndLocation()
	if err != nil {
		return "", err
	}
	start, err := startPoint.GetByteOffset()
	if err != nil {
		return "", err
	}
	end, err := endPoint.GetByteOffset()
	if err != nil {
		return "", err
	}
	if start < 0 || end < start || int(end) > len(query) {
		return "", fmt.Errorf("script expression out of range [%d, %d) of %d bytes", start, end, len(query))
	}
	return query[start:end], nil
}
