package diff

import (
	"cmp"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/earlye/schemify/go/schemify/internal/schema"
)

// indexColTypeCastRE matches PostgreSQL type cast annotations added by pg_get_indexdef
// (e.g. 'kind'::text). These are not present in the source SQL written by the user.
var indexColTypeCastRE = regexp.MustCompile(`::(?:[a-zA-Z_]\w*(?:\s+[a-zA-Z_]\w*)*)`)

// normalizeIndexColumn canonicalizes an index column string for comparison.
// It removes whitespace, PostgreSQL-added type cast annotations, and strips
// matching outer parentheses so that the parser's raw text (e.g. "(channel->>'kind')")
// matches pg_get_indexdef output which may wrap expressions in extra parens
// (e.g. PostgreSQL 18 returns "((channel ->> 'kind'::text))").
func normalizeIndexColumn(col string) string {
	col = strings.Join(strings.Fields(col), "")
	col = indexColTypeCastRE.ReplaceAllString(col, "")
	for {
		stripped, ok := stripOuterParens(col)
		if !ok {
			break
		}
		col = stripped
	}
	return strings.ToLower(col)
}

// stripOuterParens removes one layer of outer parentheses if the opening paren
// at index 0 matches the closing paren at the last index. Returns the stripped
// string and true, or the original string and false if no strip was done.
func stripOuterParens(s string) (string, bool) {
	if len(s) < 2 || s[0] != '(' || s[len(s)-1] != ')' {
		return s, false
	}
	// Walk the string; if depth reaches 0 before the last char, the first '('
	// closes before the end, meaning the outer parens are not a matched pair.
	depth := 0
	for i := 0; i < len(s)-1; i++ {
		if s[i] == '(' {
			depth++
		} else if s[i] == ')' {
			depth--
			if depth == 0 {
				return s, false
			}
		}
	}
	return s[1 : len(s)-1], true
}

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

// MigrationDetail is a sealed interface; only the Detail types in this package implement it.
type MigrationDetail interface{ migrationDetail() }

// Kind constants for Migration.Kind.
const (
	KindCreateSchema = "create_schema"
	KindCreateTable  = "create_table"
	KindAddColumn    = "add_column"
	KindDropColumn   = "drop_column"
	KindDropTable    = "drop_table"
	KindCreateIndex  = "create_index"
	KindAddPK        = "add_primary_key"
	KindAddUnique    = "add_unique_key"
	KindAddFK        = "add_foreign_key"
)

// Detail types — one per Kind.
type CreateSchemaDetail struct{}
type CreateTableDetail struct{ TableDef *schema.Table }
type AddColumnDetail struct{ Column *schema.Column }
type DropColumnDetail struct{ ColumnName string }
type DropTableDetail struct{}
type CreateIndexDetail struct{ Index *schema.Index }
type AddPKDetail struct{ PrimaryKey *schema.PrimaryKeyConstraint }
type AddUniqueDetail struct{ UniqueKey *schema.UniqueConstraint }
type AddFKDetail struct{ ForeignKey *schema.ForeignKey }

func (*CreateSchemaDetail) migrationDetail() {}
func (*CreateTableDetail) migrationDetail()  {}
func (*AddColumnDetail) migrationDetail()   {}
func (*DropColumnDetail) migrationDetail()  {}
func (*DropTableDetail) migrationDetail()   {}
func (*CreateIndexDetail) migrationDetail() {}
func (*AddPKDetail) migrationDetail()       {}
func (*AddUniqueDetail) migrationDetail()   {}
func (*AddFKDetail) migrationDetail()       {}

// Migration represents a single change to apply (additive or allowed destructive).
type Migration struct {
	Kind   string          // one of the Kind* constants
	Schema string          // e.g. "public"
	Table  string          // table name
	Detail MigrationDetail // concrete type determined by Kind
}

// DestructiveChange represents a change we refuse to apply (drop table, column, index, or pk mismatch).
type DestructiveChange struct {
	Kind   string // "drop_table", "drop_column", "drop_index", or "primary_key_mismatch"
	Schema string
	Table  string
	Column string // only for drop_column
	Index  string // only for drop_index
	Name   string // only for named constraints
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
	case "drop_unique_key":
		return fmt.Sprintf("unique constraint %s.%s.%s would be dropped", d.Schema, d.Table, d.Name)
	case "drop_foreign_key":
		return fmt.Sprintf("foreign key %s.%s.%s would be dropped", d.Schema, d.Table, d.Name)
	case "primary_key_mismatch":
		s := fmt.Sprintf("table %s.%s primary key would change", d.Schema, d.Table)
		if d.Detail != "" {
			s += ": " + d.Detail
		}
		return s
	case "drop_schema":
		return fmt.Sprintf("schema %s would be dropped", d.Schema)
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
		if normalizeIndexColumn(actual.Columns[i]) != normalizeIndexColumn(desired.Columns[i]) {
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
func Diff(
	desiredNamespaces, actualNamespaces map[string]struct{},
	desired, actual map[string]*schema.Table,
	desiredIndexes, actualIndexes map[string]*schema.Index,
	allowDropTableDefs map[string]*schema.Table,
) (migrations []Migration, disallowed []DestructiveChange) {
	for ns := range desiredNamespaces {
		if _, ok := actualNamespaces[ns]; !ok {
			migrations = append(migrations, Migration{
				Kind:   KindCreateSchema,
				Schema: ns,
				Detail: &CreateSchemaDetail{},
			})
		}
	}
	for ns := range actualNamespaces {
		if _, ok := desiredNamespaces[ns]; !ok && schema.IsDropSchemaCandidate(ns) {
			disallowed = append(disallowed, DestructiveChange{
				Kind:   "drop_schema",
				Schema: ns,
			})
		}
	}

	// Tables in actual but not in desired -> drop table (migration if allowed and columns match, else destructive)
	for key, t := range actual {
		if desired[key] == nil {
			expected := allowDropTableDefs[key]
			if expected != nil && TablesMatchForDrop(t, expected) {
				migrations = append(migrations, Migration{
					Kind:   KindDropTable,
					Schema: t.Schema,
					Table:  t.Name,
					Detail: &DropTableDetail{},
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
				Kind:   KindCreateTable,
				Schema: want.Schema,
				Table:  want.Name,
				Detail: &CreateTableDetail{TableDef: want},
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
					Kind:   KindDropColumn,
					Schema: want.Schema,
					Table:  want.Name,
					Detail: &DropColumnDetail{ColumnName: c.Name},
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
					Kind:   KindAddColumn,
					Schema: want.Schema,
					Table:  want.Name,
					Detail: &AddColumnDetail{Column: c},
				})
			}
		}

		// Primary key: adding a PK when the DB has none is additive (ALTER ADD PRIMARY KEY).
		// Dropping or changing an existing PK is destructive.
		if have.PrimaryKey == nil && want.PrimaryKey != nil {
			pk := *want.PrimaryKey
			migrations = append(migrations, Migration{
				Kind:   KindAddPK,
				Schema: want.Schema,
				Table:  want.Name,
				Detail: &AddPKDetail{PrimaryKey: &pk},
			})
		} else if have.PrimaryKey != nil && want.PrimaryKey == nil {
			disallowed = append(disallowed, DestructiveChange{
				Kind:   "primary_key_mismatch",
				Schema: want.Schema,
				Table:  want.Name,
				Detail: describePKDrift(have.PrimaryKey, want.PrimaryKey),
			})
		} else if !constraintPKEqual(have.PrimaryKey, want.PrimaryKey) {
			disallowed = append(disallowed, DestructiveChange{
				Kind:   "primary_key_mismatch",
				Schema: want.Schema,
				Table:  want.Name,
				Detail: describePKDrift(have.PrimaryKey, want.PrimaryKey),
			})
		}
		for _, u := range want.UniqueKeys {
			if !haveUnique(u, have.UniqueKeys) {
				u2 := u
				migrations = append(migrations, Migration{
					Kind:   KindAddUnique,
					Schema: want.Schema,
					Table:  want.Name,
					Detail: &AddUniqueDetail{UniqueKey: &u2},
				})
			}
		}
		for _, u := range have.UniqueKeys {
			if !haveUnique(u, want.UniqueKeys) {
				disallowed = append(disallowed, DestructiveChange{
					Kind:   "drop_unique_key",
					Schema: want.Schema,
					Table:  want.Name,
					Name:   u.Name,
				})
			}
		}
		for _, fk := range want.ForeignKeys {
			if !haveFK(fk, have.ForeignKeys) {
				fk2 := fk
				migrations = append(migrations, Migration{
					Kind:   KindAddFK,
					Schema: want.Schema,
					Table:  want.Name,
					Detail: &AddFKDetail{ForeignKey: &fk2},
				})
			}
		}
		for _, fk := range have.ForeignKeys {
			if !haveFK(fk, want.ForeignKeys) {
				disallowed = append(disallowed, DestructiveChange{
					Kind:   "drop_foreign_key",
					Schema: want.Schema,
					Table:  want.Name,
					Name:   fk.Name,
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
					Kind:   KindCreateIndex,
					Schema: wantIdx.Schema,
					Table:  wantIdx.TableName,
					Detail: &CreateIndexDetail{Index: wantIdx},
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

	migrations = sortMigrations(migrations)
	return migrations, disallowed
}

func migrationKindRank(kind string) int {
	switch kind {
	case KindCreateSchema:
		return 0
	case KindCreateTable:
		return 1
	case KindAddColumn:
		return 2
	case KindAddPK:
		return 3
	case KindAddUnique:
		return 4
	case KindAddFK:
		return 5
	case KindCreateIndex:
		return 6
	case KindDropColumn:
		return 7
	case KindDropTable:
		return 8
	default:
		return 9
	}
}

func migrationSortKey(m Migration) (int, string, string, string) {
	idx := ""
	switch d := m.Detail.(type) {
	case *CreateIndexDetail:
		idx = d.Index.Name
	}
	return migrationKindRank(m.Kind), m.Schema, m.Table, idx
}

// sortMigrations orders migrations by kind (create_schema first), stable ties, then FK topo for create_table.
func sortMigrations(migrations []Migration) []Migration {
	slices.SortStableFunc(migrations, func(a, b Migration) int {
		ka0, ka1, ka2, ka3 := migrationSortKey(a)
		kb0, kb1, kb2, kb3 := migrationSortKey(b)
		if c := cmp.Compare(ka0, kb0); c != 0 {
			return c
		}
		if c := strings.Compare(ka1, kb1); c != 0 {
			return c
		}
		if c := strings.Compare(ka2, kb2); c != 0 {
			return c
		}
		return strings.Compare(ka3, kb3)
	})
	return topoSortCreateTable(migrations)
}

// topoSortCreateTable reorders create_table migrations so that tables referenced by FK constraints
// are created before the tables that reference them. Non-create_table migrations keep their relative
// positions in the slice. A table that references another schema's table (not in this migration set)
// is treated as having no dependency on it. Cycles are left in place (PostgreSQL will error).
func topoSortCreateTable(migrations []Migration) []Migration {
	// Collect positions and details of create_table migrations.
	type entry struct {
		pos    int
		schema string
		table  string
		m      Migration
	}
	var creates []entry
	createPos := map[string]int{} // "schema.table" -> index into creates
	for i, m := range migrations {
		if m.Kind == KindCreateTable {
			key := m.Schema + "." + m.Table
			createPos[key] = len(creates)
			creates = append(creates, entry{pos: i, schema: m.Schema, table: m.Table, m: m})
		}
	}
	if len(creates) == 0 {
		return migrations
	}

	// Build adjacency: deps[i] = set of create indices that i depends on.
	n := len(creates)
	inDegree := make([]int, n)
	adj := make([][]int, n) // adj[dep] -> list of nodes that depend on dep
	for i, e := range creates {
		d, ok := e.m.Detail.(*CreateTableDetail)
		if !ok {
			continue
		}
		for _, fk := range d.TableDef.ForeignKeys {
			ref := fk.ReferencesSchema + "." + fk.ReferencesTable
			if depIdx, found := createPos[ref]; found && depIdx != i {
				adj[depIdx] = append(adj[depIdx], i)
				inDegree[i]++
			}
		}
	}

	// Kahn's algorithm.
	var queue []int
	for i := 0; i < n; i++ {
		if inDegree[i] == 0 {
			queue = append(queue, i)
		}
	}
	sorted := make([]Migration, 0, n)
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		sorted = append(sorted, creates[cur].m)
		for _, next := range adj[cur] {
			inDegree[next]--
			if inDegree[next] == 0 {
				queue = append(queue, next)
			}
		}
	}
	// Append any remaining (cycle members) in original order.
	if len(sorted) < n {
		inSorted := make(map[int]bool)
		for i, m := range sorted {
			_ = i
			for j, e := range creates {
				if e.m.Schema == m.Schema && e.m.Table == m.Table {
					inSorted[j] = true
					break
				}
			}
		}
		for i, e := range creates {
			if !inSorted[i] {
				sorted = append(sorted, e.m)
			}
		}
	}

	// Replace create_table slots in the original slice with the sorted order.
	result := make([]Migration, len(migrations))
	copy(result, migrations)
	si := 0
	for i := range result {
		if result[i].Kind == KindCreateTable {
			result[i] = sorted[si]
			si++
		}
	}
	return result
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
	if u.Name == "" {
		panic("internal invariant violation: unique constraint name is empty")
	}
	for _, e := range list {
		if e.Name == "" {
			panic("internal invariant violation: unique constraint name is empty")
		}
		if e.Name == u.Name && sliceEqual(e.Columns, u.Columns) {
			return true
		}
	}
	return false
}

// fk is the foreign key from the schema definition, list is from introspection.
func haveFK(fk schema.ForeignKey, list []schema.ForeignKey) bool {
	if fk.Name == "" {
		panic("internal invariant violation: foreign key name is empty")
	}
	for _, e := range list {
		if e.Name == "" {
			panic("internal invariant violation: foreign key name is empty")
		}
		if e.Name == fk.Name &&
			sliceEqual(e.Columns, fk.Columns) &&
			e.ReferencesSchema == fk.ReferencesSchema &&
			e.ReferencesTable == fk.ReferencesTable &&
			sliceEqual(e.ReferencesColumns, fk.ReferencesColumns) &&
			fkActionEqual(e.OnDelete, fk.OnDelete) &&
			fkActionEqual(e.OnUpdate, fk.OnUpdate) {
			return true
		}
	}
	return false
}

func fkActionEqual(a, b string) bool {
	normalize := func(s string) string {
		if s == "" {
			return "NO ACTION"
		}
		return s
	}
	return normalize(a) == normalize(b)
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
