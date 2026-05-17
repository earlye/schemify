package schema

import "testing"

func TestCollectDesiredNamespaces_ImplicitAndExplicit(t *testing.T) {
	load := &LoadResult{
		Schemas: map[string]struct{}{"app": {}},
		Tables: map[string]*Table{
			"users.widgets": {
				Schema: "users",
				Name:   "widgets",
				ForeignKeys: []ForeignKey{{
					ReferencesSchema: "public",
					ReferencesTable:  "codes",
				}},
			},
		},
		Indexes: map[string]*Index{
			"users.idx_widgets": {
				Schema:      "users",
				TableSchema: "users",
				Name:        "idx_widgets",
			},
		},
	}
	got := CollectDesiredNamespaces(load)
	for _, want := range []string{"app", "users", "public"} {
		if _, ok := got[want]; !ok {
			t.Errorf("missing namespace %q in %v", want, got)
		}
	}
	if _, ok := got["pg_catalog"]; ok {
		t.Error("pg_catalog should not be in desired namespaces")
	}
}

func TestIsDropSchemaCandidate(t *testing.T) {
	if IsDropSchemaCandidate("public") {
		t.Error("public is not a drop_schema candidate")
	}
	if !IsDropSchemaCandidate("legacy") {
		t.Error("legacy should be a drop_schema candidate")
	}
	if IsDropSchemaCandidate("pg_catalog") {
		t.Error("pg_catalog is not a drop_schema candidate")
	}
}
