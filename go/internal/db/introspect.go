package db

import (
	"context"
	"fmt"
	"strings"

	"github.com/earlye/schemify/internal/schema"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Introspect returns the current schema from the database (tables and columns)
// for the given schema name (e.g. "public").
func Introspect(ctx context.Context, pool *pgxpool.Pool, schemaName string) (map[string]*schema.Table, error) {
	if schemaName == "" {
		schemaName = "public"
	}

	tables, err := listTables(ctx, pool, schemaName)
	if err != nil {
		return nil, err
	}

	out := make(map[string]*schema.Table)
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
		out[tableKey(schemaName, tableName)] = t
	}
	return out, nil
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
