package internal

import (
	"context"
	"database/sql/driver"
	"fmt"
	"strings"
	"time"

	googlesql "github.com/goccy/go-googlesql"
)

// addColumnRequest is one ADD COLUMN [IF NOT EXISTS] action of an ALTER
// TABLE statement, copied out of the resolved AST.
type addColumnRequest struct {
	column      *ColumnSpec
	ifNotExists bool
}

// AlterTableStmtAction runs ALTER TABLE ... ADD COLUMN [IF NOT EXISTS]: it
// adds the column to the SQLite table, to the catalog spec (so the analyzer
// resolves it in later statements and INFORMATION_SCHEMA lists it) and
// reports the table as updated. Other ALTER TABLE actions stay no-ops, as
// they were before.
type AlterTableStmtAction struct {
	catalog  *Catalog
	namePath []string
	ifExists bool
	adds     []*addColumnRequest
}

func (a *Analyzer) newAlterTableStmtAction(ctx context.Context, node *googlesql.ResolvedAlterTableStmt) (StmtAction, error) {
	actions, err := node.AlterActionList()
	if err != nil {
		return nil, err
	}
	var adds []*addColumnRequest
	for _, action := range actions {
		add, ok := action.(*googlesql.ResolvedAddColumnAction)
		if !ok {
			continue
		}
		def, err := add.ColumnDefinition()
		if err != nil {
			return nil, err
		}
		ifNotExists, err := add.IsIfNotExists()
		if err != nil {
			return nil, err
		}
		columns := newColumnsFromDef(ctx, []*googlesql.ResolvedColumnDefinition{def})
		adds = append(adds, &addColumnRequest{column: columns[0], ifNotExists: ifNotExists})
	}
	if len(adds) == 0 {
		return &NoopStmtAction{}, nil
	}
	ifExists, _ := node.IsIfExists()
	return &AlterTableStmtAction{
		catalog:  a.catalog,
		namePath: a.namePath.mergePath(m1(node.NamePath())),
		ifExists: ifExists,
		adds:     adds,
	}, nil
}

func (a *AlterTableStmtAction) exec(ctx context.Context, conn *Conn) error {
	name := formatPath(a.namePath)
	a.catalog.mu.Lock()
	spec, exists := a.catalog.tableMap[name]
	a.catalog.mu.Unlock()
	if !exists {
		if a.ifExists {
			return nil
		}
		return fmt.Errorf("ALTER TABLE: table %s not found", name)
	}
	if spec.IsView {
		return fmt.Errorf("ALTER TABLE ADD COLUMN: %s is a view", name)
	}
	// Work on a copy so the catalog keeps the old spec until the whole
	// statement has succeeded.
	updated := *spec
	updated.Columns = append([]*ColumnSpec{}, spec.Columns...)
	changed := false
	for _, add := range a.adds {
		if updated.Column(add.column.Name) != nil {
			if add.ifNotExists {
				continue
			}
			return fmt.Errorf("ALTER TABLE ADD COLUMN: column %s already exists in %s", add.column.Name, name)
		}
		// SQLite refuses NOT NULL without a default; BigQuery only allows
		// adding NULLABLE columns.
		col := *add.column
		col.IsNotNull = false
		query := fmt.Sprintf("ALTER TABLE `%s` ADD COLUMN %s", spec.TableName(), col.SQLiteSchema())
		if _, err := conn.ExecContext(ctx, query); err != nil {
			return fmt.Errorf("failed to exec %s: %w", query, err)
		}
		updated.Columns = append(updated.Columns, &col)
		changed = true
	}
	if !changed {
		return nil
	}
	updated.UpdatedAt = time.Now()
	if err := a.catalog.replaceTableSpec(ctx, conn, name, &updated); err != nil {
		return fmt.Errorf("failed to update table spec: %w", err)
	}
	if !updated.IsTemp {
		conn.updateTable(&updated)
	}
	return nil
}

func (a *AlterTableStmtAction) Prepare(ctx context.Context, conn *Conn) (driver.Stmt, error) {
	return nil, nil
}

func (a *AlterTableStmtAction) ExecContext(ctx context.Context, conn *Conn) (driver.Result, error) {
	if err := a.exec(ctx, conn); err != nil {
		return nil, err
	}
	return &Result{conn: conn}, nil
}

func (a *AlterTableStmtAction) QueryContext(ctx context.Context, conn *Conn) (*Rows, error) {
	if err := a.exec(ctx, conn); err != nil {
		return nil, err
	}
	return &Rows{conn: conn}, nil
}

func (a *AlterTableStmtAction) Args() []any { return nil }

func (a *AlterTableStmtAction) Cleanup(ctx context.Context, conn *Conn) error { return nil }

// replaceTableSpec swaps the catalog's spec of a table for a new one. The
// analyzer catalog cannot change a table in place, so the old spec is
// removed (which rebuilds the catalog) and the new one registered.
func (c *Catalog) replaceTableSpec(ctx context.Context, conn *Conn, name string, spec *TableSpec) error {
	if err := c.DeleteTableSpec(ctx, conn, name); err != nil {
		return err
	}
	return c.AddNewTableSpec(ctx, conn, spec)
}

// CreateSchemaStmtAction reports CREATE SCHEMA through the ChangedCatalog.
// The driver has no schema objects of its own.
type CreateSchemaStmtAction struct {
	spec *SchemaSpec
}

func (a *Analyzer) newCreateSchemaStmtAction(node *googlesql.ResolvedCreateSchemaStmt) (StmtAction, error) {
	mode, err := node.CreateMode()
	if err != nil {
		return nil, err
	}
	path, err := node.NamePath()
	if err != nil {
		return nil, err
	}
	// A schema path is [dataset] or [project, dataset]; the analyzer's
	// default path is [project, dataset] so it is not merged in.
	path = a.namePath.normalizePath(path)
	if len(path) == 0 || len(path) > 2 {
		return nil, fmt.Errorf("CREATE SCHEMA: invalid schema name %q", strings.Join(path, "."))
	}
	opts, err := node.OptionList()
	if err != nil {
		return nil, err
	}
	return &CreateSchemaStmtAction{spec: &SchemaSpec{
		NamePath:    path,
		IfNotExists: mode == googlesql.ResolvedCreateStatementEnums_CreateModeCreateIfNotExists,
		OrReplace:   mode == googlesql.ResolvedCreateStatementEnums_CreateModeCreateOrReplace,
		Options:     newTableOptionsFromResolved(opts),
	}}, nil
}

// DropSchemaStmtAction reports DROP SCHEMA through the ChangedCatalog; the driver has no schema
// objects of its own, the embedding layer drops the dataset.
type DropSchemaStmtAction struct {
	spec *DroppedSchema
}

func (a *Analyzer) newDropSchemaStmtAction(node *googlesql.ResolvedDropStmt) (StmtAction, error) {
	path, err := node.NamePath()
	if err != nil {
		return nil, err
	}
	path = a.namePath.normalizePath(path)
	if len(path) == 0 || len(path) > 2 {
		return nil, fmt.Errorf("DROP SCHEMA: invalid schema name %q", strings.Join(path, "."))
	}
	ifExists, _ := node.IsIfExists()
	mode, _ := node.DropMode()
	return &DropSchemaStmtAction{spec: &DroppedSchema{
		NamePath: path,
		IfExists: ifExists,
		Cascade:  mode == googlesql.ResolvedDropStmtEnums_DropModeCascade,
	}}, nil
}

func (a *DropSchemaStmtAction) Prepare(ctx context.Context, conn *Conn) (driver.Stmt, error) {
	return nil, nil
}

func (a *DropSchemaStmtAction) ExecContext(ctx context.Context, conn *Conn) (driver.Result, error) {
	conn.dropSchema(a.spec)
	return &Result{conn: conn}, nil
}

func (a *DropSchemaStmtAction) QueryContext(ctx context.Context, conn *Conn) (*Rows, error) {
	conn.dropSchema(a.spec)
	return &Rows{conn: conn}, nil
}

func (a *DropSchemaStmtAction) Args() []any { return nil }

func (a *DropSchemaStmtAction) Cleanup(ctx context.Context, conn *Conn) error { return nil }

func (a *CreateSchemaStmtAction) Prepare(ctx context.Context, conn *Conn) (driver.Stmt, error) {
	return nil, nil
}

func (a *CreateSchemaStmtAction) ExecContext(ctx context.Context, conn *Conn) (driver.Result, error) {
	conn.addSchema(a.spec)
	return &Result{conn: conn}, nil
}

func (a *CreateSchemaStmtAction) QueryContext(ctx context.Context, conn *Conn) (*Rows, error) {
	conn.addSchema(a.spec)
	return &Rows{conn: conn}, nil
}

func (a *CreateSchemaStmtAction) Args() []any { return nil }

func (a *CreateSchemaStmtAction) Cleanup(ctx context.Context, conn *Conn) error { return nil }
