package diff

import (
	"fmt"

	"github.com/earlye/schemify/internal/schema"
)

// Migration represents a single additive change to apply.
type Migration struct {
	Kind     string       // "create_table" or "add_column"
	Schema   string       // e.g. "public"
	Table    string       // table name
	TableDef *schema.Table // for create_table: full definition
	Column   *schema.Column // for add_column: the column to add
}

// DestructiveChange represents a change we refuse to apply (drop table or column).
type DestructiveChange struct {
	Kind   string // "drop_table" or "drop_column"
	Schema string
	Table  string
	Column string // only for drop_column
}

func (d DestructiveChange) String() string {
	if d.Kind == "drop_table" {
		return fmt.Sprintf("table %s.%s would be dropped", d.Schema, d.Table)
	}
	return fmt.Sprintf("column %s.%s.%s would be dropped", d.Schema, d.Table, d.Column)
}

// Diff compares desired schema (from SQL files) with actual (from DB).
// Returns additive migrations to apply and any destructive changes (which must not be applied).
func Diff(desired, actual map[string]*schema.Table) (additive []Migration, destructive []DestructiveChange) {
	// Tables in actual but not in desired -> would drop table
	for key, t := range actual {
		if desired[key] == nil {
			destructive = append(destructive, DestructiveChange{
				Kind:   "drop_table",
				Schema: t.Schema,
				Table:  t.Name,
			})
		}
	}

	// For each desired table: create if missing, or add missing columns
	for key, want := range desired {
		have := actual[key]
		if have == nil {
			additive = append(additive, Migration{
				Kind:     "create_table",
				Schema:   want.Schema,
				Table:    want.Name,
				TableDef: want,
			})
			continue
		}
		// Table exists: find columns in actual but not in desired -> drop column (destructive)
		wantCols := make(map[string]bool)
		for _, c := range want.Columns {
			wantCols[c.Name] = true
		}
		for _, c := range have.Columns {
			if !wantCols[c.Name] {
				destructive = append(destructive, DestructiveChange{
					Kind:   "drop_column",
					Schema: want.Schema,
					Table:  want.Name,
					Column: c.Name,
				})
			}
		}
		// Columns in desired but not in actual -> add column
		haveCols := make(map[string]bool)
		for _, c := range have.Columns {
			haveCols[c.Name] = true
		}
		for i := range want.Columns {
			c := &want.Columns[i]
			if !haveCols[c.Name] {
				additive = append(additive, Migration{
					Kind:   "add_column",
					Schema: want.Schema,
					Table:  want.Name,
					Column: c,
				})
			}
		}
	}
	return additive, destructive
}
