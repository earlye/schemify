package schema

// Table represents a table in a schema (desired or actual).
type Table struct {
	Schema  string   // e.g. "public"
	Name    string   // table name
	Columns []Column // column definitions
}

// Column represents a single column.
type Column struct {
	Name     string
	Type     string // normalized type name for comparison (e.g. "integer", "character varying(255)")
	Nullable bool
	Default  string
}
