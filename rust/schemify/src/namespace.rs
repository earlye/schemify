//! PostgreSQL namespace (schema) helpers.

use crate::load::LoadResult;
use std::collections::{BTreeSet, HashSet};

pub fn is_system_namespace(name: &str) -> bool {
    let name = name.trim().to_lowercase();
    if name.is_empty() {
        return true;
    }
    match name.as_str() {
        "pg_catalog" | "information_schema" => true,
        _ => name.starts_with("pg_toast") || name.starts_with("pg_"),
    }
}

pub fn is_drop_schema_candidate(name: &str) -> bool {
    let name = name.trim().to_lowercase();
    if name.is_empty() || name == "public" {
        return false;
    }
    !is_system_namespace(&name)
}

pub fn collect_desired_namespaces(load: &LoadResult) -> HashSet<String> {
    let mut out = HashSet::new();
    let mut add = |ns: &str| {
        let ns = ns.trim().to_lowercase();
        if ns.is_empty() || is_system_namespace(&ns) {
            return;
        }
        out.insert(ns);
    };
    for ns in &load.schemas {
        add(ns);
    }
    for t in load.tables.values() {
        add(&t.schema);
        for fk in &t.foreign_keys {
            let ref_schema = if fk.references_schema.is_empty() {
                "public"
            } else {
                fk.references_schema.as_str()
            };
            add(ref_schema);
        }
    }
    for idx in load.indexes.values() {
        add(&idx.schema);
        add(&idx.table_schema);
    }
    out
}

pub fn union_namespaces(a: &HashSet<String>, b: &HashSet<String>) -> Vec<String> {
    let mut out: BTreeSet<String> = BTreeSet::new();
    for ns in a {
        out.insert(ns.clone());
    }
    for ns in b {
        out.insert(ns.clone());
    }
    out.into_iter().collect()
}
