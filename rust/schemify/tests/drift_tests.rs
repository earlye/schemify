//! Unit tests for drift block parsing (ported from Go drift_test.go).

use schemify::drift::{build_anticipated_drift, extract_drift_blocks, merge_drift_groups};
use schemify::schema::{DriftBlock, DriftPolicy, DriftScope};

#[test]
fn extract_drift_blocks_basic_drop() {
    let sql = "-- DRIFT cleanup1 DROP (\n--   old_col TEXT NOT NULL\n-- )";
    let blocks = extract_drift_blocks(sql, "public", "things").unwrap();
    assert_eq!(blocks.len(), 1);
    let b = &blocks[0];
    assert_eq!(b.id, "cleanup1");
    assert_eq!(b.policy, DriftPolicy::Drop);
    assert_eq!(b.scope, DriftScope::Table);
    assert!(b.raw_body.contains("old_col"), "raw_body: {:?}", b.raw_body);
}

#[test]
fn extract_drift_blocks_deprecated() {
    let sql = "-- DRIFT cleanup2 DEPRECATED (\n--   old_col TEXT NOT NULL\n-- )";
    let blocks = extract_drift_blocks(sql, "public", "things").unwrap();
    assert_eq!(blocks.len(), 1);
    assert_eq!(blocks[0].policy, DriftPolicy::Deprecated);
}

#[test]
fn extract_drift_blocks_nested_parens() {
    let sql = "-- DRIFT cleanup3 DROP (\n-- CHECK ( x > 0 )\n-- )";
    let blocks = extract_drift_blocks(sql, "public", "things").unwrap();
    assert_eq!(blocks.len(), 1);
    assert!(
        blocks[0].raw_body.contains("CHECK"),
        "raw_body: {:?}",
        blocks[0].raw_body
    );
}

#[test]
fn extract_drift_blocks_single_quoted_literal() {
    let sql = "-- DRIFT cleanup4 DROP (\n-- CHECK (name != '(')\n-- )";
    let blocks = extract_drift_blocks(sql, "public", "things").unwrap();
    assert_eq!(blocks.len(), 1);
}

#[test]
fn extract_drift_blocks_unclosed_error() {
    let sql = "-- DRIFT cleanup5 DROP (\n--   old_col TEXT NOT NULL";
    let err = extract_drift_blocks(sql, "public", "things").unwrap_err();
    assert!(err.to_string().contains("unclosed"), "err: {err}");
}

#[test]
fn extract_drift_blocks_policy_conflict_error() {
    let sql = "-- DRIFT cleanup6 DROP (\n--   col1 TEXT\n-- )\n-- DRIFT cleanup6 DEPRECATED (\n--   col2 TEXT\n-- )";
    let err = extract_drift_blocks(sql, "public", "things").unwrap_err();
    assert!(!err.to_string().is_empty());
}

#[test]
fn extract_drift_blocks_same_id_same_policy_ok() {
    let sql = "-- DRIFT cleanup7 DROP (\n--   col1 TEXT\n-- )\n-- DRIFT cleanup7 DROP (\n--   col2 TEXT\n-- )";
    let blocks = extract_drift_blocks(sql, "public", "things").unwrap();
    assert_eq!(blocks.len(), 2);
}

#[test]
fn extract_drift_blocks_file_scope() {
    let sql = "-- DRIFT cleanup8 DROP (\n-- CREATE INDEX CONCURRENTLY idx_foo ON public.things (col);\n-- )";
    let blocks = extract_drift_blocks(sql, "", "").unwrap();
    assert_eq!(blocks.len(), 1);
    assert_eq!(blocks[0].scope, DriftScope::File);
}

#[test]
fn build_anticipated_drift_column() {
    let mut block = DriftBlock {
        id: "cleanup9".into(),
        policy: DriftPolicy::Drop,
        scope: DriftScope::Table,
        table_schema: "public".into(),
        table_name: "things".into(),
        raw_body: "  old_col TEXT NOT NULL".into(),
        anticipated_table: None,
        anticipated_indexes: Vec::new(),
    };
    build_anticipated_drift(&mut block).unwrap();
    let t = block.anticipated_table.as_ref().expect("anticipated_table");
    assert_eq!(t.columns.len(), 1);
    assert_eq!(t.columns[0].name, "old_col");
}

#[test]
fn build_anticipated_drift_invalid_ddl_error() {
    let mut block = DriftBlock {
        id: "cleanup10".into(),
        policy: DriftPolicy::Drop,
        scope: DriftScope::Table,
        table_schema: String::new(),
        table_name: String::new(),
        raw_body: "NOT VALID SQL @@@@".into(),
        anticipated_table: None,
        anticipated_indexes: Vec::new(),
    };
    let err = build_anticipated_drift(&mut block).unwrap_err();
    assert!(!err.to_string().is_empty());
}

#[test]
fn build_anticipated_drift_file_scope_index() {
    let mut block = DriftBlock {
        id: "cleanup11".into(),
        policy: DriftPolicy::Drop,
        scope: DriftScope::File,
        table_schema: String::new(),
        table_name: String::new(),
        raw_body: "CREATE INDEX CONCURRENTLY idx_foo ON public.things (col);".into(),
        anticipated_table: None,
        anticipated_indexes: Vec::new(),
    };
    build_anticipated_drift(&mut block).unwrap();
    assert_eq!(block.anticipated_indexes.len(), 1);
    assert_eq!(block.anticipated_indexes[0].name, "idx_foo");
}

#[test]
fn merge_drift_groups_same_id_merged() {
    use schemify::schema::{Column, Table};
    let blocks = vec![
        DriftBlock {
            id: "cleanup12".into(),
            policy: DriftPolicy::Drop,
            scope: DriftScope::Table,
            table_schema: String::new(),
            table_name: String::new(),
            raw_body: String::new(),
            anticipated_table: Some(Table {
                schema: String::new(),
                name: String::new(),
                columns: vec![Column {
                    name: "col1".into(),
                    type_: "text".into(),
                    nullable: true,
                    default: String::new(),
                }],
                allow_drop_columns: Vec::new(),
                primary_key: None,
                unique_keys: Vec::new(),
                foreign_keys: Vec::new(),
            }),
            anticipated_indexes: Vec::new(),
        },
        DriftBlock {
            id: "cleanup12".into(),
            policy: DriftPolicy::Drop,
            scope: DriftScope::Table,
            table_schema: String::new(),
            table_name: String::new(),
            raw_body: String::new(),
            anticipated_table: Some(Table {
                schema: String::new(),
                name: String::new(),
                columns: vec![Column {
                    name: "col2".into(),
                    type_: "integer".into(),
                    nullable: true,
                    default: String::new(),
                }],
                allow_drop_columns: Vec::new(),
                primary_key: None,
                unique_keys: Vec::new(),
                foreign_keys: Vec::new(),
            }),
            anticipated_indexes: Vec::new(),
        },
    ];
    let groups = merge_drift_groups(blocks).unwrap();
    let g = groups.get("cleanup12").expect("group cleanup12");
    assert_eq!(g.anticipated_columns.len(), 2);
}

#[test]
fn merge_drift_groups_policy_conflict_error() {
    let blocks = vec![
        DriftBlock {
            id: "cleanup13".into(),
            policy: DriftPolicy::Drop,
            scope: DriftScope::Table,
            table_schema: String::new(),
            table_name: String::new(),
            raw_body: String::new(),
            anticipated_table: None,
            anticipated_indexes: Vec::new(),
        },
        DriftBlock {
            id: "cleanup13".into(),
            policy: DriftPolicy::Deprecated,
            scope: DriftScope::Table,
            table_schema: String::new(),
            table_name: String::new(),
            raw_body: String::new(),
            anticipated_table: None,
            anticipated_indexes: Vec::new(),
        },
    ];
    let err = merge_drift_groups(blocks).unwrap_err();
    assert!(!err.to_string().is_empty());
}

#[test]
fn extract_drift_blocks_trailing_comma_ok() {
    let mut block = DriftBlock {
        id: "cleanup14".into(),
        policy: DriftPolicy::Drop,
        scope: DriftScope::Table,
        table_schema: "public".into(),
        table_name: "things".into(),
        raw_body: "  old_col TEXT NOT NULL,".into(),
        anticipated_table: None,
        anticipated_indexes: Vec::new(),
    };
    build_anticipated_drift(&mut block).unwrap();
    let t = block.anticipated_table.as_ref().expect("anticipated_table");
    assert_eq!(t.columns.len(), 1);
}
