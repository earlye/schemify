package schema

import (
	"fmt"
	"io/fs"
	"log/slog"
	"maps"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/earlye/schemify/go/schemify/internal/helpers"
	"github.com/earlye/postgresparser"
)

// LoadResult holds the result of LoadFromDir (tables, indexes, and explicit CREATE SCHEMA names).
type LoadResult struct {
	Schemas map[string]struct{} // from CREATE SCHEMA statements in SQL files
	Tables  map[string]*Table   // key: schema.tablename
	Indexes map[string]*Index   // key: schema.indexname
}

// LoadFromDir reads all *.sql files from dir, parses CREATE TABLE and CREATE INDEX statements,
// and returns the desired schema (tables with optional PK/UNIQUE/FK, and indexes).
// Indexes must use CREATE INDEX CONCURRENTLY or loading fails (for large-table safety).
// Comments are retained in the source for future directive parsing.
func LoadFromFS(fsys fs.FS) (*LoadResult, error) {
	slog.Debug("Loading schema from FS", "fsys", fsys)

	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return nil, fmt.Errorf("read schema dir: %w", err)
	}

	var sqlFiles []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if strings.HasSuffix(strings.ToLower(e.Name()), ".sql") {
			sqlFiles = append(sqlFiles, e.Name())
		}
	}
	sort.Strings(sqlFiles)
	slog.Debug("Found SQL files:", "sqlFiles", sqlFiles)

	tables := make(map[string]*Table)
	indexes := make(map[string]*Index)
	schemas := make(map[string]struct{})

	for _, path := range sqlFiles {
		body, err := fs.ReadFile(fsys, path)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", path, err)
		}
		tbls, idxs, explicitSchemas, err := parseDDL(string(body))
		if err != nil {
			// File may contain only comments (e.g. -- DROP TABLE ... directive); treat as 0 tables.
			// TODO: detect specific error, so syntax errors are still reported.
			continue
		}
		for ns := range explicitSchemas {
			schemas[ns] = struct{}{}
		}
		for _, t := range tbls {
			key := tableKey(t.Schema, t.Name)
			tables[key] = t
		}
		for _, idx := range idxs {
			key := indexKey(idx.Schema, idx.Name)
			indexes[key] = idx
		}
	}

	slog.Debug("Loaded schema",
		"schemas", slices.Collect(maps.Keys(schemas)),
		"tables", slices.Collect(maps.Keys(tables)),
		"indexes", slices.Collect(maps.Keys(indexes)),
	)

	return &LoadResult{Schemas: schemas, Tables: tables, Indexes: indexes}, nil
}

// LoadAllowDropTableDefs reads all *.sql files in fsys, finds "-- DROP TABLE schema.tablename ("
// ... "-- );" blocks, converts each to CREATE TABLE and parses to get expected table definitions.
// Returns a map keyed by table key (e.g. "public.events"); only successfully parsed blocks are included.
// Last occurrence wins if the same table appears in multiple blocks.
func LoadAllowDropTableDefs(fsys fs.FS) (map[string]*Table, error) {
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return nil, fmt.Errorf("read schema dir: %w", err)
	}

	var sqlFiles []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if strings.HasSuffix(strings.ToLower(e.Name()), ".sql") {
			sqlFiles = append(sqlFiles, e.Name())
		}
	}
	sort.Strings(sqlFiles)

	out := make(map[string]*Table)
	for _, path := range sqlFiles {
		body, err := fs.ReadFile(fsys, path)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", path, err)
		}
		defs := extractDropTableBlockDefs(string(body))
		for k, t := range defs {
			out[k] = t
		}
	}
	return out, nil
}

// extractDropTableBlockDefs finds "-- DROP TABLE ... (" ... "-- );" blocks in rawSQL,
// converts each to "CREATE TABLE ..." and parses; returns map of table key -> expected definition.
// Malformed or unparseable blocks are skipped.
func extractDropTableBlockDefs(rawSQL string) map[string]*Table {
	lines := strings.Split(rawSQL, "\n")
	out := make(map[string]*Table)
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		if !dropTableCommentRE.MatchString(line) {
			continue
		}
		start := i
		end := -1
		for j := i + 1; j < len(lines); j++ {
			if dropTableBlockEndRE.MatchString(lines[j]) {
				end = j
				break
			}
		}
		if end < 0 {
			continue
		}
		blockLines := lines[start : end+1]
		var sb strings.Builder
		for _, l := range blockLines {
			trimmed := strings.TrimPrefix(l, "--")
			trimmed = strings.TrimPrefix(trimmed, " ")
			sb.WriteString(trimmed)
			sb.WriteString("\n")
		}
		createSQL := strings.Replace(sb.String(), "DROP TABLE", "CREATE TABLE", 1)
		tbls, _, _, err := parseDDL(createSQL)
		if err != nil || len(tbls) == 0 {
			continue
		}
		t := tbls[0]
		key := tableKey(t.Schema, t.Name)
		out[key] = t
		i = end
	}
	return out
}

func tableKey(schema, name string) string {
	if schema == "" {
		schema = "public"
	}
	return schema + "." + name
}

// IndexKey returns the map key for an index (schema.indexname).
func IndexKey(schema, indexName string) string {
	if schema == "" {
		schema = "public"
	}
	return schema + "." + indexName
}

func indexKey(schema, name string) string {
	return IndexKey(schema, name)
}

// removedDirectiveRE matches "-- removed: colname type" or "-- removed: colname ANY_TYPE"
var removedDirectiveRE = regexp.MustCompile(`(?m)^\s*--\s*removed:\s*(\w+)\s+(\S+(?:\s+\S+)*)\s*$`)

// dropTableCommentRE matches a comment line that starts with "-- DROP TABLE schema.tablename (" (the directive format).
var dropTableCommentRE = regexp.MustCompile(`(?m)^\s*--\s*DROP TABLE\s+(\w+(?:\.\w+)?)\s*\(`)

// dropTableBlockEndRE matches the closing line of a DROP TABLE block: "-- );"
var dropTableBlockEndRE = regexp.MustCompile(`^\s*--\s*\)\s*;?\s*$`)

// createSchemaRE matches CREATE SCHEMA [IF NOT EXISTS] name (quoted or unquoted).
var createSchemaRE = regexp.MustCompile(`(?is)CREATE\s+SCHEMA\s+(?:IF\s+NOT\s+EXISTS\s+)?(?:"([^"]+)"|([a-zA-Z_][a-zA-Z0-9_$]*))`)

func extractCreateSchemas(sql string) map[string]struct{} {
	out := make(map[string]struct{})
	for _, sub := range createSchemaRE.FindAllStringSubmatch(sql, -1) {
		if len(sub) < 3 {
			continue
		}
		name := strings.TrimSpace(sub[1])
		if name == "" {
			name = strings.TrimSpace(sub[2])
		}
		name = strings.ToLower(name)
		if name == "" || IsSystemNamespace(name) {
			continue
		}
		out[name] = struct{}{}
	}
	return out
}

func extractRemovedDirectives(rawSQL string) []AllowDropColumn {
	var out []AllowDropColumn
	for _, sub := range removedDirectiveRE.FindAllStringSubmatch(rawSQL, -1) {
		if len(sub) != 3 {
			continue
		}
		colName := strings.TrimSpace(sub[1])
		// Type may have trailing ")", ",", or newline/space from next line of SQL
		typeStr := strings.TrimSpace(strings.TrimRight(strings.TrimSpace(sub[2]), "),"))
		if colName == "" {
			continue
		}
		allowType := typeStr
		if strings.ToUpper(typeStr) != "ANY_TYPE" {
			allowType = normalizeType(typeStr)
		} else {
			allowType = "ANY_TYPE"
		}
		out = append(out, AllowDropColumn{Name: colName, Type: allowType})
	}
	return out
}

func parseDDL(sql string) ([]*Table, []*Index, map[string]struct{}, error) {
	batch, err := postgresparser.ParseSQLAll(sql)
	if err != nil {
		return nil, nil, nil, err
	}

	var tables []*Table
	var indexes []*Index
	explicitSchemas := extractCreateSchemas(sql)
	for _, stmt := range batch.Statements {
		if stmt.Query == nil {
			for ns := range extractCreateSchemas(stmt.RawSQL) {
				explicitSchemas[ns] = struct{}{}
			}
			continue
		}
		for ns := range extractCreateSchemas(stmt.RawSQL) {
			explicitSchemas[ns] = struct{}{}
		}
		allowDrops := extractRemovedDirectives(stmt.RawSQL)
		for _, action := range stmt.Query.DDLActions {
			switch action.Type {
			case postgresparser.DDLCreateTable:
				t := &Table{
					Schema:           action.Schema,
					Name:             strings.ToLower(action.ObjectName),
					Columns:          make([]Column, 0, len(action.ColumnDetails)),
					AllowDropColumns: allowDrops,
				}
				if t.Schema == "" {
					t.Schema = "public"
				}
				for _, c := range action.ColumnDetails {
					t.Columns = append(t.Columns, Column{
						Name:     strings.ToLower(c.Name),
						Type:     normalizeType(c.Type),
						Nullable: c.Nullable,
						Default:  c.Default,
					})
				}
				if action.Constraints != nil {
					if action.Constraints.PrimaryKey != nil {
						t.PrimaryKey = &PrimaryKeyConstraint{
							Name:    action.Constraints.PrimaryKey.ConstraintName,
							Columns: lowerAll(action.Constraints.PrimaryKey.Columns),
						}
					}
					for _, u := range action.Constraints.UniqueKeys {
						cols := lowerAll(u.Columns)
						name := u.ConstraintName
						if name == "" {
							name = helpers.PredictedUniqueConstraintName(t.Name, cols)
						}
						t.UniqueKeys = append(t.UniqueKeys, UniqueConstraint{
							Name:    name,
							Columns: cols,
						})
					}
					for _, fk := range action.Constraints.ForeignKeys {
						cols := lowerAll(fk.Columns)
						name := fk.ConstraintName
						if name == "" {
							name = helpers.PredictedForeignKeyConstraintName(t.Name, cols)
						}
						t.ForeignKeys = append(t.ForeignKeys, ForeignKey{
							Name:              name,
							Columns:           cols,
							ReferencesSchema:  fk.ReferencesSchema,
							ReferencesTable:   fk.ReferencesTable,
							ReferencesColumns: lowerAll(fk.ReferencesColumns),
							OnDelete:          string(fk.OnDelete),
							OnUpdate:          string(fk.OnUpdate),
						})
					}
				}
				tables = append(tables, t)
			case postgresparser.DDLCreateIndex:
				// Table is in the same statement's Tables.
				if len(stmt.Query.Tables) == 0 {
					continue
				}
				tbl := stmt.Query.Tables[0]
				idxSchema := action.Schema
				if idxSchema == "" {
					idxSchema = tbl.Schema
				}
				if idxSchema == "" {
					idxSchema = "public"
				}
				concurrent := false
				for _, f := range action.Flags {
					if f == "CONCURRENTLY" {
						concurrent = true
						break
					}
				}
				if !concurrent {
					return nil, nil, nil, fmt.Errorf("CREATE INDEX %s.%s must use CONCURRENTLY (required for large-table safety)", idxSchema, action.ObjectName)
				}
				idxType := action.IndexType
				if idxType == "" {
					idxType = "btree"
				}
				unique := false
				for _, f := range action.Flags {
					if f == "UNIQUE" {
						unique = true
						break
					}
				}
				if action.ObjectName == "" {
					return nil, nil, fmt.Errorf("CREATE INDEX CONCURRENTLY on table %s.%s requires an explicit name (schemify rewrites it as IF NOT EXISTS <name>)", idxSchema, strings.ToLower(tbl.Name))
				}
				indexes = append(indexes, &Index{
					Name:         strings.ToLower(action.ObjectName),
					Schema:       idxSchema,
					TableSchema:  strings.ToLower(tbl.Schema),
					TableName:    strings.ToLower(tbl.Name),
					Columns:      append([]string(nil), action.Columns...),
					Unique:       unique,
					IndexType:    idxType,
					Concurrently: true,
				})
			}
		}
	}
	return tables, indexes, explicitSchemas, nil
}

// lowerAll returns a new slice with each element lowercased.
func lowerAll(ss []string) []string {
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = strings.ToLower(s)
	}
	return out
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
