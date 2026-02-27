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

	add, dest := Diff(desired, actual, nil, nil, nil)
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

	add, dest := Diff(desired, actual, nil, nil, nil)
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

	add, dest := Diff(desired, actual, nil, nil, nil)
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

	add, dest := Diff(desired, actual, nil, nil, nil)
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

func TestDiff_DropTable_Allowed(t *testing.T) {
	desired := map[string]*schema.Table{
		"public.a": {Schema: "public", Name: "a", Columns: []schema.Column{{Name: "id", Type: "integer"}}},
	}
	actual := map[string]*schema.Table{
		"public.a": {Schema: "public", Name: "a", Columns: []schema.Column{{Name: "id", Type: "integer"}}},
		"public.b": {Schema: "public", Name: "b", Columns: []schema.Column{{Name: "id", Type: "integer"}}},
	}
	allowDropTableDefs := map[string]*schema.Table{
		"public.b": {Schema: "public", Name: "b", Columns: []schema.Column{{Name: "id", Type: "integer"}}},
	}

	add, dest := Diff(desired, actual, nil, nil, allowDropTableDefs)
	if len(dest) != 0 {
		t.Fatalf("expected no destructive, got %v", dest)
	}
	if len(add) != 1 || add[0].Kind != "drop_table" || add[0].Table != "b" {
		t.Errorf("expected one drop_table migration for b, got %v", add)
	}
}

func TestDiff_DropTable_ColumnMismatch_Destructive(t *testing.T) {
	desired := map[string]*schema.Table{
		"public.a": {Schema: "public", Name: "a", Columns: []schema.Column{{Name: "id", Type: "integer"}}},
	}
	actual := map[string]*schema.Table{
		"public.a": {Schema: "public", Name: "a", Columns: []schema.Column{{Name: "id", Type: "integer"}}},
		"public.b": {Schema: "public", Name: "b", Columns: []schema.Column{{Name: "id", Type: "integer"}, {Name: "name", Type: "character varying(255)"}}},
	}
	// Directive says b has only id; actual has id + name -> mismatch
	allowDropTableDefs := map[string]*schema.Table{
		"public.b": {Schema: "public", Name: "b", Columns: []schema.Column{{Name: "id", Type: "integer"}}},
	}

	add, dest := Diff(desired, actual, nil, nil, allowDropTableDefs)
	if len(add) != 0 {
		t.Errorf("expected no additive, got %v", add)
	}
	if len(dest) != 1 || dest[0].Kind != "drop_table" || dest[0].Table != "b" {
		t.Errorf("expected one destructive drop_table (column mismatch), got add=%v dest=%v", add, dest)
	}
}

func TestTablesMatchForDrop(t *testing.T) {
	a := &schema.Table{Schema: "public", Name: "a", Columns: []schema.Column{{Name: "id", Type: "integer"}, {Name: "x", Type: "text"}}}
	b := &schema.Table{Schema: "public", Name: "b", Columns: []schema.Column{{Name: "id", Type: "integer"}, {Name: "x", Type: "text"}}}
	if !TablesMatchForDrop(a, b) {
		t.Error("expected match: same columns")
	}
	b2 := &schema.Table{Schema: "public", Name: "b", Columns: []schema.Column{{Name: "id", Type: "integer"}}}
	if TablesMatchForDrop(a, b2) {
		t.Error("expected no match: different column count")
	}
	b3 := &schema.Table{Schema: "public", Name: "b", Columns: []schema.Column{{Name: "id", Type: "integer"}, {Name: "x", Type: "integer"}}}
	if TablesMatchForDrop(a, b3) {
		t.Error("expected no match: different type for x")
	}
}

func TestDiff_DropColumn_AllowedByTypeMatch(t *testing.T) {
	desired := map[string]*schema.Table{
		"public.a": {
			Schema:  "public",
			Name:    "a",
			Columns: []schema.Column{{Name: "id", Type: "integer"}},
			AllowDropColumns: []schema.AllowDropColumn{{Name: "name", Type: "character varying(255)"}},
		},
	}
	actual := map[string]*schema.Table{
		"public.a": {
			Schema:  "public",
			Name:    "a",
			Columns: []schema.Column{{Name: "id", Type: "integer"}, {Name: "name", Type: "character varying(255)"}},
		},
	}

	add, dest := Diff(desired, actual, nil, nil, nil)
	if len(dest) != 0 {
		t.Fatalf("expected no destructive, got %v", dest)
	}
	if len(add) != 1 {
		t.Fatalf("expected 1 additive (drop_column), got %d", len(add))
	}
	if add[0].Kind != "drop_column" || add[0].Column.Name != "name" {
		t.Errorf("add[0]: %+v", add[0])
	}
}

func TestDiff_DropColumn_AllowedByAnyType(t *testing.T) {
	desired := map[string]*schema.Table{
		"public.a": {
			Schema:  "public",
			Name:    "a",
			Columns: []schema.Column{{Name: "id", Type: "integer"}},
			AllowDropColumns: []schema.AllowDropColumn{{Name: "name", Type: "ANY_TYPE"}},
		},
	}
	actual := map[string]*schema.Table{
		"public.a": {
			Schema:  "public",
			Name:    "a",
			Columns: []schema.Column{{Name: "id", Type: "integer"}, {Name: "name", Type: "text"}},
		},
	}

	add, dest := Diff(desired, actual, nil, nil, nil)
	if len(dest) != 0 {
		t.Fatalf("expected no destructive, got %v", dest)
	}
	if len(add) != 1 || add[0].Kind != "drop_column" || add[0].Column.Name != "name" {
		t.Errorf("expected one drop_column migration, got %v", add)
	}
}

func TestDiff_Index_CreateIndex(t *testing.T) {
	desiredIdx := map[string]*schema.Index{
		"public.idx_a_x": {
			Name: "idx_a_x", Schema: "public", TableSchema: "public", TableName: "a",
			Columns: []string{"x"}, Unique: false, IndexType: "btree", Concurrently: true,
		},
	}
	actualIdx := map[string]*schema.Index{}

	add, dest := Diff(
		map[string]*schema.Table{"public.a": {Schema: "public", Name: "a", Columns: []schema.Column{{Name: "id", Type: "integer"}}}},
		map[string]*schema.Table{"public.a": {Schema: "public", Name: "a", Columns: []schema.Column{{Name: "id", Type: "integer"}}}},
		desiredIdx, actualIdx, nil)
	if len(dest) != 0 {
		t.Fatalf("expected no destructive, got %v", dest)
	}
	if len(add) != 1 {
		t.Fatalf("expected 1 additive (create_index), got %d", len(add))
	}
	if add[0].Kind != "create_index" || add[0].Index.Name != "idx_a_x" {
		t.Errorf("add[0]: %+v", add[0])
	}
}

func TestDiff_AddConstraint_PrimaryKey(t *testing.T) {
	desired := map[string]*schema.Table{
		"public.a": {
			Schema:  "public",
			Name:    "a",
			Columns: []schema.Column{{Name: "id", Type: "integer"}},
			PrimaryKey: &schema.PrimaryKeyConstraint{Name: "a_pkey", Columns: []string{"id"}},
		},
	}
	actual := map[string]*schema.Table{
		"public.a": {
			Schema:  "public",
			Name:    "a",
			Columns: []schema.Column{{Name: "id", Type: "integer"}},
		},
	}

	add, dest := Diff(desired, actual, nil, nil, nil)
	if len(dest) != 0 {
		t.Fatalf("expected no destructive, got %v", dest)
	}
	var addConstraint *Migration
	for i := range add {
		if add[i].Kind == "add_constraint" && add[i].PrimaryKey != nil {
			addConstraint = &add[i]
			break
		}
	}
	if addConstraint == nil {
		t.Fatalf("expected one add_constraint (primary key), got %v", add)
	}
	if addConstraint.Table != "a" || len(addConstraint.PrimaryKey.Columns) != 1 || addConstraint.PrimaryKey.Columns[0] != "id" {
		t.Errorf("add_constraint: %+v", addConstraint)
	}
}

func TestDiff_Index_DropIndex_Destructive(t *testing.T) {
	desiredIdx := map[string]*schema.Index{}
	actualIdx := map[string]*schema.Index{
		"public.idx_a_x": {
			Name: "idx_a_x", Schema: "public", TableSchema: "public", TableName: "a",
			Columns: []string{"x"}, Unique: false, IndexType: "btree", Concurrently: true,
		},
	}

	add, dest := Diff(
		map[string]*schema.Table{"public.a": {Schema: "public", Name: "a", Columns: []schema.Column{{Name: "id", Type: "integer"}}}},
		map[string]*schema.Table{"public.a": {Schema: "public", Name: "a", Columns: []schema.Column{{Name: "id", Type: "integer"}}}},
		desiredIdx, actualIdx, nil)
	if len(add) != 0 {
		t.Errorf("expected no additive, got %v", add)
	}
	if len(dest) != 1 {
		t.Fatalf("expected 1 destructive (drop_index), got %d", len(dest))
	}
	if dest[0].Kind != "drop_index" || dest[0].Index != "idx_a_x" {
		t.Errorf("dest[0]: %+v", dest[0])
	}
}

func TestDiff_DropColumn_TypeMismatch_StillDestructive(t *testing.T) {
	desired := map[string]*schema.Table{
		"public.a": {
			Schema:  "public",
			Name:    "a",
			Columns: []schema.Column{{Name: "id", Type: "integer"}},
			AllowDropColumns: []schema.AllowDropColumn{{Name: "name", Type: "integer"}}, // actual is varchar
		},
	}
	actual := map[string]*schema.Table{
		"public.a": {
			Schema:  "public",
			Name:    "a",
			Columns: []schema.Column{{Name: "id", Type: "integer"}, {Name: "name", Type: "character varying(255)"}},
		},
	}

	add, dest := Diff(desired, actual, nil, nil, nil)
	if len(add) != 0 {
		t.Errorf("expected no additive, got %v", add)
	}
	if len(dest) != 1 || dest[0].Kind != "drop_column" || dest[0].Column != "name" {
		t.Errorf("expected one destructive drop_column, got add=%v dest=%v", add, dest)
	}
}
