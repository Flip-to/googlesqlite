package internal

import (
	"context"
	"database/sql"
	"strings"
)

type ChangedCatalog struct {
	Table         *ChangedTable
	Function      *ChangedFunction
	TableFunction *ChangedTableFunction
	Schema        *ChangedSchema
}

func newChangedCatalog() *ChangedCatalog {
	return &ChangedCatalog{
		Table:         &ChangedTable{},
		Function:      &ChangedFunction{},
		TableFunction: &ChangedTableFunction{},
		Schema:        &ChangedSchema{},
	}
}

func (c *ChangedCatalog) Changed() bool {
	return c.Table.Changed() || c.Function.Changed() || c.TableFunction.Changed() || c.Schema.Changed()
}

// ChangedTableFunction lists the table-valued functions (CREATE TABLE
// FUNCTION) created or dropped by a statement.
type ChangedTableFunction struct {
	Added   []*TVFSpec
	Deleted []*TVFSpec
}

func (f *ChangedTableFunction) Changed() bool {
	return len(f.Added) != 0 || len(f.Deleted) != 0
}

// ChangedSchema lists the schemas (BigQuery datasets) requested by CREATE
// SCHEMA statements. The driver keeps no state for a schema itself; the
// embedding layer owns datasets and applies the request.
type ChangedSchema struct {
	Added []*SchemaSpec
	// Dropped lists the DROP SCHEMA statements: the embedding layer drops the dataset (and, with
	// Cascade, its tables).
	Dropped []*DroppedSchema
}

func (s *ChangedSchema) Changed() bool {
	return len(s.Added) != 0 || len(s.Dropped) != 0
}

// DroppedSchema is a DROP SCHEMA [IF EXISTS] statement.
type DroppedSchema struct {
	// NamePath is [dataset] or [project, dataset].
	NamePath []string `json:"namePath"`
	IfExists bool     `json:"ifExists"`
	// Cascade is set for DROP SCHEMA ... CASCADE; otherwise a schema that still holds tables is not dropped.
	Cascade bool `json:"cascade"`
}

type ChangedTable struct {
	Added   []*TableSpec
	Updated []*TableSpec
	Deleted []*TableSpec
}

func (t *ChangedTable) Changed() bool {
	return len(t.Added) != 0 || len(t.Updated) != 0 || len(t.Deleted) != 0
}

type ChangedFunction struct {
	Added   []*FunctionSpec
	Deleted []*FunctionSpec
}

func (f *ChangedFunction) Changed() bool {
	return len(f.Added) != 0 || len(f.Deleted) != 0
}

type Conn struct {
	conn           *sql.Conn
	tx             *sql.Tx
	cc             *ChangedCatalog
	systemVars     map[string]string
	scriptVars     map[string]string
	materializeCTE bool
}

func newConn(conn *sql.Conn, tx *sql.Tx) *Conn {
	return &Conn{
		conn:           conn,
		tx:             tx,
		cc:             newChangedCatalog(),
		systemVars:     map[string]string{},
		scriptVars:     map[string]string{},
		materializeCTE: true,
	}
}

// NewConnWithSystemVars constructs an internal.Conn that shares its
// session-scope @@system_variable map with the caller. The driver
// uses this so SET written in one statement is observable by the
// next statement on the same logical connection.
func newConnWithSystemVars(conn *sql.Conn, tx *sql.Tx, systemVars map[string]string) *Conn {
	if systemVars == nil {
		systemVars = map[string]string{}
	}
	return &Conn{
		conn:           conn,
		tx:             tx,
		cc:             newChangedCatalog(),
		systemVars:     systemVars,
		scriptVars:     map[string]string{},
		materializeCTE: true,
	}
}

// NewConnWithOptions is the full-control constructor. The driver
// uses this so per-Conn options (system vars, CTE materialise flag,
// ...) thread through every internal.Conn it spawns per statement.
func NewConnWithOptions(conn *sql.Conn, tx *sql.Tx, systemVars, scriptVars map[string]string, materializeCTE bool) *Conn {
	if systemVars == nil {
		systemVars = map[string]string{}
	}
	if scriptVars == nil {
		scriptVars = map[string]string{}
	}
	return &Conn{
		conn:           conn,
		tx:             tx,
		cc:             newChangedCatalog(),
		systemVars:     systemVars,
		scriptVars:     scriptVars,
		materializeCTE: materializeCTE,
	}
}

// SetScriptVariable updates the session-scope value of a script
// variable declared via DECLARE / SET. Stored as the formatted SQL
// expression text that should replace identifier references in
// subsequent statements on the same connection.
func (c *Conn) SetScriptVariable(name, exprSQL string) {
	if c.scriptVars == nil {
		c.scriptVars = map[string]string{}
	}
	c.scriptVars[strings.ToLower(name)] = exprSQL
}

// ScriptVariable returns the current SQL expression bound to a
// script variable name (case-insensitive) and whether the name has
// been declared.
func (c *Conn) ScriptVariable(name string) (string, bool) {
	if c.scriptVars == nil {
		return "", false
	}
	v, ok := c.scriptVars[strings.ToLower(name)]
	return v, ok
}

// MaterializeCTE reports the materialize-multi-ref-CTE flag for this
// connection. The formatter consults this through context to decide
// whether to emit the SQLite `MATERIALIZED` hint.
func (c *Conn) MaterializeCTE() bool {
	return c.materializeCTE
}

// SetSystemVariable updates the session-scope value of an
// `@@system_variable`. Unknown names are accepted on write so the
// session can carry forward variables we have not declared yet; the
// analyzer is the gate for read paths.
func (c *Conn) SetSystemVariable(name, value string) {
	if c.systemVars == nil {
		c.systemVars = map[string]string{}
	}
	c.systemVars[name] = value
}

// SystemVariable returns the current session value of the given
// `@@system_variable` name and whether it has been set.
func (c *Conn) SystemVariable(name string) (string, bool) {
	if c.systemVars == nil {
		return "", false
	}
	v, ok := c.systemVars[name]
	return v, ok
}

func (c *Conn) PrepareContext(ctx context.Context, query string) (*sql.Stmt, error) {
	if c.tx != nil {
		return c.tx.PrepareContext(ctx, query)
	}
	return c.conn.PrepareContext(ctx, query)
}

func (c *Conn) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	if c.tx != nil {
		return c.tx.ExecContext(ctx, query, args...)
	}
	return c.conn.ExecContext(ctx, query, args...)
}

func (c *Conn) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	if c.tx != nil {
		return c.tx.QueryContext(ctx, query, args...)
	}
	return c.conn.QueryContext(ctx, query, args...)
}

func (c *Conn) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	if c.tx != nil {
		return c.tx.QueryRowContext(ctx, query, args...)
	}
	return c.conn.QueryRowContext(ctx, query, args...)
}

func (c *Conn) addTable(spec *TableSpec) {
	c.removeFromDeletedTablesIfExists(spec)
	c.cc.Table.Added = append(c.cc.Table.Added, spec)
}

func (c *Conn) updateTable(spec *TableSpec) {
	c.removeFromUpdatedTablesIfExists(spec)
	c.cc.Table.Updated = append(c.cc.Table.Updated, spec)
}

func (c *Conn) removeFromUpdatedTablesIfExists(spec *TableSpec) {
	tables := make([]*TableSpec, 0, len(c.cc.Table.Updated))
	for _, table := range c.cc.Table.Updated {
		if table.TableName() == spec.TableName() {
			continue
		}
		tables = append(tables, table)
	}
	c.cc.Table.Updated = tables
}

func (c *Conn) addTVF(spec *TVFSpec) {
	c.cc.TableFunction.Deleted = filterTVFs(c.cc.TableFunction.Deleted, spec)
	c.cc.TableFunction.Added = append(c.cc.TableFunction.Added, spec)
}

func (c *Conn) deleteTVF(spec *TVFSpec) {
	c.cc.TableFunction.Added = filterTVFs(c.cc.TableFunction.Added, spec)
	c.cc.TableFunction.Deleted = append(c.cc.TableFunction.Deleted, spec)
}

func filterTVFs(specs []*TVFSpec, drop *TVFSpec) []*TVFSpec {
	out := make([]*TVFSpec, 0, len(specs))
	for _, spec := range specs {
		if spec.TVFName() == drop.TVFName() {
			continue
		}
		out = append(out, spec)
	}
	return out
}

func (c *Conn) addSchema(spec *SchemaSpec) {
	c.cc.Schema.Added = append(c.cc.Schema.Added, spec)
}

func (c *Conn) dropSchema(spec *DroppedSchema) {
	c.cc.Schema.Dropped = append(c.cc.Schema.Dropped, spec)
}

func (c *Conn) deleteTable(spec *TableSpec) {
	c.removeFromAddedTablesIfExists(spec)
	c.cc.Table.Deleted = append(c.cc.Table.Deleted, spec)
}

func (c *Conn) addFunction(spec *FunctionSpec) {
	c.removeFromDeletedFunctionsIfExists(spec)
	c.cc.Function.Added = append(c.cc.Function.Added, spec)
}

func (c *Conn) deleteFunction(spec *FunctionSpec) {
	c.removeFromAddedFunctionsIfExists(spec)
	c.cc.Function.Deleted = append(c.cc.Function.Deleted, spec)
}

func (c *Conn) removeFromDeletedTablesIfExists(spec *TableSpec) {
	tables := make([]*TableSpec, 0, len(c.cc.Table.Deleted))
	for _, table := range c.cc.Table.Deleted {
		if table.TableName() == spec.TableName() {
			continue
		}
		tables = append(tables, table)
	}
	c.cc.Table.Deleted = tables
}

func (c *Conn) removeFromAddedTablesIfExists(spec *TableSpec) {
	tables := make([]*TableSpec, 0, len(c.cc.Table.Added))
	for _, table := range c.cc.Table.Added {
		if table.TableName() == spec.TableName() {
			continue
		}
		tables = append(tables, table)
	}
	c.cc.Table.Added = tables
}

func (c *Conn) removeFromDeletedFunctionsIfExists(spec *FunctionSpec) {
	funcs := make([]*FunctionSpec, 0, len(c.cc.Function.Deleted))
	for _, fun := range c.cc.Function.Deleted {
		if fun.FuncName() == spec.FuncName() {
			continue
		}
		funcs = append(funcs, fun)
	}
	c.cc.Function.Deleted = funcs
}

func (c *Conn) removeFromAddedFunctionsIfExists(spec *FunctionSpec) {
	funcs := make([]*FunctionSpec, 0, len(c.cc.Function.Added))
	for _, fun := range c.cc.Function.Added {
		if fun.FuncName() == spec.FuncName() {
			continue
		}
		funcs = append(funcs, fun)
	}
	c.cc.Function.Added = funcs
}
