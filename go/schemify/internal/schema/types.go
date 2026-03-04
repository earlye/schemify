package schema

// AllowDropColumn declares that a column may be dropped if the directive is present.
// Type is the expected column type to allow the drop; use "ANY_TYPE" to allow regardless of type.
type AllowDropColumn struct {
	Name string // column name
	Type string // normalized type (must match actual) or "ANY_TYPE"
}

// PrimaryKeyConstraint describes a PRIMARY KEY on a table.
type PrimaryKeyConstraint struct {
	Name    string   // constraint name (optional in PG)
	Columns []string // column names in order
}

// UniqueConstraint describes a UNIQUE constraint on a table.
type UniqueConstraint struct {
	Name    string
	Columns []string
}

// ForeignKey describes a FOREIGN KEY constraint.
type ForeignKey struct {
	Name               string   // constraint name
	Columns            []string // columns on this table
	ReferencesSchema   string
	ReferencesTable    string
	ReferencesColumns  []string
	OnDelete           string // NO ACTION, RESTRICT, CASCADE, SET NULL, SET DEFAULT
	OnUpdate           string
}

// Table represents a table in a schema (desired or actual).
type Table struct {
	Schema           string              // e.g. "public"
	Name             string              // table name
	Columns          []Column            // column definitions
	AllowDropColumns []AllowDropColumn   // from "-- removed: colname type" or "-- removed: colname ANY_TYPE"
	PrimaryKey       *PrimaryKeyConstraint
	UniqueKeys       []UniqueConstraint
	ForeignKeys      []ForeignKey
}

// Column represents a single column.
type Column struct {
	Name     string
	Type     string // normalized type name for comparison (e.g. "integer", "character varying(255)")
	Nullable bool
	Default  string
}

// Index represents a standalone index (CREATE INDEX). Key in maps: schema.indexname (e.g. "public.idx_users_username").
// Concurrently must be true (required for large-table safety); load should fail if CREATE INDEX is not CONCURRENTLY.
type Index struct {
	Name        string   // index name
	Schema      string   // index schema (e.g. "public")
	TableSchema string   // table's schema
	TableName   string   // table name
	Columns     []string // column names in order
	Unique      bool
	IndexType   string // btree, gin, gist, hash; default "btree"
	Concurrently bool  // must be true; CREATE INDEX CONCURRENTLY
}
