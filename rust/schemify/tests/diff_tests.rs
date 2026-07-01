//! Port of Go `internal/diff/diff_test.go`.

use schemify::diff::{
    diff_tables_and_indexes, tables_match_for_drop, DestructiveChange, MigrationDetail,
    KIND_ADD_COLUMN, KIND_CREATE_SCHEMA, KIND_CREATE_TABLE, KIND_DROP_TABLE,
};
use schemify::schema::{Column, Index, Table, UniqueConstraint};
use std::collections::{HashMap, HashSet};

fn public_namespaces() -> HashSet<String> {
    ["public".into()].into_iter().collect()
}

fn diff(
    desired: HashMap<String, Table>,
    actual: HashMap<String, Table>,
    di: Option<HashMap<String, Index>>,
    ai: Option<HashMap<String, Index>>,
    allow: Option<HashMap<String, Table>>,
) -> (Vec<schemify::diff::Migration>, Vec<DestructiveChange>) {
    let ns = public_namespaces();
    diff_tables_and_indexes(
        &ns,
        &ns,
        &desired,
        &actual,
        di.as_ref(),
        ai.as_ref(),
        allow.as_ref(),
        None,
    )
}

#[test]
fn diff_add_table() {
    let mut desired = HashMap::new();
    desired.insert(
        "public.a".into(),
        Table {
            schema: "public".into(),
            name: "a".into(),
            columns: vec![Column {
                name: "id".into(),
                type_: "integer".into(),
                nullable: true,
                default: String::new(),
            }],
            ..empty_table()
        },
    );
    let (add, dest) = diff(desired, HashMap::new(), None, None, None);
    assert!(dest.is_empty());
    assert_eq!(add.len(), 1);
    assert_eq!(add[0].kind, KIND_CREATE_TABLE);
    assert_eq!(add[0].table, "a");
}

#[test]
fn diff_add_column() {
    let mut desired = HashMap::new();
    desired.insert(
        "public.a".into(),
        Table {
            schema: "public".into(),
            name: "a".into(),
            columns: vec![col("id", "integer"), col("name", "character varying(255)")],
            ..empty_table()
        },
    );
    let mut actual = HashMap::new();
    actual.insert(
        "public.a".into(),
        Table {
            schema: "public".into(),
            name: "a".into(),
            columns: vec![col("id", "integer")],
            ..empty_table()
        },
    );
    let (add, dest) = diff(desired, actual, None, None, None);
    assert!(dest.is_empty());
    assert_eq!(add.len(), 1);
    assert_eq!(add[0].kind, KIND_ADD_COLUMN);
    let MigrationDetail::AddColumn { column } = &add[0].detail else {
        panic!("expected AddColumn");
    };
    assert_eq!(column.name, "name");
}

#[test]
fn diff_add_column_not_null_default_additive() {
    let mut desired = HashMap::new();
    desired.insert(
        "public.a".into(),
        Table {
            schema: "public".into(),
            name: "a".into(),
            columns: vec![
                col("id", "integer"),
                Column {
                    name: "status".into(),
                    type_: "text".into(),
                    nullable: false,
                    default: "'init'".into(),
                },
            ],
            ..empty_table()
        },
    );
    let mut actual = HashMap::new();
    actual.insert(
        "public.a".into(),
        Table {
            schema: "public".into(),
            name: "a".into(),
            columns: vec![col("id", "integer")],
            ..empty_table()
        },
    );
    let (add, dest) = diff(desired, actual, None, None, None);
    assert!(dest.is_empty());
    assert_eq!(add.len(), 1);
    assert_eq!(add[0].kind, KIND_ADD_COLUMN);
    let MigrationDetail::AddColumn { column } = &add[0].detail else {
        panic!("expected AddColumn");
    };
    assert_eq!(column.name, "status");
}

#[test]
fn diff_add_column_not_null_no_default_disallowed() {
    let mut desired = HashMap::new();
    desired.insert(
        "public.a".into(),
        Table {
            schema: "public".into(),
            name: "a".into(),
            columns: vec![
                col("id", "integer"),
                Column {
                    name: "status".into(),
                    type_: "text".into(),
                    nullable: false,
                    default: String::new(),
                },
            ],
            ..empty_table()
        },
    );
    let mut actual = HashMap::new();
    actual.insert(
        "public.a".into(),
        Table {
            schema: "public".into(),
            name: "a".into(),
            columns: vec![col("id", "integer")],
            ..empty_table()
        },
    );
    let (add, dest) = diff(desired, actual, None, None, None);
    assert!(add.is_empty());
    assert_eq!(dest.len(), 1);
    assert_eq!(dest[0].kind, "add_column_not_null_no_default");
    assert_eq!(dest[0].column, "status");
}

#[test]
fn diff_drop_column_destructive() {
    let mut desired = HashMap::new();
    desired.insert(
        "public.a".into(),
        Table {
            schema: "public".into(),
            name: "a".into(),
            columns: vec![col("id", "integer")],
            ..empty_table()
        },
    );
    let mut actual = HashMap::new();
    actual.insert(
        "public.a".into(),
        Table {
            schema: "public".into(),
            name: "a".into(),
            columns: vec![col("id", "integer"), col("name", "character varying(255)")],
            ..empty_table()
        },
    );
    let (add, dest) = diff(desired, actual, None, None, None);
    assert!(add.is_empty());
    assert_eq!(dest.len(), 1);
    assert_eq!(dest[0].kind, "drop_column");
    assert_eq!(dest[0].column, "name");
}

#[test]
fn diff_drop_table_destructive() {
    let mut actual = HashMap::new();
    actual.insert(
        "public.a".into(),
        Table {
            schema: "public".into(),
            name: "a".into(),
            columns: vec![col("id", "integer")],
            ..empty_table()
        },
    );
    let (add, dest) = diff(HashMap::new(), actual, None, None, None);
    assert!(add.is_empty());
    assert_eq!(dest.len(), 1);
    assert_eq!(dest[0].kind, "drop_table");
}

#[test]
fn diff_drop_table_allowed() {
    let mut desired = HashMap::new();
    desired.insert(
        "public.a".into(),
        Table {
            schema: "public".into(),
            name: "a".into(),
            columns: vec![col("id", "integer")],
            ..empty_table()
        },
    );
    let mut actual = desired.clone();
    actual.insert(
        "public.b".into(),
        Table {
            schema: "public".into(),
            name: "b".into(),
            columns: vec![col("id", "integer")],
            ..empty_table()
        },
    );
    let mut allow = HashMap::new();
    allow.insert(
        "public.b".into(),
        Table {
            schema: "public".into(),
            name: "b".into(),
            columns: vec![col("id", "integer")],
            ..empty_table()
        },
    );
    let (add, dest) = diff(desired, actual, None, None, Some(allow));
    assert!(dest.is_empty());
    assert_eq!(add.len(), 1);
    assert_eq!(add[0].kind, KIND_DROP_TABLE);
    assert_eq!(add[0].table, "b");
}

#[test]
fn tables_match_for_drop_test() {
    let a = Table {
        schema: "public".into(),
        name: "a".into(),
        columns: vec![col("id", "integer"), col("x", "text")],
        ..empty_table()
    };
    let b = Table {
        schema: "public".into(),
        name: "b".into(),
        columns: vec![col("id", "integer"), col("x", "text")],
        ..empty_table()
    };
    assert!(tables_match_for_drop(Some(&a), Some(&b)));
    let b2 = Table {
        schema: "public".into(),
        name: "b".into(),
        columns: vec![col("id", "integer")],
        ..empty_table()
    };
    assert!(!tables_match_for_drop(Some(&a), Some(&b2)));
}

#[test]
fn diff_jsonb_expression_index_second_run_idempotent() {
    let tables = HashMap::from([(
        "public.key_doc".into(),
        Table {
            schema: "public".into(),
            name: "key_doc".into(),
            columns: vec![col("key", "text"), col("doc", "jsonb")],
            ..empty_table()
        },
    )]);
    let desired_idx = HashMap::from([(
        "public.idx_key_doc_key_kind".into(),
        Index {
            name: "idx_key_doc_key_kind".into(),
            schema: "public".into(),
            table_schema: "public".into(),
            table_name: "key_doc".into(),
            columns: vec!["key".into(), "(doc->>'kind')".into()],
            unique: false,
            index_type: "btree".into(),
            concurrently: true,
        },
    )]);
    let actual_idx = HashMap::from([(
        "public.idx_key_doc_key_kind".into(),
        Index {
            name: "idx_key_doc_key_kind".into(),
            schema: "public".into(),
            table_schema: "public".into(),
            table_name: "key_doc".into(),
            columns: vec!["key".into(), "((doc ->> 'kind'::text))".into()],
            unique: false,
            index_type: "btree".into(),
            concurrently: true,
        },
    )]);
    let (add, dest) = diff(
        tables.clone(),
        tables,
        Some(desired_idx),
        Some(actual_idx),
        None,
    );
    assert!(dest.is_empty(), "{dest:?}");
    assert!(add.is_empty(), "{add:?}");
}

#[test]
fn diff_empty_unique_constraint_names_panic() {
    let mut desired = HashMap::new();
    desired.insert(
        "public.users".into(),
        Table {
            schema: "public".into(),
            name: "users".into(),
            columns: vec![col("id", "integer"), col("email", "text")],
            unique_keys: vec![UniqueConstraint {
                name: String::new(),
                columns: vec!["email".into()],
            }],
            ..empty_table()
        },
    );
    let mut actual = HashMap::new();
    actual.insert(
        "public.users".into(),
        Table {
            schema: "public".into(),
            name: "users".into(),
            columns: vec![col("id", "integer"), col("email", "text")],
            unique_keys: vec![UniqueConstraint {
                name: "users_email_key".into(),
                columns: vec!["email".into()],
            }],
            ..empty_table()
        },
    );
    let result = std::panic::catch_unwind(|| {
        diff(desired, actual, None, None, None);
    });
    assert!(result.is_err());
}

fn empty_table() -> Table {
    Table {
        schema: String::new(),
        name: String::new(),
        columns: Vec::new(),
        allow_drop_columns: Vec::new(),
        primary_key: None,
        unique_keys: Vec::new(),
        foreign_keys: Vec::new(),
    }
}

fn col(name: &str, type_: &str) -> Column {
    Column {
        name: name.into(),
        type_: type_.into(),
        nullable: true,
        default: String::new(),
    }
}

#[test]
fn diff_create_schema_missing_namespace() {
    let desired_ns: HashSet<String> = ["users".into()].into_iter().collect();
    let actual_ns = HashSet::new();
    let (add, dest) = diff_tables_and_indexes(
        &desired_ns,
        &actual_ns,
        &HashMap::new(),
        &HashMap::new(),
        None,
        None,
        None,
        None,
    );
    assert!(dest.is_empty());
    assert_eq!(add.len(), 1);
    assert_eq!(add[0].kind, KIND_CREATE_SCHEMA);
    assert_eq!(add[0].schema, "users");
}

#[test]
fn diff_drop_schema_destructive() {
    let desired_ns = public_namespaces();
    let actual_ns: HashSet<String> = ["public".into(), "legacy".into()].into_iter().collect();
    let (add, dest) = diff_tables_and_indexes(
        &desired_ns,
        &actual_ns,
        &HashMap::new(),
        &HashMap::new(),
        None,
        None,
        None,
        None,
    );
    assert!(add.is_empty());
    assert_eq!(dest.len(), 1);
    assert_eq!(dest[0].kind, "drop_schema");
    assert_eq!(dest[0].schema, "legacy");
}

#[test]
fn diff_drop_schema_public_not_destructive() {
    let desired_ns = HashSet::new();
    let actual_ns = public_namespaces();
    let (_, dest) = diff_tables_and_indexes(
        &desired_ns,
        &actual_ns,
        &HashMap::new(),
        &HashMap::new(),
        None,
        None,
        None,
        None,
    );
    assert!(!dest.iter().any(|d| d.kind == "drop_schema"));
}
