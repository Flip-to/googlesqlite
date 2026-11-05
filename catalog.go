package googlesqlite

import (
	"database/sql"
	"fmt"
	"reflect"

	internal "github.com/goccy/googlesqlite/internal"
)

type (
	ChangedCatalog  = internal.ChangedCatalog
	ChangedTable    = internal.ChangedTable
	ChangedFunction = internal.ChangedFunction
	// ChangedTableFunction lists the CREATE TABLE FUNCTION / DROP TABLE
	// FUNCTION statements of a query.
	ChangedTableFunction = internal.ChangedTableFunction
	// ChangedSchema lists the CREATE SCHEMA statements of a query.
	ChangedSchema = internal.ChangedSchema
	TVFSpec       = internal.TVFSpec
	SchemaSpec    = internal.SchemaSpec
	TableSpec       = internal.TableSpec
	FunctionSpec    = internal.FunctionSpec
	NameWithType    = internal.NameWithType
	ColumnSpec      = internal.ColumnSpec
	Type            = internal.Type
)

// ChangedCatalogFromRows retrieve modified catalog information from sql.Rows.
// NOTE: This API relies on the internal structure of sql.Rows, so not will work for all Go versions.
func ChangedCatalogFromRows(rows *sql.Rows) (*ChangedCatalog, error) {
	if rows == nil {
		return nil, fmt.Errorf("googlesqlite: sql.Rows instance required not nil")
	}
	rv := reflect.ValueOf(rows)
	rowsi := rv.Elem().FieldByName("rowsi")
	if !rowsi.IsValid() {
		return nil, fmt.Errorf("googlesqlite: unexpected sql.Rows layout")
	}
	driverValue := rowsi.Elem()
	if driverValue.Type() != reflect.TypeFor[*internal.Rows]() {
		return nil, fmt.Errorf("googlesqlite: sql.Rows must be an instance created using the googlesqlite database driver")
	}
	googlesqliteRows := (*internal.Rows)(driverValue.UnsafePointer())
	return googlesqliteRows.ChangedCatalog(), nil
}

// PartitionInfo is the PARTITION BY of a table; see TableSpec.Partitioning.
type PartitionInfo = internal.PartitionInfo
