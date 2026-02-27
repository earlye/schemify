package db

import (
	"context"
	"fmt"
	"strings"

	"github.com/earlye/schemify/internal/schema"
	"github.com/jackc/pgx/v5/pgxpool"
)

// IntrospectResult holds the result of Introspect (tables with constraints and indexes).
type IntrospectResult struct {
	Tables  map[string]*schema.Table  // key: schema.tablename
	Indexes map[string]*schema.Index   // key: schema.indexname
}

// Introspect returns the current schema from the database (tables with columns and constraints, and indexes)
// for the given schema name (e.g. "public").
func Introspect(ctx context.Context, pool *pgxpool.Pool, schemaName string) (*IntrospectResult, error) {
	if schemaName == "" {
		schemaName = "public"
	}

	tables, err := listTables(ctx, pool, schemaName)
	if err != nil {
		return nil, err
	}

	tablesOut := make(map[string]*schema.Table)
	for _, tableName := range tables {
		cols, err := listColumns(ctx, pool, schemaName, tableName)
		if err != nil {
			return nil, fmt.Errorf("table %s.%s: %w", schemaName, tableName, err)
		}
		t := &schema.Table{
			Schema:  schemaName,
			Name:    tableName,
			Columns: cols,
		}
		tablesOut[tableKey(schemaName, tableName)] = t
	}

	// Attach constraints to tables.
	if err := listConstraints(ctx, pool, schemaName, tablesOut); err != nil {
		return nil, err
	}

	indexesOut, err := listIndexes(ctx, pool, schemaName)
	if err != nil {
		return nil, fmt.Errorf("list indexes: %w", err)
	}

	return &IntrospectResult{Tables: tablesOut, Indexes: indexesOut}, nil
}

func tableKey(schemaName, tableName string) string {
	return schemaName + "." + tableName
}

func listTables(ctx context.Context, pool *pgxpool.Pool, schemaName string) ([]string, error) {
	rows, err := pool.Query(ctx,
		`SELECT table_name FROM information_schema.tables
		 WHERE table_schema = $1 AND table_type = 'BASE TABLE'
		 ORDER BY table_name`,
		schemaName)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		names = append(names, name)
	}
	return names, rows.Err()
}

func listColumns(ctx context.Context, pool *pgxpool.Pool, schemaName, tableName string) ([]schema.Column, error) {
	rows, err := pool.Query(ctx,
		`SELECT column_name, column_default, data_type, character_maximum_length
		 FROM information_schema.columns
		 WHERE table_schema = $1 AND table_name = $2
		 ORDER BY ordinal_position`,
		schemaName, tableName)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var cols []schema.Column
	for rows.Next() {
		var name, dataType string
		var def *string
		var maxLen *int32
		if err := rows.Scan(&name, &def, &dataType, &maxLen); err != nil {
			return nil, err
		}
		defaultVal := ""
		if def != nil {
			defaultVal = *def
		}
		pgType := dataType
		if maxLen != nil && (*maxLen > 0) {
			pgType = fmt.Sprintf("%s(%d)", dataType, *maxLen)
		}
		cols = append(cols, schema.Column{
			Name:     name,
			Type:     normalizeInfoSchemaType(pgType),
			Nullable: true, // we don't read is_nullable for now; can add
			Default:  defaultVal,
		})
	}
	return cols, rows.Err()
}

// normalizeInfoSchemaType converts information_schema types to the same
// canonical form used by schema.normalizeType so diff matches.
func normalizeInfoSchemaType(t string) string {
	t = strings.TrimSpace(strings.ToLower(t))
	switch {
	case t == "integer":
		return "integer"
	case t == "bigint":
		return "bigint"
	case t == "smallint":
		return "smallint"
	case strings.HasPrefix(t, "character varying"):
		return "character varying" + typeLengthSuffix(t)
	case strings.HasPrefix(t, "varchar"):
		return "character varying" + typeLengthSuffix(t)
	case strings.HasPrefix(t, "character("):
		return "character" + typeLengthSuffix(t)
	}
	return t
}

func typeLengthSuffix(t string) string {
	i := strings.Index(t, "(")
	if i < 0 {
		return ""
	}
	return t[i:]
}

// listConstraints populates PrimaryKey, UniqueKeys, ForeignKeys on each table in tables.
func listConstraints(ctx context.Context, pool *pgxpool.Pool, schemaName string, tables map[string]*schema.Table) error {
	// Primary keys and unique constraints: table_constraints + key_column_usage for column order.
	rows, err := pool.Query(ctx, `
		SELECT tc.constraint_name, tc.constraint_type, tc.table_name, kcu.column_name, kcu.ordinal_position
		FROM information_schema.table_constraints tc
		JOIN information_schema.key_column_usage kcu
		  ON tc.constraint_schema = kcu.constraint_schema AND tc.constraint_name = kcu.constraint_name AND tc.table_schema = kcu.table_schema AND tc.table_name = kcu.table_name
		WHERE tc.table_schema = $1 AND tc.constraint_type IN ('PRIMARY KEY', 'UNIQUE')
		ORDER BY tc.table_name, tc.constraint_name, kcu.ordinal_position`,
		schemaName)
	if err != nil {
		return err
	}
	defer rows.Close()
	type key struct{ table, name string }
	pkCols := make(map[key][]string)
	uniqueCols := make(map[key][]string)
	for rows.Next() {
		var cname, ctype, tname, col string
		var ord int
		if err := rows.Scan(&cname, &ctype, &tname, &col, &ord); err != nil {
			return err
		}
		k := key{table: tname, name: cname}
		if ctype == "PRIMARY KEY" {
			pkCols[k] = append(pkCols[k], col)
		} else {
			uniqueCols[k] = append(uniqueCols[k], col)
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for k, cols := range pkCols {
		tblKey := tableKey(schemaName, k.table)
		if t := tables[tblKey]; t != nil {
			t.PrimaryKey = &schema.PrimaryKeyConstraint{Name: k.name, Columns: cols}
		}
	}
	for k, cols := range uniqueCols {
		tblKey := tableKey(schemaName, k.table)
		if t := tables[tblKey]; t != nil {
			t.UniqueKeys = append(t.UniqueKeys, schema.UniqueConstraint{Name: k.name, Columns: cols})
		}
	}

	// Foreign keys: key_column_usage for local columns (ordered); join to ref constraint's key_column_usage for ref columns in order.
	rows2, err := pool.Query(ctx, `
		SELECT tc.table_name, tc.constraint_name, kcu.column_name, kcu.ordinal_position,
		       rc.unique_constraint_schema, rc.unique_constraint_name, rc.delete_rule, rc.update_rule,
		       kcu_ref.table_schema AS ref_schema, kcu_ref.table_name AS ref_table, kcu_ref.column_name AS ref_column
		FROM information_schema.table_constraints tc
		JOIN information_schema.key_column_usage kcu ON tc.constraint_schema = kcu.constraint_schema AND tc.constraint_name = kcu.constraint_name AND tc.table_schema = kcu.table_schema AND tc.table_name = kcu.table_name
		JOIN information_schema.referential_constraints rc ON tc.constraint_schema = rc.constraint_schema AND tc.constraint_name = rc.constraint_name
		JOIN information_schema.key_column_usage kcu_ref ON kcu_ref.constraint_schema = rc.unique_constraint_schema AND kcu_ref.constraint_name = rc.unique_constraint_name AND kcu_ref.ordinal_position = kcu.position_in_unique_constraint
		WHERE tc.table_schema = $1 AND tc.constraint_type = 'FOREIGN KEY'
		ORDER BY tc.table_name, tc.constraint_name, kcu.ordinal_position`,
		schemaName)
	if err != nil {
		return err
	}
	defer rows2.Close()
	type fkKey struct{ table, cname string }
	fkData := make(map[fkKey]struct {
		cols              []string
		refSchema         string
		refTable          string
		refCols           []string
		onDelete, onUpdate string
	})
	var curTable, curCname string
	var cols, refCols []string
	var refSchema, refTable, onDel, onUpd string
	for rows2.Next() {
		var tname, cname, col string
		var ord int
		var ucs, ucn string
		var delRule, upRule string
		var rs, rt, rc string
		if err := rows2.Scan(&tname, &cname, &col, &ord, &ucs, &ucn, &delRule, &upRule, &rs, &rt, &rc); err != nil {
			return err
		}
		if tname != curTable || cname != curCname {
			if curTable != "" {
				fkData[fkKey{curTable, curCname}] = struct {
					cols              []string
					refSchema         string
					refTable          string
					refCols           []string
					onDelete, onUpdate string
				}{cols, refSchema, refTable, refCols, onDel, onUpd}
			}
			curTable, curCname = tname, cname
			cols = []string{col}
			refCols = []string{rc}
			refSchema, refTable, onDel, onUpd = rs, rt, delRule, upRule
		} else {
			cols = append(cols, col)
			refCols = append(refCols, rc)
		}
	}
	if curTable != "" {
		fkData[fkKey{curTable, curCname}] = struct {
			cols              []string
			refSchema         string
			refTable          string
			refCols           []string
			onDelete, onUpdate string
		}{cols, refSchema, refTable, refCols, onDel, onUpd}
	}
	if err := rows2.Err(); err != nil {
		return err
	}
	for k, d := range fkData {
		tblKey := tableKey(schemaName, k.table)
		if t := tables[tblKey]; t != nil {
			t.ForeignKeys = append(t.ForeignKeys, schema.ForeignKey{
				Name:              k.cname,
				Columns:           d.cols,
				ReferencesSchema:  d.refSchema,
				ReferencesTable:   d.refTable,
				ReferencesColumns: d.refCols,
				OnDelete:          d.onDelete,
				OnUpdate:          d.onUpdate,
			})
		}
	}
	return nil
}

// listIndexes returns all indexes in the schema. Key: schema.indexname.
// Excludes indexes that are backing PK/UNIQUE constraints (we only want standalone CREATE INDEX).
// Actually we want all indexes for diffing; the DB may have both constraint-backed and standalone.
// So we list all indexes and return them as schema.Index (including unique and index type).
func listIndexes(ctx context.Context, pool *pgxpool.Pool, schemaName string) (map[string]*schema.Index, error) {
	// pg_index: indkey is int2vector of attnums; use generate_subscripts to get column names in order.
	rows, err := pool.Query(ctx, `
		SELECT n.nspname, c.relname AS indexname, t.relname AS tablename, tn.nspname AS tableschema,
		       i.indisunique, am.amname AS indextype,
		       (SELECT array_agg(a.attname ORDER BY ord)
		        FROM generate_subscripts(i.indkey, 1) AS ord
		        JOIN pg_attribute a ON a.attrelid = i.indrelid AND a.attnum = (i.indkey)[ord] AND a.attnum > 0 AND NOT a.attisdropped) AS columns
		FROM pg_index i
		JOIN pg_class c ON c.oid = i.indexrelid
		JOIN pg_namespace n ON n.oid = c.relnamespace
		JOIN pg_class t ON t.oid = i.indrelid
		JOIN pg_namespace tn ON tn.oid = t.relnamespace
		JOIN pg_am am ON am.oid = c.relam
		WHERE n.nspname = $1 AND c.relkind = 'i'`,
		schemaName)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]*schema.Index)
	for rows.Next() {
		var idxSchema, idxName, tableName, tableSchema string
		var isUnique bool
		var indexType string
		var cols []string
		if err := rows.Scan(&idxSchema, &idxName, &tableName, &tableSchema, &isUnique, &indexType, &cols); err != nil {
			return nil, err
		}
		if cols == nil {
			cols = []string{}
		}
		idx := &schema.Index{
			Name:          idxName,
			Schema:        idxSchema,
			TableSchema:   tableSchema,
			TableName:     tableName,
			Columns:       cols,
			Unique:        isUnique,
			IndexType:     indexType,
			Concurrently:  true, // introspected indexes are already created; we don't track how they were created
		}
		key := schema.IndexKey(idxSchema, idxName)
		out[key] = idx
	}
	return out, rows.Err()
}
