package diff

import (
	"fmt"
	"sort"
	"strings"

	"github.com/earlye/schemify/internal/schema"
)

// TablesMatchForDrop returns true if actual and expected have the same columns (name + type).
// Order does not matter. Used to allow drop only when the directive's column list matches the DB.
func TablesMatchForDrop(actual, expected *schema.Table) bool {
	if actual == nil || expected == nil {
		return false
	}
	mk := func(cols []schema.Column) []string {
		keys := make([]string, 0, len(cols))
		for _, c := range cols {
			keys = append(keys, c.Name+":"+c.Type)
		}
		sort.Strings(keys)
		return keys
	}
	a, e := mk(actual.Columns), mk(expected.Columns)
	if len(a) != len(e) {
		return false
	}
	for i := range a {
		if a[i] != e[i] {
			return false
		}
	}
	return true
}

// Migration represents a single change to apply (additive or allowed destructive).
type Migration struct {
	Kind     string         // "create_table", "add_column", "drop_column", or "drop_table"
	Schema   string         // e.g. "public"
	Table    string         // table name
	TableDef *schema.Table  // for create_table: full definition
	Column   *schema.Column // for add_column: the column to add; for drop_column: Name only
}

// DestructiveChange represents a change we refuse to apply (drop table or column).
type DestructiveChange struct {
	Kind   string // "drop_table" or "drop_column"
	Schema string
	Table  string
	Column string // only for drop_column
	Detail string // optional; e.g. column drift when drop_table rejected due to mismatch
}

func (d DestructiveChange) String() string {
	if d.Kind == "drop_table" {
		s := fmt.Sprintf("table %s.%s would be dropped", d.Schema, d.Table)
		if d.Detail != "" {
			s += ": " + d.Detail
		}
		return s
	}
	return fmt.Sprintf("column %s.%s.%s would be dropped", d.Schema, d.Table, d.Column)
}

// describeColumnDrift returns a short message describing how actual and expected columns differ.
func describeColumnDrift(actual, expected *schema.Table) string {
	actualSet := make(map[string]string) // name -> type
	for _, c := range actual.Columns {
		actualSet[c.Name] = c.Type
	}
	expectedSet := make(map[string]string)
	for _, c := range expected.Columns {
		expectedSet[c.Name] = c.Type
	}
	var inDBNotDirective, inDirectiveNotDB, typeMismatch []string
	for name, typ := range actualSet {
		expTyp, ok := expectedSet[name]
		if !ok {
			inDBNotDirective = append(inDBNotDirective, name)
			continue
		}
		if typ != expTyp {
			typeMismatch = append(typeMismatch, fmt.Sprintf("%s (DB %s vs directive %s)", name, typ, expTyp))
		}
	}
	for name := range expectedSet {
		if _, ok := actualSet[name]; !ok {
			inDirectiveNotDB = append(inDirectiveNotDB, name)
		}
	}
	sort.Strings(inDBNotDirective)
	sort.Strings(inDirectiveNotDB)
	sort.Strings(typeMismatch)
	var parts []string
	if len(inDBNotDirective) > 0 {
		parts = append(parts, fmt.Sprintf("in DB but not in table drop directive: %s", strings.Join(inDBNotDirective, ", ")))
	}
	if len(inDirectiveNotDB) > 0 {
		parts = append(parts, fmt.Sprintf("in table drop directive but not in DB: %s", strings.Join(inDirectiveNotDB, ", ")))
	}
	if len(typeMismatch) > 0 {
		parts = append(parts, fmt.Sprintf("type mismatch: %s", strings.Join(typeMismatch, "; ")))
	}
	if len(parts) == 0 {
		return "column list mismatch"
	}
	return strings.Join(parts, "; ")
}

// Diff compares desired schema (from SQL files) with actual (from DB).
// allowDropTableDefs is the map of table key -> expected definition from "-- DROP TABLE ... (" blocks;
// a table in actual but not desired is dropped only if its key is in the map and TablesMatchForDrop(actual, expected).
// Returns additive migrations to apply and any destructive changes (which must not be applied).
func Diff(desired, actual map[string]*schema.Table, allowDropTableDefs map[string]*schema.Table) (additive []Migration, destructive []DestructiveChange) {
	// Tables in actual but not in desired -> drop table (migration if allowed and columns match, else destructive)
	for key, t := range actual {
		if desired[key] == nil {
			expected := allowDropTableDefs[key]
			if expected != nil && TablesMatchForDrop(t, expected) {
				additive = append(additive, Migration{
					Kind:   "drop_table",
					Schema: t.Schema,
					Table:  t.Name,
				})
			} else {
				dc := DestructiveChange{
					Kind:   "drop_table",
					Schema: t.Schema,
					Table:  t.Name,
				}
				if expected != nil {
					dc.Detail = describeColumnDrift(t, expected)
				}
				destructive = append(destructive, dc)
			}
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
		// Table exists: find columns in actual but not in desired -> drop column (destructive unless allowed)
		wantCols := make(map[string]bool)
		for _, c := range want.Columns {
			wantCols[c.Name] = true
		}
		allowDropByCol := make(map[string]schema.AllowDropColumn)
		for _, a := range want.AllowDropColumns {
			allowDropByCol[a.Name] = a
		}
		for _, c := range have.Columns {
			if !wantCols[c.Name] {
				allow, ok := allowDropByCol[c.Name]
				if !ok {
					destructive = append(destructive, DestructiveChange{
						Kind:   "drop_column",
						Schema: want.Schema,
						Table:  want.Name,
						Column: c.Name,
					})
					continue
				}
				// Allowed only if type matches exactly or directive is ANY_TYPE
				actualType := c.Type
				if allow.Type != "ANY_TYPE" && allow.Type != actualType {
					destructive = append(destructive, DestructiveChange{
						Kind:   "drop_column",
						Schema: want.Schema,
						Table:  want.Name,
						Column: c.Name,
					})
					continue
				}
				additive = append(additive, Migration{
					Kind:   "drop_column",
					Schema: want.Schema,
					Table:  want.Name,
					Column: &schema.Column{Name: c.Name},
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
