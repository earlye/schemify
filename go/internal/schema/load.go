package schema

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/valkdb/postgresparser"
)

// LoadFromDir reads all *.sql files from dir, parses CREATE TABLE statements,
// and returns the desired schema as a map keyed by "schema_name.table_name".
// Comments are retained in the source for future directive parsing.
func LoadFromDir(dir string) (map[string]*Table, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read schema dir: %w", err)
	}

	var sqlFiles []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if strings.HasSuffix(strings.ToLower(e.Name()), ".sql") {
			sqlFiles = append(sqlFiles, filepath.Join(dir, e.Name()))
		}
	}
	sort.Strings(sqlFiles)

	desired := make(map[string]*Table)

	for _, path := range sqlFiles {
		body, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", path, err)
		}
		tables, err := parseDDL(string(body))
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", path, err)
		}
		for _, t := range tables {
			key := tableKey(t.Schema, t.Name)
			desired[key] = t
		}
	}

	return desired, nil
}

func tableKey(schema, name string) string {
	if schema == "" {
		schema = "public"
	}
	return schema + "." + name
}

func parseDDL(sql string) ([]*Table, error) {
	batch, err := postgresparser.ParseSQLAll(sql)
	if err != nil {
		return nil, err
	}

	var out []*Table
	for _, stmt := range batch.Statements {
		if stmt.Query == nil {
			continue
		}
		for _, action := range stmt.Query.DDLActions {
			if action.Type != postgresparser.DDLCreateTable {
				continue
			}
			t := &Table{
				Schema:  action.Schema,
				Name:    action.ObjectName,
				Columns: make([]Column, 0, len(action.ColumnDetails)),
			}
			if t.Schema == "" {
				t.Schema = "public"
			}
			for _, c := range action.ColumnDetails {
				t.Columns = append(t.Columns, Column{
					Name:     c.Name,
					Type:     normalizeType(c.Type),
					Nullable: c.Nullable,
					Default:  c.Default,
				})
			}
			out = append(out, t)
		}
	}
	return out, nil
}

// normalizeType canonicalizes PostgreSQL type names for comparison with information_schema.
func normalizeType(t string) string {
	t = strings.TrimSpace(strings.ToLower(t))
	// character varying(n) / varchar(n) -> keep as "character varying(n)" for simplicity
	// integer, int, int4 -> integer
	switch {
	case t == "int" || t == "int4":
		return "integer"
	case t == "int8":
		return "bigint"
	case t == "int2":
		return "smallint"
	case strings.HasPrefix(t, "character varying") || strings.HasPrefix(t, "varchar"):
		return "character varying" + typeLengthSuffix(t)
	case strings.HasPrefix(t, "char(") || t == "character":
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
