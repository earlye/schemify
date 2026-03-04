package diff

import (
	"fmt"
	"sort"
	"strings"

	"github.com/earlye/schemify/go/schemify/internal/schema"
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
// TODO: migrate this to be a discriminated union so it can be marshaled to JSON/YAML/SQL/etc.
type Migration struct {
	Kind     string         // "create_table", "add_column", "drop_column", "drop_table", "create_index", "add_constraint"
	Schema   string         // e.g. "public"
	Table    string         // table name
	TableDef *schema.Table  // for create_table: full definition
	Column   *schema.Column // for add_column: the column to add; for drop_column: Name only
	Index    *schema.Index  // for create_index
	// For add_constraint: exactly one of PrimaryKey, UniqueKey, ForeignKey is set.
	PrimaryKey *schema.PrimaryKeyConstraint
	UniqueKey  *schema.UniqueConstraint
	ForeignKey *schema.ForeignKey
}

// DestructiveChange represents a change we refuse to apply (drop table, column, index, or pk mismatch).
type DestructiveChange struct {
	Kind   string // "drop_table", "drop_column", "drop_index", or "primary_key_mismatch"
	Schema string
	Table  string
	Column string // only for drop_column
	Index  string // only for drop_index
	Detail string // optional; e.g. column drift when drop_table rejected, or pk drift description
}

func (d DestructiveChange) String() string {
	switch d.Kind {
	case "drop_table":
		s := fmt.Sprintf("table %s.%s would be dropped", d.Schema, d.Table)
		if d.Detail != "" {
			s += ": " + d.Detail
		}
		return s
	case "drop_index":
		return fmt.Sprintf("index %s.%s would be dropped", d.Schema, d.Index)
	case "drop_column":
		return fmt.Sprintf("column %s.%s.%s would be dropped", d.Schema, d.Table, d.Column)
	case "primary_key_mismatch":
		s := fmt.Sprintf("table %s.%s primary key would change", d.Schema, d.Table)
		if d.Detail != "" {
			s += ": " + d.Detail
		}
		return s
	default:
		return fmt.Sprintf("%s %s.%s would be dropped", d.Kind, d.Schema, d.Table)
	}
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

// IndexMatches returns true if actual index matches desired (same columns, unique, type).
func IndexMatches(actual, desired *schema.Index) bool {
	if actual == nil || desired == nil {
		return false
	}
	if actual.Unique != desired.Unique || actual.IndexType != desired.IndexType {
		return false
	}
	if len(actual.Columns) != len(desired.Columns) {
		return false
	}
	for i := range actual.Columns {
		if actual.Columns[i] != desired.Columns[i] {
			return false
		}
	}
	return true
}

// Diff compares desired schema (from SQL files) with actual (from DB), including indexes.
// allowDropTableDefs is the map of table key -> expected definition from "-- DROP TABLE ... (" blocks;
// a table in actual but not desired is dropped only if its key is in the map and TablesMatchForDrop(actual, expected).
// desiredIndexes/actualIndexes can be nil (no index diff). Index in actual but not in desired -> destructive drop_index.
// Returns additive migrations to apply and any destructive changes (which must not be applied).
func Diff(desired, actual map[string]*schema.Table, desiredIndexes, actualIndexes map[string]*schema.Index, allowDropTableDefs map[string]*schema.Table) (migrations []Migration, disallowed []DestructiveChange) {
	// Tables in actual but not in desired -> drop table (migration if allowed and columns match, else destructive)
	for key, t := range actual {
		if desired[key] == nil {
			expected := allowDropTableDefs[key]
			if expected != nil && TablesMatchForDrop(t, expected) {
				migrations = append(migrations, Migration{
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
				disallowed = append(disallowed, dc)
			}
		}
	}

	// For each desired table: create if missing, or add missing columns
	for key, want := range desired {
		have := actual[key]
		if have == nil {
			migrations = append(migrations, Migration{
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
					disallowed = append(disallowed, DestructiveChange{
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
					disallowed = append(disallowed, DestructiveChange{
						Kind:   "drop_column",
						Schema: want.Schema,
						Table:  want.Name,
						Column: c.Name,
					})
					continue
				}
				migrations = append(migrations, Migration{
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
				migrations = append(migrations, Migration{
					Kind:   "add_column",
					Schema: want.Schema,
					Table:  want.Name,
					Column: c,
				})
			}
		}

		// Primary key: any mismatch on an existing table is a destructive change.
		// This includes: desired has a different PK, desired has no PK but live DB does (implicit drop),
		// or desired has a PK but live DB has none.
		if !constraintPKEqual(have.PrimaryKey, want.PrimaryKey) {
			detail := describePKDrift(have.PrimaryKey, want.PrimaryKey)
			disallowed = append(disallowed, DestructiveChange{
				Kind:   "primary_key_mismatch",
				Schema: want.Schema,
				Table:  want.Name,
				Detail: detail,
			})
		}
		for _, u := range want.UniqueKeys {
			if !haveUnique(u, have.UniqueKeys) {
				u2 := u
				migrations = append(migrations, Migration{
					Kind:      "add_constraint",
					Schema:    want.Schema,
					Table:     want.Name,
					UniqueKey: &u2,
				})
			}
		}
		for _, fk := range want.ForeignKeys {
			if !haveFK(fk, have.ForeignKeys) {
				fk2 := fk
				migrations = append(migrations, Migration{
					Kind:       "add_constraint",
					Schema:     want.Schema,
					Table:      want.Name,
					ForeignKey: &fk2,
				})
			}
		}
	}

	// Index diff: desiredIndexes and actualIndexes can be nil
	if desiredIndexes != nil && actualIndexes != nil {
		for key, wantIdx := range desiredIndexes {
			haveIdx := actualIndexes[key]
			if haveIdx == nil || !IndexMatches(haveIdx, wantIdx) {
				migrations = append(migrations, Migration{
					Kind:   "create_index",
					Schema: wantIdx.Schema,
					Table:  wantIdx.TableName,
					Index:  wantIdx,
				})
			}
		}
		for key, haveIdx := range actualIndexes {
			if desiredIndexes[key] == nil {
				disallowed = append(disallowed, DestructiveChange{
					Kind:   "drop_index",
					Schema: haveIdx.Schema,
					Index:  haveIdx.Name,
				})
			}
		}
	}

	return migrations, disallowed
}

func describePKDrift(actual, desired *schema.PrimaryKeyConstraint) string {
	dbCols := "(none)"
	if actual != nil && len(actual.Columns) > 0 {
		dbCols = "(" + strings.Join(actual.Columns, ", ") + ")"
	}
	wantCols := "(none)"
	if desired != nil && len(desired.Columns) > 0 {
		wantCols = "(" + strings.Join(desired.Columns, ", ") + ")"
	}
	return fmt.Sprintf("DB has %s, schema has %s", dbCols, wantCols)
}

func constraintPKEqual(a, b *schema.PrimaryKeyConstraint) bool {
	if a == nil || b == nil {
		return a == b
	}
	if len(a.Columns) != len(b.Columns) {
		return false
	}
	for i := range a.Columns {
		if a.Columns[i] != b.Columns[i] {
			return false
		}
	}
	return true
}

func haveUnique(u schema.UniqueConstraint, list []schema.UniqueConstraint) bool {
	for _, e := range list {
		if e.Name == u.Name && sliceEqual(e.Columns, u.Columns) {
			return true
		}
	}
	return false
}

func haveFK(fk schema.ForeignKey, list []schema.ForeignKey) bool {
	for _, e := range list {
		if e.Name == fk.Name && sliceEqual(e.Columns, fk.Columns) && e.ReferencesSchema == fk.ReferencesSchema && e.ReferencesTable == fk.ReferencesTable && sliceEqual(e.ReferencesColumns, fk.ReferencesColumns) {
			return true
		}
	}
	return false
}

func sliceEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
