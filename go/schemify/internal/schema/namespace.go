package schema

import (
	"sort"
	"strings"
)

// IsSystemNamespace reports PostgreSQL built-in namespaces we never manage.
func IsSystemNamespace(name string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		return true
	}
	switch name {
	case "pg_catalog", "information_schema":
		return true
	}
	return strings.HasPrefix(name, "pg_toast") || strings.HasPrefix(name, "pg_")
}

// IsDropSchemaCandidate is true when a surplus namespace in the DB should surface as drop_schema drift.
// public is never a candidate (asymmetric rule: we may create it but never plan-drop it).
func IsDropSchemaCandidate(name string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" || name == "public" {
		return false
	}
	return !IsSystemNamespace(name)
}

// CollectDesiredNamespaces returns all namespace names required by the desired model.
func CollectDesiredNamespaces(load *LoadResult) map[string]struct{} {
	if load == nil {
		return map[string]struct{}{}
	}
	out := make(map[string]struct{})
	add := func(ns string) {
		ns = strings.ToLower(strings.TrimSpace(ns))
		if ns == "" || IsSystemNamespace(ns) {
			return
		}
		out[ns] = struct{}{}
	}
	for ns := range load.Schemas {
		add(ns)
	}
	for _, t := range load.Tables {
		add(t.Schema)
		for _, fk := range t.ForeignKeys {
			ref := fk.ReferencesSchema
			if ref == "" {
				ref = "public"
			}
			add(ref)
		}
	}
	for _, idx := range load.Indexes {
		add(idx.Schema)
		add(idx.TableSchema)
	}
	return out
}

// NamespaceNames returns sorted namespace names from a set.
func NamespaceNames(set map[string]struct{}) []string {
	if len(set) == 0 {
		return nil
	}
	out := make([]string, 0, len(set))
	for ns := range set {
		out = append(out, ns)
	}
	sort.Strings(out)
	return out
}

// UnionNamespaces returns sorted union of two namespace sets.
func UnionNamespaces(a, b map[string]struct{}) []string {
	if len(a) == 0 && len(b) == 0 {
		return nil
	}
	out := make(map[string]struct{})
	for ns := range a {
		out[ns] = struct{}{}
	}
	for ns := range b {
		out[ns] = struct{}{}
	}
	return NamespaceNames(out)
}
