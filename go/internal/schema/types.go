package schema

// AllowDropColumn declares that a column may be dropped if the directive is present.
// Type is the expected column type to allow the drop; use "ANY_TYPE" to allow regardless of type.
type AllowDropColumn struct {
	Name string // column name
	Type string // normalized type (must match actual) or "ANY_TYPE"
}

// Table represents a table in a schema (desired or actual).
type Table struct {
	Schema          string             // e.g. "public"
	Name            string             // table name
	Columns         []Column           // column definitions
	AllowDropColumns []AllowDropColumn // from "-- removed: colname type" or "-- removed: colname ANY_TYPE"
}

// Column represents a single column.
type Column struct {
	Name     string
	Type     string // normalized type name for comparison (e.g. "integer", "character varying(255)")
	Nullable bool
	Default  string
}
