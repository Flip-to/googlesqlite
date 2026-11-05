package internal

import (
	"strings"
	"time"

	googlesql "github.com/goccy/go-googlesql"
	"github.com/goccy/googlesqlite/internal/value"
)

// InfoSchemaView describes one BigQuery `INFORMATION_SCHEMA.<name>`
// view. The driver exposes each view as a SQLite virtual table so
// rows are pulled lazily from the live catalog at query time —
// nothing is materialised until SQLite asks for it. This means
// after a process restart, INFORMATION_SCHEMA queries observe
// whatever the (file-backed) catalog contains, not stale snapshots.
type infoSchemaView struct {
	// Name is the BigQuery view name, e.g. "TABLES" / "COLUMNS".
	Name string
	// Module is the SQLite-side virtual-table name, e.g.
	// "googlesqlite_info_schema_tables".
	Module string
	// Columns are the visible columns in declaration order. The
	// trailing hidden `__schema` column carries the dataset
	// constraint passed by the formatter.
	Columns []*infoSchemaColumn
	// TopLevel marks a view that lives directly in the dataset
	// (`<dataset>.__TABLES__`) instead of under INFORMATION_SCHEMA.
	TopLevel bool
	// BuildRows synthesises rows for the view limited to the given
	// dataset. An empty schema (no constraint) means all datasets.
	// countRows reports the number of rows stored in a SQLite table.
	BuildRows func(c *Catalog, schema string, countRows rowCounter) []map[string]value.Value
}

// rowCounter returns the number of rows of the SQLite table with the given
// name, or false when it cannot be counted.
type rowCounter func(sqliteTable string) (int64, bool)

// timestampOrNull converts a time to a TIMESTAMP value; the zero time (an
// object whose creation time was not recorded) is NULL.
func timestampOrNull(t time.Time) value.Value {
	if t.IsZero() {
		return nil
	}
	return value.TimestampValue(t.UTC())
}

// millisOrNull converts a time to epoch milliseconds, the form __TABLES__
// uses for creation_time and last_modified_time.
func millisOrNull(t time.Time) value.Value {
	if t.IsZero() {
		return nil
	}
	return value.IntValue(t.UnixMilli())
}

// InfoSchemaColumn declares one virtual-table column.
type infoSchemaColumn struct {
	Name string
	// Type is the SQLite-level declared type. We use STRING for
	// most columns since BigQuery's INFORMATION_SCHEMA columns are
	// nearly all text-typed; INT64 / TIMESTAMP are reflected through
	// the value layer the same way as anywhere else.
	Type string
}

// infoSchemaViews enumerates every supported view. New views append
// here; the vtab registrar walks this slice on each connection.
var infoSchemaViews = []*infoSchemaView{
	{
		Name:   "SCHEMATA",
		Module: "googlesqlite_info_schema_schemata",
		Columns: []*infoSchemaColumn{
			{Name: "catalog_name", Type: "STRING"},
			{Name: "schema_name", Type: "STRING"},
			{Name: "schema_owner", Type: "STRING"},
			{Name: "creation_time", Type: "TIMESTAMP"},
			{Name: "last_modified_time", Type: "TIMESTAMP"},
			{Name: "location", Type: "STRING"},
		},
		BuildRows: buildSchemataRows,
	},
	{
		Name:   "TABLES",
		Module: "googlesqlite_info_schema_tables",
		Columns: []*infoSchemaColumn{
			{Name: "table_catalog", Type: "STRING"},
			{Name: "table_schema", Type: "STRING"},
			{Name: "table_name", Type: "STRING"},
			{Name: "table_type", Type: "STRING"},
			{Name: "is_insertable_into", Type: "STRING"},
			{Name: "is_typed", Type: "STRING"},
			{Name: "creation_time", Type: "TIMESTAMP"},
		},
		BuildRows: buildTablesRows,
	},
	{
		Name:   "COLUMNS",
		Module: "googlesqlite_info_schema_columns",
		Columns: []*infoSchemaColumn{
			{Name: "table_catalog", Type: "STRING"},
			{Name: "table_schema", Type: "STRING"},
			{Name: "table_name", Type: "STRING"},
			{Name: "column_name", Type: "STRING"},
			{Name: "ordinal_position", Type: "INT64"},
			{Name: "is_nullable", Type: "STRING"},
			{Name: "data_type", Type: "STRING"},
			{Name: "is_partitioning_column", Type: "STRING"},
			{Name: "is_hidden", Type: "STRING"},
			{Name: "is_system_defined", Type: "STRING"},
		},
		BuildRows: buildColumnsRows,
	},
	{
		Name:   "TABLE_OPTIONS",
		Module: "googlesqlite_info_schema_table_options",
		Columns: []*infoSchemaColumn{
			{Name: "table_catalog", Type: "STRING"},
			{Name: "table_schema", Type: "STRING"},
			{Name: "table_name", Type: "STRING"},
			{Name: "option_name", Type: "STRING"},
			{Name: "option_type", Type: "STRING"},
			{Name: "option_value", Type: "STRING"},
		},
		BuildRows: buildTableOptionsRows,
	},
	{
		Name:   "ROUTINES",
		Module: "googlesqlite_info_schema_routines",
		Columns: []*infoSchemaColumn{
			{Name: "specific_catalog", Type: "STRING"},
			{Name: "specific_schema", Type: "STRING"},
			{Name: "specific_name", Type: "STRING"},
			{Name: "routine_catalog", Type: "STRING"},
			{Name: "routine_schema", Type: "STRING"},
			{Name: "routine_name", Type: "STRING"},
			{Name: "routine_type", Type: "STRING"},
			{Name: "data_type", Type: "STRING"},
			{Name: "routine_body", Type: "STRING"},
			{Name: "routine_definition", Type: "STRING"},
			{Name: "external_language", Type: "STRING"},
			{Name: "is_deterministic", Type: "STRING"},
			{Name: "security_type", Type: "STRING"},
			{Name: "created", Type: "TIMESTAMP"},
			{Name: "last_altered", Type: "TIMESTAMP"},
		},
		BuildRows: buildRoutinesRows,
	},
	{
		Name:   "PARTITIONS",
		Module: "googlesqlite_info_schema_partitions",
		Columns: []*infoSchemaColumn{
			{Name: "table_catalog", Type: "STRING"},
			{Name: "table_schema", Type: "STRING"},
			{Name: "table_name", Type: "STRING"},
			{Name: "partition_id", Type: "STRING"},
			{Name: "total_rows", Type: "INT64"},
			{Name: "total_logical_bytes", Type: "INT64"},
			{Name: "last_modified_time", Type: "TIMESTAMP"},
			{Name: "storage_tier", Type: "STRING"},
		},
		BuildRows: buildPartitionsRows,
	},
	{
		// `<dataset>.__TABLES__`, the legacy table listing.
		Name:     "__TABLES__",
		Module:   "googlesqlite_info_schema_legacy_tables",
		TopLevel: true,
		Columns: []*infoSchemaColumn{
			{Name: "project_id", Type: "STRING"},
			{Name: "dataset_id", Type: "STRING"},
			{Name: "table_id", Type: "STRING"},
			{Name: "creation_time", Type: "INT64"},
			{Name: "last_modified_time", Type: "INT64"},
			{Name: "row_count", Type: "INT64"},
			{Name: "size_bytes", Type: "INT64"},
			{Name: "type", Type: "INT64"},
		},
		BuildRows: buildLegacyTablesRows,
	},
}

// catalogProjectAndSchemas walks every TableSpec / FunctionSpec /
// TVFSpec and gathers the (catalog, schema) pairs in deterministic
// order. The first NamePath segment is the catalog (BigQuery
// project), the second is the schema (BigQuery dataset).
func catalogProjectAndSchemas(c *Catalog) []struct{ catalog, schema string } {
	seen := map[string]struct{}{}
	out := make([]struct{ catalog, schema string }, 0)
	// Like matchesSchema: a 2-segment path is `dataset.table` (no
	// project prefix); a 3+ segment path is `project.dataset.table`
	// (project is the third-from-last segment, schema is the
	// second-from-last). Anything shorter than 2 segments is just a
	// bare name.
	add := func(path []string) {
		if len(path) < 2 {
			return
		}
		var cat, sch string
		switch len(path) {
		case 2:
			sch = path[0]
		default:
			cat = path[len(path)-3]
			sch = path[len(path)-2]
		}
		key := cat + "\x00" + sch
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		out = append(out, struct{ catalog, schema string }{cat, sch})
	}
	for _, t := range c.tables {
		add(t.NamePath)
	}
	for _, f := range c.functions {
		add(f.NamePath)
	}
	for _, t := range c.tvfs {
		add(t.NamePath)
	}
	return out
}

// matchesSchema reports whether spec.NamePath sits under the given
// schema filter. An empty filter accepts every dataset.
func matchesSchema(namePath []string, filter string) (catalog, schema string, ok bool) {
	// BigQuery's identifier layout is `project.dataset.table` (3
	// segments) or `dataset.table` (2 segments). The schema reported
	// by INFORMATION_SCHEMA is the dataset name — that's the
	// SECOND-from-last segment, never always NamePath[1].
	if len(namePath) < 2 {
		return "", "", false
	}
	var cat, sch string
	switch len(namePath) {
	case 2:
		sch = namePath[0]
	default:
		cat = namePath[len(namePath)-3]
		sch = namePath[len(namePath)-2]
	}
	if filter != "" && !strings.EqualFold(filter, sch) {
		return "", "", false
	}
	return cat, sch, true
}

func buildSchemataRows(c *Catalog, schema string, _ rowCounter) []map[string]value.Value {
	pairs := catalogProjectAndSchemas(c)
	out := make([]map[string]value.Value, 0, len(pairs))
	for _, p := range pairs {
		if schema != "" && !strings.EqualFold(schema, p.schema) {
			continue
		}
		out = append(out, map[string]value.Value{
			"catalog_name":       value.StringValue(p.catalog),
			"schema_name":        value.StringValue(p.schema),
			"schema_owner":       nil,
			"creation_time":      nil,
			"last_modified_time": nil,
			"location":           nil,
		})
	}
	return out
}

func buildTablesRows(c *Catalog, schema string, _ rowCounter) []map[string]value.Value {
	out := make([]map[string]value.Value, 0, len(c.tables))
	for _, t := range c.tables {
		cat, sch, ok := matchesSchema(t.NamePath, schema)
		if !ok {
			continue
		}
		tableType := "BASE TABLE"
		if t.IsView {
			tableType = "VIEW"
		}
		insertable := "YES"
		if t.IsView {
			insertable = "NO"
		}
		row := map[string]value.Value{
			"table_catalog":      value.StringValue(cat),
			"table_schema":       value.StringValue(sch),
			"table_name":         value.StringValue(t.NamePath[len(t.NamePath)-1]),
			"table_type":         value.StringValue(tableType),
			"is_insertable_into": value.StringValue(insertable),
			"is_typed":           value.StringValue("NO"),
			"creation_time":      timestampOrNull(t.CreatedAt),
		}
		out = append(out, row)
	}
	return out
}

func buildColumnsRows(c *Catalog, schema string, _ rowCounter) []map[string]value.Value {
	out := make([]map[string]value.Value, 0)
	for _, t := range c.tables {
		cat, sch, ok := matchesSchema(t.NamePath, schema)
		if !ok {
			continue
		}
		tableName := t.NamePath[len(t.NamePath)-1]
		for i, col := range t.Columns {
			isNullable := "YES"
			if col.IsNotNull {
				isNullable = "NO"
			}
			out = append(out, map[string]value.Value{
				"table_catalog":          value.StringValue(cat),
				"table_schema":           value.StringValue(sch),
				"table_name":             value.StringValue(tableName),
				"column_name":            value.StringValue(col.Name),
				"ordinal_position":       value.IntValue(int64(i + 1)),
				"is_nullable":            value.StringValue(isNullable),
				"data_type":              value.StringValue(infoSchemaDataType(col.Type)),
				"is_partitioning_column": value.StringValue("NO"),
				"is_hidden":              value.StringValue("NO"),
				"is_system_defined":      value.StringValue("NO"),
			})
		}
	}
	return out
}

func buildTableOptionsRows(c *Catalog, schema string, _ rowCounter) []map[string]value.Value {
	out := make([]map[string]value.Value, 0)
	for _, t := range c.tables {
		cat, sch, ok := matchesSchema(t.NamePath, schema)
		if !ok {
			continue
		}
		tableName := t.NamePath[len(t.NamePath)-1]
		for _, opt := range tableOptionsRows(t) {
			out = append(out, map[string]value.Value{
				"table_catalog": value.StringValue(cat),
				"table_schema":  value.StringValue(sch),
				"table_name":    value.StringValue(tableName),
				"option_name":   value.StringValue(opt.Name),
				"option_type":   value.StringValue(opt.Type),
				"option_value":  value.StringValue(opt.Value),
			})
		}
	}
	return out
}

// tableOptionRow is the trio that appears in INFORMATION_SCHEMA.
// TABLE_OPTIONS for one row. TableSpec carries the metadata; we
// surface whatever it set.
type tableOptionRow struct {
	Name, Type, Value string
}

func tableOptionsRows(t *TableSpec) []tableOptionRow {
	if t == nil || len(t.Options) == 0 {
		return nil
	}
	out := make([]tableOptionRow, 0, len(t.Options))
	for _, opt := range t.Options {
		if opt == nil || opt.Name == "" {
			continue
		}
		out = append(out, tableOptionRow{
			Name:  opt.Name,
			Type:  opt.Type,
			Value: opt.Value,
		})
	}
	return out
}

func infoSchemaDataType(typ *Type) string {
	if typ == nil {
		return ""
	}
	switch googlesql.TypeKind(typ.Kind) {
	case googlesql.TypeKindTypeArray:
		if typ.ElementType == nil {
			return "ARRAY"
		}
		return "ARRAY<" + infoSchemaDataType(typ.ElementType) + ">"
	case googlesql.TypeKindTypeStruct:
		fields := make([]string, 0, len(typ.FieldTypes))
		for _, f := range typ.FieldTypes {
			fields = append(fields, f.Name+" "+infoSchemaDataType(f.Type))
		}
		return "STRUCT<" + strings.Join(fields, ", ") + ">"
	}
	return typeKindToSQLName(googlesql.TypeKind(typ.Kind))
}

// buildRoutinesRows lists the persistent SQL / JavaScript functions and the
// table functions of the catalog. Temporary functions have no dataset and
// are not listed, as in BigQuery.
func buildRoutinesRows(c *Catalog, schema string, _ rowCounter) []map[string]value.Value {
	out := make([]map[string]value.Value, 0, len(c.functions)+len(c.tvfs))
	for _, f := range c.functions {
		if f.IsTemp {
			continue
		}
		cat, sch, ok := matchesSchema(f.NamePath, schema)
		if !ok {
			continue
		}
		routineType := "FUNCTION"
		if f.IsAggregate {
			routineType = "AGGREGATE FUNCTION"
		}
		out = append(out, routineRow(cat, sch, f.NamePath[len(f.NamePath)-1], routineType,
			infoSchemaDataType(f.Return), f.Language, f.Code, f.CreatedAt, f.UpdatedAt))
	}
	for _, t := range c.tvfs {
		if t.IsTemp {
			continue
		}
		cat, sch, ok := matchesSchema(t.NamePath, schema)
		if !ok {
			continue
		}
		out = append(out, routineRow(cat, sch, t.NamePath[len(t.NamePath)-1], "TABLE FUNCTION",
			"", "SQL", t.Code, t.CreatedAt, t.UpdatedAt))
	}
	return out
}

func routineRow(cat, sch, name, routineType, dataType, language, code string, created, updated time.Time) map[string]value.Value {
	body := "SQL"
	var externalLanguage value.Value
	if strings.EqualFold(language, "js") || strings.EqualFold(language, "javascript") {
		body = "EXTERNAL"
		externalLanguage = value.StringValue("JAVASCRIPT")
	}
	row := map[string]value.Value{
		"specific_catalog":   value.StringValue(cat),
		"specific_schema":    value.StringValue(sch),
		"specific_name":      value.StringValue(name),
		"routine_catalog":    value.StringValue(cat),
		"routine_schema":     value.StringValue(sch),
		"routine_name":       value.StringValue(name),
		"routine_type":       value.StringValue(routineType),
		"data_type":          nil,
		"routine_body":       value.StringValue(body),
		"routine_definition": nil,
		"external_language":  externalLanguage,
		"is_deterministic":   nil,
		"security_type":      nil,
		"created":            timestampOrNull(created),
		"last_altered":       timestampOrNull(updated),
	}
	if dataType != "" {
		row["data_type"] = value.StringValue(dataType)
	}
	if code != "" {
		row["routine_definition"] = value.StringValue(code)
	}
	return row
}

// buildPartitionsRows reports one row per base table. The emulator does not
// partition storage, so a table is a single unpartitioned partition: its
// partition_id is NULL. total_logical_bytes is not tracked and reported as 0.
func buildPartitionsRows(c *Catalog, schema string, countRows rowCounter) []map[string]value.Value {
	out := make([]map[string]value.Value, 0, len(c.tables))
	for _, t := range c.tables {
		if t.IsView || t.IsTemp {
			continue
		}
		cat, sch, ok := matchesSchema(t.NamePath, schema)
		if !ok {
			continue
		}
		rows, _ := countRows(t.TableName())
		out = append(out, map[string]value.Value{
			"table_catalog":       value.StringValue(cat),
			"table_schema":        value.StringValue(sch),
			"table_name":          value.StringValue(t.NamePath[len(t.NamePath)-1]),
			"partition_id":        nil,
			"total_rows":          value.IntValue(rows),
			"total_logical_bytes": value.IntValue(0),
			"last_modified_time":  timestampOrNull(lastModified(t)),
			"storage_tier":        value.StringValue("ACTIVE"),
		})
	}
	return out
}

func lastModified(t *TableSpec) time.Time {
	if !t.UpdatedAt.IsZero() {
		return t.UpdatedAt
	}
	return t.CreatedAt
}

// buildLegacyTablesRows backs `<dataset>.__TABLES__`. type is 1 for a table
// and 2 for a view; size_bytes is not tracked and reported as 0.
func buildLegacyTablesRows(c *Catalog, schema string, countRows rowCounter) []map[string]value.Value {
	out := make([]map[string]value.Value, 0, len(c.tables))
	for _, t := range c.tables {
		if t.IsTemp {
			continue
		}
		cat, sch, ok := matchesSchema(t.NamePath, schema)
		if !ok {
			continue
		}
		tableType, rows := int64(1), int64(0)
		if t.IsView {
			tableType = 2
		} else {
			rows, _ = countRows(t.TableName())
		}
		out = append(out, map[string]value.Value{
			"project_id":         value.StringValue(cat),
			"dataset_id":         value.StringValue(sch),
			"table_id":           value.StringValue(t.NamePath[len(t.NamePath)-1]),
			"creation_time":      millisOrNull(t.CreatedAt),
			"last_modified_time": millisOrNull(lastModified(t)),
			"row_count":          value.IntValue(rows),
			"size_bytes":         value.IntValue(0),
			"type":               value.IntValue(tableType),
		})
	}
	return out
}
