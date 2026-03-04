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
