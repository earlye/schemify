package diff

import (
	"testing"

	"github.com/earlye/schemify/internal/schema"
)

func TestDiff_AddTable(t *testing.T) {
	desired := map[string]*schema.Table{
		"public.a": {Schema: "public", Name: "a", Columns: []schema.Column{{Name: "id", Type: "integer"}}},
	}
	actual := map[string]*schema.Table{}

	add, dest := Diff(desired, actual)
	if len(dest) != 0 {
		t.Fatalf("expected no destructive, got %v", dest)
	}
	if len(add) != 1 {
		t.Fatalf("expected 1 additive, got %d", len(add))
	}
	if add[0].Kind != "create_table" || add[0].Table != "a" {
		t.Errorf("add[0]: %+v", add[0])
	}
}

func TestDiff_AddColumn(t *testing.T) {
	desired := map[string]*schema.Table{
		"public.a": {
			Schema:  "public",
			Name:    "a",
			Columns: []schema.Column{{Name: "id", Type: "integer"}, {Name: "name", Type: "character varying(255)"}},
		},
	}
	actual := map[string]*schema.Table{
		"public.a": {
			Schema:  "public",
			Name:    "a",
			Columns: []schema.Column{{Name: "id", Type: "integer"}},
		},
	}

	add, dest := Diff(desired, actual)
	if len(dest) != 0 {
		t.Fatalf("expected no destructive, got %v", dest)
	}
	if len(add) != 1 {
		t.Fatalf("expected 1 additive, got %d", len(add))
	}
	if add[0].Kind != "add_column" || add[0].Column.Name != "name" {
		t.Errorf("add[0]: %+v", add[0])
	}
}

func TestDiff_DropColumn_Destructive(t *testing.T) {
	desired := map[string]*schema.Table{
		"public.a": {
			Schema:  "public",
			Name:    "a",
			Columns: []schema.Column{{Name: "id", Type: "integer"}},
		},
	}
	actual := map[string]*schema.Table{
		"public.a": {
			Schema:  "public",
			Name:    "a",
			Columns: []schema.Column{{Name: "id", Type: "integer"}, {Name: "name", Type: "character varying(255)"}},
		},
	}

	add, dest := Diff(desired, actual)
	if len(add) != 0 {
		t.Errorf("expected no additive, got %v", add)
	}
	if len(dest) != 1 {
		t.Fatalf("expected 1 destructive, got %d", len(dest))
	}
	if dest[0].Kind != "drop_column" || dest[0].Column != "name" {
		t.Errorf("dest[0]: %+v", dest[0])
	}
}

func TestDiff_DropTable_Destructive(t *testing.T) {
	desired := map[string]*schema.Table{}
	actual := map[string]*schema.Table{
		"public.a": {Schema: "public", Name: "a", Columns: []schema.Column{{Name: "id", Type: "integer"}}},
	}

	add, dest := Diff(desired, actual)
	if len(add) != 0 {
		t.Errorf("expected no additive, got %v", add)
	}
	if len(dest) != 1 {
		t.Fatalf("expected 1 destructive, got %d", len(dest))
	}
	if dest[0].Kind != "drop_table" || dest[0].Table != "a" {
		t.Errorf("dest[0]: %+v", dest[0])
	}
}
