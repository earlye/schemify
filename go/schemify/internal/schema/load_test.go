package schema

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)


func TestLoadFromDir(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.sql", "CREATE TABLE public.users (id integer, username character varying(255));")
	writeFile(t, dir, "b.sql", "CREATE TABLE public.events (id integer, event character varying(255));")

	got, err := LoadFromFS(os.DirFS(dir))
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Tables) != 2 {
		t.Fatalf("expected 2 tables, got %d", len(got.Tables))
	}
	users, ok := got.Tables["public.users"]
	if !ok {
		t.Fatal("missing public.users")
	}
	if users.Name != "users" || users.Schema != "public" {
		t.Errorf("users: schema=%q name=%q", users.Schema, users.Name)
	}
	if len(users.Columns) != 2 {
		t.Fatalf("users: expected 2 columns, got %d", len(users.Columns))
	}
	events, ok := got.Tables["public.events"]
	if !ok {
		t.Fatal("missing public.events")
	}
	if len(events.Columns) != 2 {
		t.Fatalf("events: expected 2 columns, got %d", len(events.Columns))
	}
}

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestParseDDL_SingleTable(t *testing.T) {
	sql := `CREATE TABLE public.foo (id integer, name character varying(100));`
	tables, indexes, err := parseDDL(sql)
	if err != nil {
		t.Fatal(err)
	}
	if len(tables) != 1 {
		t.Fatalf("expected 1 table, got %d", len(tables))
	}
	if len(indexes) != 0 {
		t.Fatalf("expected 0 indexes, got %d", len(indexes))
	}
	tbl := tables[0]
	if tbl.Schema != "public" || tbl.Name != "foo" {
		t.Errorf("table: schema=%q name=%q", tbl.Schema, tbl.Name)
	}
	if len(tbl.Columns) != 2 {
		t.Fatalf("expected 2 columns, got %d", len(tbl.Columns))
	}
	if tbl.Columns[0].Name != "id" || tbl.Columns[0].Type != "integer" {
		t.Errorf("col0: %+v", tbl.Columns[0])
	}
	if tbl.Columns[1].Name != "name" {
		t.Errorf("col1 name: %q", tbl.Columns[1].Name)
	}
}

func TestLoadFromDir_NoSuchDir(t *testing.T) {
	_, err := LoadFromFS(os.DirFS("/nonexistent-schema-dir-xyz"))
	if err == nil {
		t.Fatal("expected error for nonexistent dir")
	}
}

func TestLoadFromDir_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	got, err := LoadFromFS(os.DirFS(dir))
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Tables) != 0 {
		t.Errorf("expected 0 tables, got %d", len(got.Tables))
	}
}

func TestParseDDL_RemovedDirective(t *testing.T) {
	sql := `CREATE TABLE public.users (
    id integer,
    username character varying(255)
    -- removed: passwordhash character varying(64)
);
`
	tables, _, err := parseDDL(sql)
	if err != nil {
		t.Fatal(err)
	}
	if len(tables) != 1 {
		t.Fatalf("expected 1 table, got %d", len(tables))
	}
	tbl := tables[0]
	if len(tbl.AllowDropColumns) != 1 {
		t.Fatalf("expected 1 AllowDropColumn, got %d", len(tbl.AllowDropColumns))
	}
	if tbl.AllowDropColumns[0].Name != "passwordhash" || tbl.AllowDropColumns[0].Type != "character varying(64)" {
		t.Errorf("AllowDropColumns[0]: %+v", tbl.AllowDropColumns[0])
	}
}

func TestParseDDL_RemovedDirectiveAnyType(t *testing.T) {
	sql := `CREATE TABLE public.foo (
    id integer
    -- removed: bar ANY_TYPE
);
`
	tables, _, err := parseDDL(sql)
	if err != nil {
		t.Fatal(err)
	}
	if len(tables) != 1 || len(tables[0].AllowDropColumns) != 1 {
		t.Fatalf("expected 1 table with 1 AllowDropColumn, got %d tables", len(tables))
	}
	if tables[0].AllowDropColumns[0].Name != "bar" || tables[0].AllowDropColumns[0].Type != "ANY_TYPE" {
		t.Errorf("AllowDropColumns[0]: %+v", tables[0].AllowDropColumns[0])
	}
}

func TestLoadAllowDropTableDefs(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.sql", "CREATE TABLE public.users (id integer);\n-- DROP TABLE public.events (\n-- );\n")
	writeFile(t, dir, "b.sql", "-- DROP TABLE public.other (\n-- );\n")

	defs, err := LoadAllowDropTableDefs(os.DirFS(dir))
	if err != nil {
		t.Fatal(err)
	}
	if len(defs) != 2 {
		t.Fatalf("expected 2 entries, got %v", defs)
	}
	if _, ok := defs["public.events"]; !ok {
		t.Errorf("missing public.events")
	}
	if _, ok := defs["public.other"]; !ok {
		t.Errorf("missing public.other")
	}
	// Empty column list for "-- );" only blocks
	if len(defs["public.events"].Columns) != 0 {
		t.Errorf("public.events: expected 0 columns, got %d", len(defs["public.events"].Columns))
	}
}

// TestLoadFromDir_CommentOnlyFile verifies that a file containing only comments
// (e.g. -- DROP TABLE ... directive) does not fail the load; it contributes 0 tables.
func TestLoadFromDir_CommentOnlyFile(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "comments_only.sql", "-- DROP TABLE public.foo (\n-- );")
	writeFile(t, dir, "real.sql", "CREATE TABLE public.bar (id integer);")

	got, err := LoadFromFS(os.DirFS(dir))
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Tables) != 1 {
		t.Fatalf("expected 1 table (bar), got %d", len(got.Tables))
	}
	if _, ok := got.Tables["public.bar"]; !ok {
		t.Errorf("missing public.bar, got %v", got.Tables)
	}
}

// TestParseDDL_CreateIndexConcurrently verifies that CREATE INDEX CONCURRENTLY is parsed and CONCURRENTLY is required.
func TestParseDDL_CreateIndexConcurrently(t *testing.T) {
	sql := `CREATE TABLE public.users (id integer, username character varying(255));
CREATE INDEX CONCURRENTLY idx_users_username ON public.users (username);`
	tables, indexes, err := parseDDL(sql)
	if err != nil {
		t.Fatal(err)
	}
	if len(tables) != 1 {
		t.Fatalf("expected 1 table, got %d", len(tables))
	}
	if len(indexes) != 1 {
		t.Fatalf("expected 1 index, got %d", len(indexes))
	}
	idx := indexes[0]
	if idx.Name != "idx_users_username" || idx.Schema != "public" {
		t.Errorf("index: name=%q schema=%q", idx.Name, idx.Schema)
	}
	if idx.TableName != "users" || idx.TableSchema != "public" {
		t.Errorf("index table: %s.%s", idx.TableSchema, idx.TableName)
	}
	if len(idx.Columns) != 1 || idx.Columns[0] != "username" {
		t.Errorf("index columns: %v", idx.Columns)
	}
	if !idx.Concurrently {
		t.Error("index must have Concurrently=true")
	}
}

// TestParseDDL_CreateIndexWithoutConcurrentlyFails verifies that CREATE INDEX without CONCURRENTLY returns an error.
func TestParseDDL_CreateIndexWithoutConcurrentlyFails(t *testing.T) {
	sql := `CREATE INDEX idx_users_username ON public.users (username);`
	_, _, err := parseDDL(sql)
	if err == nil {
		t.Fatal("expected error when CREATE INDEX does not use CONCURRENTLY")
	}
	if !strings.Contains(err.Error(), "CONCURRENTLY") {
		t.Errorf("error should mention CONCURRENTLY: %v", err)
	}
}

// TestLoadAllowDropTableDefs_DropTableComment verifies that the "-- DROP TABLE schema.tablename ("
// comment block is parsed and returns expected table definition with columns.
func TestLoadAllowDropTableDefs_DropTableComment(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "events.sql", `-- Note: override destructive drop table
-- DROP TABLE public.events (
--     id integer,
--     event character varying(255)
-- );
`)

	defs, err := LoadAllowDropTableDefs(os.DirFS(dir))
	if err != nil {
		t.Fatal(err)
	}
	if len(defs) != 1 {
		t.Fatalf("expected 1 entry, got %v", defs)
	}
	tbl, ok := defs["public.events"]
	if !ok {
		t.Fatal("missing public.events")
	}
	if len(tbl.Columns) != 2 {
		t.Fatalf("expected 2 columns, got %d", len(tbl.Columns))
	}
	if tbl.Columns[0].Name != "id" || tbl.Columns[0].Type != "integer" {
		t.Errorf("col0: %+v", tbl.Columns[0])
	}
	if tbl.Columns[1].Name != "event" || tbl.Columns[1].Type != "character varying(255)" {
		t.Errorf("col1: %+v", tbl.Columns[1])
	}
}

// TestLoadAllowDropTableDefs_MalformedBlock_ Skipped verifies malformed blocks (no closing -- );) are skipped.
func TestExtractDropTableBlockDefs_NoClosingLine_Skipped(t *testing.T) {
	raw := `-- DROP TABLE public.foo (
--     id integer
`
	defs := extractDropTableBlockDefs(raw)
	if len(defs) != 0 {
		t.Errorf("expected 0 defs when block has no closing -- ); got %v", defs)
	}
}

// TestParseDDL_JSONB_TableWithCheckConstraint verifies that a CREATE TABLE with a jsonb column
// and a CHECK constraint using ->> and -> operators parses without error and yields correct columns.
func TestParseDDL_JSONB_TableWithCheckConstraint(t *testing.T) {
	sql := `CREATE TABLE public.key_docs (
    key   text   NOT NULL,
    doc jsonb  NOT NULL,
    CONSTRAINT key_docs_doc_kind_check CHECK (
        (doc->>'kind') IN ('text', 'pdf', 'json') AND
        (doc->'value') IS NOT NULL
    )
);`
	tables, indexes, err := parseDDL(sql)
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	if len(indexes) != 0 {
		t.Errorf("expected 0 indexes, got %d", len(indexes))
	}
	if len(tables) != 1 {
		t.Fatalf("expected 1 table, got %d", len(tables))
	}
	tbl := tables[0]
	if tbl.Schema != "public" || tbl.Name != "key_docs" {
		t.Errorf("table: schema=%q name=%q", tbl.Schema, tbl.Name)
	}
	if len(tbl.Columns) != 2 {
		t.Fatalf("expected 2 columns, got %d: %v", len(tbl.Columns), tbl.Columns)
	}
	if tbl.Columns[0].Name != "key" || tbl.Columns[0].Type != "text" {
		t.Errorf("col0: %+v", tbl.Columns[0])
	}
	if tbl.Columns[1].Name != "doc" || tbl.Columns[1].Type != "jsonb" {
		t.Errorf("col1: %+v", tbl.Columns[1])
	}
}

// TestParseDDL_JSONB_ExpressionIndex verifies that a CREATE INDEX CONCURRENTLY IF NOT EXISTS
// with an expression column using the ->> operator parses without error, returns 2 columns,
// and preserves the ->> operator literally (no unicode escaping of '>').
func TestParseDDL_JSONB_ExpressionIndex(t *testing.T) {
	sql := `CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_key_docs_key_kind ON public.key_docs (key, (doc->>'kind'));`
	_, indexes, err := parseDDL(sql)
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	if len(indexes) != 1 {
		t.Fatalf("expected 1 index, got %d", len(indexes))
	}
	idx := indexes[0]
	if idx.Name != "idx_key_docs_key_kind" {
		t.Errorf("unexpected index name: %q", idx.Name)
	}
	if idx.TableName != "key_docs" || idx.TableSchema != "public" {
		t.Errorf("index table: schema=%q name=%q", idx.TableSchema, idx.TableName)
	}
	// The index has 2 columns: "key" and the expression "(doc->>'kind')".
	if len(idx.Columns) != 2 {
		t.Fatalf("expected 2 columns (key + expression), got %d: %v", len(idx.Columns), idx.Columns)
	}
	if idx.Columns[0] != "key" {
		t.Errorf("col[0]: expected %q, got %q", "key", idx.Columns[0])
	}
	// Verify the expression column preserves the ->> operator without unicode escaping.
	expr := idx.Columns[1]
	if strings.Contains(expr, `\u003e`) || strings.Contains(expr, `%3E`) {
		t.Errorf("column expression contains unicode-escaped '>': %q", expr)
	}
	if !strings.Contains(expr, "->>") {
		t.Errorf("column expression should contain '->>' operator, got: %q", expr)
	}
}

// TestLoadFromFS_JSONB_FullSchema verifies that the complete user-supplied schema
// (two tables, two indexes, jsonb column, check constraint, expression index, IF NOT EXISTS)
// loads without any file being silently skipped due to a parse error.
// TestParseDDL_ColumnNameCaseNormalized verifies that column and constraint column names are
// normalized to lowercase at parse time. PostgreSQL stores unquoted identifiers in lowercase,
// so a SQL file using mixed-case names like "myKey" must produce "mykey" to match introspection.
func TestParseDDL_ColumnNameCaseNormalized(t *testing.T) {
	sql := `CREATE TABLE public.t (myKey text, PRIMARY KEY (myKey));`
	tables, _, err := parseDDL(sql)
	if err != nil {
		t.Fatal(err)
	}
	if len(tables) != 1 {
		t.Fatalf("expected 1 table, got %d", len(tables))
	}
	tbl := tables[0]
	if len(tbl.Columns) != 1 {
		t.Fatalf("expected 1 column, got %d", len(tbl.Columns))
	}
	if tbl.Columns[0].Name != "mykey" {
		t.Errorf("column name: expected %q, got %q", "mykey", tbl.Columns[0].Name)
	}
	if tbl.PrimaryKey == nil {
		t.Fatal("expected PrimaryKey to be set")
	}
	if len(tbl.PrimaryKey.Columns) != 1 || tbl.PrimaryKey.Columns[0] != "mykey" {
		t.Errorf("PK columns: expected [%q], got %v", "mykey", tbl.PrimaryKey.Columns)
	}
}

// TestParseDDL_JSONB_QuestionMarkCheckConstraint verifies that the JSONB ? operator
// inside a DDL CHECK constraint does not cause the parser to silently drop subsequent
// statements — the regression from valkdb/postgresparser DDL vs DML grammar coverage.
func TestParseDDL_JSONB_QuestionMarkCheckConstraint(t *testing.T) {
	sql := `CREATE TABLE public.owners_channels (
    owner   text   NOT NULL,
    channel jsonb  NOT NULL,
    CONSTRAINT owners_channels_channel_kind_check CHECK (
        channel ? 'kind' AND channel ? 'value' AND (channel->>'kind') IN ('PD', 'DD', 'Slack', 'Fail')
    )
);

CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_owners_channels_owner ON public.owners_channels (owner);

CREATE TABLE public.environment_overrides (
    envKey   text NOT NULL,
    envValue text NOT NULL,
    PRIMARY KEY (envKey)
);`
	tables, indexes, err := parseDDL(sql)
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	if len(tables) != 2 {
		t.Fatalf("expected 2 tables, got %d — statements after ? operator may have been silently dropped", len(tables))
	}
	if len(indexes) != 1 {
		t.Fatalf("expected 1 index, got %d — statements after ? operator may have been silently dropped", len(indexes))
	}
	if tables[0].Name != "owners_channels" {
		t.Errorf("table[0]: expected owners_channels, got %q", tables[0].Name)
	}
	if tables[1].Name != "environment_overrides" {
		t.Errorf("table[1]: expected environment_overrides, got %q", tables[1].Name)
	}
	if indexes[0].Name != "idx_owners_channels_owner" {
		t.Errorf("index: expected idx_owners_channels_owner, got %q", indexes[0].Name)
	}
}

func TestLoadFromFS_JSONB_FullSchema(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "key_docs.sql", `-- Maps keys to delivery docs.
CREATE TABLE public.key_docs (
    key   text   NOT NULL,
    doc jsonb  NOT NULL,
    CONSTRAINT key_docs_doc_kind_check CHECK (
        (doc->>'kind') IN ('text', 'pdf', 'json') AND
        (doc->'value') IS NOT NULL
    )
);

CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_key_docs_key ON public.key_docs (key);
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_key_docs_key_kind ON public.key_docs (key, (doc->>'kind'));
`)
	writeFile(t, dir, "key_value.sql", `CREATE TABLE public.key_value (
    key   text NOT NULL,
    value text NOT NULL,
    PRIMARY KEY(key)
);
`)
	got, err := LoadFromFS(os.DirFS(dir))
	if err != nil {
		t.Fatalf("LoadFromFS error: %v", err)
	}
	if len(got.Tables) != 2 {
		t.Errorf("expected 2 tables, got %d: %v", len(got.Tables), got.Tables)
	}
	if _, ok := got.Tables["public.key_docs"]; !ok {
		t.Error("missing public.key_docs (file may have been silently skipped due to parse error)")
	}
	if _, ok := got.Tables["public.key_value"]; !ok {
		t.Error("missing public.key_value")
	}
	if len(got.Indexes) != 2 {
		t.Errorf("expected 2 indexes, got %d: %v", len(got.Indexes), got.Indexes)
	}
	if _, ok := got.Indexes["public.idx_key_docs_key"]; !ok {
		t.Error("missing public.idx_key_docs_key")
	}
	if _, ok := got.Indexes["public.idx_key_docs_key_kind"]; !ok {
		t.Error("missing public.idx_key_docs_key_kind")
	}
}
