package schema

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
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
			// File may contain only comments (e.g. -- DROP TABLE ... directive); treat as 0 tables.
			// TODO: detect specific error, so syntax errors are still reported.
			continue
		}
		for _, t := range tables {
			key := tableKey(t.Schema, t.Name)
			desired[key] = t
		}
	}

	return desired, nil
}

// LoadAllowDropTableDefs reads all *.sql files in dir, finds "-- DROP TABLE schema.tablename ("
// ... "-- );" blocks, converts each to CREATE TABLE and parses to get expected table definitions.
// Returns a map keyed by table key (e.g. "public.events"); only successfully parsed blocks are included.
// Last occurrence wins if the same table appears in multiple blocks.
func LoadAllowDropTableDefs(dir string) (map[string]*Table, error) {
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

	out := make(map[string]*Table)
	for _, path := range sqlFiles {
		body, err := os.ReadFile(path)
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
		tables, err := parseDDL(createSQL)
		if err != nil || len(tables) == 0 {
			continue
		}
		t := tables[0]
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

// removedDirectiveRE matches "-- removed: colname type" or "-- removed: colname ANY_TYPE"
var removedDirectiveRE = regexp.MustCompile(`(?m)^\s*--\s*removed:\s*(\w+)\s+(\S+(?:\s+\S+)*)\s*$`)

// dropTableCommentRE matches a comment line that starts with "-- DROP TABLE schema.tablename (" (the directive format).
var dropTableCommentRE = regexp.MustCompile(`(?m)^\s*--\s*DROP TABLE\s+(\w+(?:\.\w+)?)\s*\(`)

// dropTableBlockEndRE matches the closing line of a DROP TABLE block: "-- );"
var dropTableBlockEndRE = regexp.MustCompile(`^\s*--\s*\)\s*;?\s*$`)

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
		allowDrops := extractRemovedDirectives(stmt.RawSQL)
		for _, action := range stmt.Query.DDLActions {
			if action.Type != postgresparser.DDLCreateTable {
				continue
			}
			t := &Table{
				Schema:           action.Schema,
				Name:             action.ObjectName,
				Columns:          make([]Column, 0, len(action.ColumnDetails)),
				AllowDropColumns: allowDrops,
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
