package schema

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadFromDir(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.sql", "CREATE TABLE public.users (id integer, username character varying(255));")
	writeFile(t, dir, "b.sql", "CREATE TABLE public.events (id integer, event character varying(255));")

	got, err := LoadFromDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 tables, got %d", len(got))
	}
	users, ok := got["public.users"]
	if !ok {
		t.Fatal("missing public.users")
	}
	if users.Name != "users" || users.Schema != "public" {
		t.Errorf("users: schema=%q name=%q", users.Schema, users.Name)
	}
	if len(users.Columns) != 2 {
		t.Fatalf("users: expected 2 columns, got %d", len(users.Columns))
	}
	events, ok := got["public.events"]
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
	tables, err := parseDDL(sql)
	if err != nil {
		t.Fatal(err)
	}
	if len(tables) != 1 {
		t.Fatalf("expected 1 table, got %d", len(tables))
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
	_, err := LoadFromDir("/nonexistent-schema-dir-xyz")
	if err == nil {
		t.Fatal("expected error for nonexistent dir")
	}
}

func TestLoadFromDir_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	got, err := LoadFromDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("expected 0 tables, got %d", len(got))
	}
}
