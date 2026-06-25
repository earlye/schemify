package schema

import (
	"strings"
	"testing"
)

func TestExtractDriftBlocks_BasicDROP(t *testing.T) {
	sql := "-- DRIFT cleanup1 DROP (\n--   old_col TEXT NOT NULL\n-- )"
	blocks, err := extractDriftBlocks(sql, "public", "things")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(blocks) != 1 {
		t.Fatalf("expected 1 block, got %d", len(blocks))
	}
	b := blocks[0]
	if b.ID != "cleanup1" {
		t.Errorf("expected ID cleanup1, got %s", b.ID)
	}
	if b.Policy != DriftPolicyDrop {
		t.Errorf("expected DROP policy, got %s", b.Policy)
	}
	if b.Scope != DriftScopeTable {
		t.Errorf("expected DriftScopeTable, got %v", b.Scope)
	}
	if !strings.Contains(b.RawBody, "old_col") {
		t.Errorf("expected RawBody to contain old_col, got %q", b.RawBody)
	}
}

func TestExtractDriftBlocks_DEPRECATED(t *testing.T) {
	sql := "-- DRIFT cleanup2 DEPRECATED (\n--   old_col TEXT NOT NULL\n-- )"
	blocks, err := extractDriftBlocks(sql, "public", "things")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(blocks) != 1 {
		t.Fatalf("expected 1 block, got %d", len(blocks))
	}
	if blocks[0].Policy != DriftPolicyDeprecated {
		t.Errorf("expected DEPRECATED policy, got %s", blocks[0].Policy)
	}
}

func TestExtractDriftBlocks_NestedParens(t *testing.T) {
	sql := "-- DRIFT cleanup3 DROP (\n-- CHECK ( x > 0 )\n-- )"
	blocks, err := extractDriftBlocks(sql, "public", "things")
	if err != nil {
		t.Fatalf("unexpected error with nested parens: %v", err)
	}
	if len(blocks) != 1 {
		t.Fatalf("expected 1 block, got %d", len(blocks))
	}
	if !strings.Contains(blocks[0].RawBody, "CHECK") {
		t.Errorf("expected RawBody to contain CHECK, got %q", blocks[0].RawBody)
	}
}

func TestExtractDriftBlocks_SingleQuotedParen(t *testing.T) {
	sql := "-- DRIFT cleanup4 DROP (\n-- CHECK (name != '(')\n-- )"
	blocks, err := extractDriftBlocks(sql, "public", "things")
	if err != nil {
		t.Fatalf("unexpected error with single-quoted paren: %v", err)
	}
	if len(blocks) != 1 {
		t.Fatalf("expected 1 block, got %d", len(blocks))
	}
}

func TestExtractDriftBlocks_Unclosed(t *testing.T) {
	sql := "-- DRIFT cleanup5 DROP (\n--   old_col TEXT NOT NULL"
	_, err := extractDriftBlocks(sql, "public", "things")
	if err == nil {
		t.Fatal("expected error for unclosed block")
	}
	if !strings.Contains(err.Error(), "unclosed") {
		t.Errorf("expected error containing 'unclosed', got %q", err.Error())
	}
}

func TestExtractDriftBlocks_PolicyConflict(t *testing.T) {
	sql := "-- DRIFT cleanup6 DROP (\n--   col1 TEXT\n-- )\n-- DRIFT cleanup6 DEPRECATED (\n--   col2 TEXT\n-- )"
	_, err := extractDriftBlocks(sql, "public", "things")
	if err == nil {
		t.Fatal("expected error for policy conflict")
	}
}

func TestExtractDriftBlocks_SameIDSamePolicy(t *testing.T) {
	sql := "-- DRIFT cleanup7 DROP (\n--   col1 TEXT\n-- )\n-- DRIFT cleanup7 DROP (\n--   col2 TEXT\n-- )"
	blocks, err := extractDriftBlocks(sql, "public", "things")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(blocks) != 2 {
		t.Fatalf("expected 2 blocks, got %d", len(blocks))
	}
}

func TestExtractDriftBlocks_FileScope(t *testing.T) {
	sql := "-- DRIFT cleanup8 DROP (\n-- CREATE INDEX CONCURRENTLY idx_foo ON public.things (col);\n-- )"
	blocks, err := extractDriftBlocks(sql, "", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(blocks) != 1 {
		t.Fatalf("expected 1 block, got %d", len(blocks))
	}
	if blocks[0].Scope != DriftScopeFile {
		t.Errorf("expected DriftScopeFile, got %v", blocks[0].Scope)
	}
}

func TestBuildAnticipatedDrift_Column(t *testing.T) {
	block := &DriftBlock{
		ID:          "cleanup9",
		Policy:      DriftPolicyDrop,
		Scope:       DriftScopeTable,
		TableSchema: "public",
		TableName:   "things",
		RawBody:     "  old_col TEXT NOT NULL",
	}
	if err := buildAnticipatedDrift(block); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if block.AnticipatedTable == nil {
		t.Fatal("expected AnticipatedTable to be set")
	}
	if len(block.AnticipatedTable.Columns) != 1 {
		t.Fatalf("expected 1 column, got %d", len(block.AnticipatedTable.Columns))
	}
	if block.AnticipatedTable.Columns[0].Name != "old_col" {
		t.Errorf("expected column name old_col, got %s", block.AnticipatedTable.Columns[0].Name)
	}
}

func TestBuildAnticipatedDrift_InvalidDDL(t *testing.T) {
	block := &DriftBlock{
		ID:      "cleanup10",
		Policy:  DriftPolicyDrop,
		Scope:   DriftScopeTable,
		RawBody: "NOT VALID SQL @@@@",
	}
	err := buildAnticipatedDrift(block)
	if err == nil {
		t.Fatal("expected error for invalid DDL")
	}
}

func TestBuildAnticipatedDrift_FileScope_Index(t *testing.T) {
	block := &DriftBlock{
		ID:      "cleanup11",
		Policy:  DriftPolicyDrop,
		Scope:   DriftScopeFile,
		RawBody: "CREATE INDEX CONCURRENTLY idx_foo ON public.things (col);",
	}
	if err := buildAnticipatedDrift(block); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(block.AnticipatedIndexes) != 1 {
		t.Fatalf("expected 1 index, got %d", len(block.AnticipatedIndexes))
	}
	if block.AnticipatedIndexes[0].Name != "idx_foo" {
		t.Errorf("expected index name idx_foo, got %s", block.AnticipatedIndexes[0].Name)
	}
}

func TestMergeDriftGroups_Merge(t *testing.T) {
	blocks := []DriftBlock{
		{
			ID:     "cleanup12",
			Policy: DriftPolicyDrop,
			AnticipatedTable: &Table{
				Columns: []Column{{Name: "col1", Type: "text"}},
			},
		},
		{
			ID:     "cleanup12",
			Policy: DriftPolicyDrop,
			AnticipatedTable: &Table{
				Columns: []Column{{Name: "col2", Type: "integer"}},
			},
		},
	}
	groups, err := MergeDriftGroups(blocks)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	g, ok := groups["cleanup12"]
	if !ok {
		t.Fatal("expected group cleanup12")
	}
	if len(g.AnticipatedColumns) != 2 {
		t.Fatalf("expected 2 columns, got %d", len(g.AnticipatedColumns))
	}
}

func TestMergeDriftGroups_PolicyConflict(t *testing.T) {
	blocks := []DriftBlock{
		{ID: "cleanup13", Policy: DriftPolicyDrop},
		{ID: "cleanup13", Policy: DriftPolicyDeprecated},
	}
	_, err := MergeDriftGroups(blocks)
	if err == nil {
		t.Fatal("expected error for policy conflict")
	}
}

func TestBuildAnticipatedDrift_TrailingComma(t *testing.T) {
	block := &DriftBlock{
		ID:          "cleanup14",
		Policy:      DriftPolicyDrop,
		Scope:       DriftScopeTable,
		TableSchema: "public",
		TableName:   "things",
		RawBody:     "  old_col TEXT NOT NULL,",
	}
	if err := buildAnticipatedDrift(block); err != nil {
		t.Fatalf("unexpected error with trailing comma: %v", err)
	}
	if block.AnticipatedTable == nil {
		t.Fatal("expected AnticipatedTable to be set")
	}
	if len(block.AnticipatedTable.Columns) != 1 {
		t.Fatalf("expected 1 column, got %d", len(block.AnticipatedTable.Columns))
	}
}
