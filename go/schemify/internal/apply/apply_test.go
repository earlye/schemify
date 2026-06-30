package apply

import (
	"strings"
	"testing"

	"github.com/earlye/schemify/go/schemify/internal/diff"
	"github.com/earlye/schemify/go/schemify/internal/schema"
)

func TestColumnDef_NotNullDefault(t *testing.T) {
	c := &schema.Column{Name: "status", Type: "text", Nullable: false, Default: "'init'"}
	got := columnDef(c)
	if !strings.Contains(got, "NOT NULL") {
		t.Errorf("expected NOT NULL in %q", got)
	}
	if !strings.Contains(got, "DEFAULT 'init'") {
		t.Errorf("expected DEFAULT 'init' in %q", got)
	}
}

func TestColumnDef_NullableNoDefault(t *testing.T) {
	c := &schema.Column{Name: "status", Type: "text", Nullable: true}
	got := columnDef(c)
	if got != "status text" {
		t.Errorf("expected %q, got %q", "status text", got)
	}
}

func TestAddColumnSQL_IncludesNotNullDefault(t *testing.T) {
	m := diff.Migration{
		Kind:   diff.KindAddColumn,
		Schema: "public",
		Table:  "things",
		Detail: &diff.AddColumnDetail{Column: &schema.Column{Name: "status", Type: "text", Nullable: false, Default: "'init'"}},
	}
	sql, err := migrationSQL(m)
	if err != nil {
		t.Fatalf("migrationSQL: %v", err)
	}
	want := "ALTER TABLE public.things ADD COLUMN status text NOT NULL DEFAULT 'init'"
	if sql != want {
		t.Errorf("got %q, want %q", sql, want)
	}
}

func TestCreateTableSQL_IncludesNotNullDefault(t *testing.T) {
	tbl := &schema.Table{
		Schema: "public",
		Name:   "things",
		Columns: []schema.Column{
			{Name: "id", Type: "text", Nullable: false},
			{Name: "status", Type: "text", Nullable: false, Default: "'init'"},
		},
	}
	sql := createTableSQL(tbl)
	want := "CREATE TABLE public.things (id text NOT NULL, status text NOT NULL DEFAULT 'init')"
	if sql != want {
		t.Errorf("got %q, want %q", sql, want)
	}
}
