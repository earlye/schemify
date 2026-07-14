//! Port of Go `internal/schema/load_test.rs` (subset + critical cases).

use schemify::load::{
    extract_drop_table_block_defs, load_allow_drop_table_defs, load_from_dir, parse_ddl,
};
use std::fs;
use std::io::Write;
use tempfile::tempdir;

#[test]
fn load_from_dir_reads_sql_files() {
    let dir = tempdir().unwrap();
    let mut f = fs::File::create(dir.path().join("a.sql")).unwrap();
    writeln!(
        f,
        "CREATE TABLE public.users (id integer, username character varying(255));"
    )
    .unwrap();
    let mut f = fs::File::create(dir.path().join("b.sql")).unwrap();
    writeln!(
        f,
        "CREATE TABLE public.events (id integer, event character varying(255));"
    )
    .unwrap();

    let got = load_from_dir(dir.path()).unwrap();
    assert_eq!(got.tables.len(), 2);
    let users = got.tables.get("public.users").expect("users");
    assert_eq!(users.name, "users");
    assert_eq!(users.schema, "public");
    assert_eq!(users.columns.len(), 2);
}

#[test]
fn parse_ddl_single_table() {
    let sql = "CREATE TABLE public.foo (id integer, name character varying(100));";
    let (tables, indexes, _) = parse_ddl(sql).unwrap();
    assert_eq!(tables.len(), 1);
    assert!(indexes.is_empty());
    let tbl = &tables[0];
    assert_eq!(tbl.schema, "public");
    assert_eq!(tbl.name, "foo");
    assert_eq!(tbl.columns.len(), 2);
    assert_eq!(tbl.columns[0].name, "id");
    assert_eq!(tbl.columns[0].type_, "integer");
    assert_eq!(tbl.columns[1].name, "name");
}

#[test]
fn parse_ddl_removed_directive() {
    let sql = r#"CREATE TABLE public.users (
    id integer,
    username character varying(255)
    -- removed: passwordhash character varying(64)
);
"#;
    let (tables, _, _) = parse_ddl(sql).unwrap();
    assert_eq!(tables.len(), 1);
    assert_eq!(tables[0].allow_drop_columns.len(), 1);
    let a = &tables[0].allow_drop_columns[0];
    assert_eq!(a.name, "passwordhash");
    assert_eq!(a.type_, "character varying(64)");
}

#[test]
fn parse_ddl_create_index_concurrently() {
    let sql = r#"CREATE TABLE public.users (id integer, username character varying(255));
CREATE INDEX CONCURRENTLY idx_users_username ON public.users (username);"#;
    let (tables, indexes, _) = parse_ddl(sql).unwrap();
    assert_eq!(tables.len(), 1);
    assert_eq!(indexes.len(), 1);
    let idx = &indexes[0];
    assert_eq!(idx.name, "idx_users_username");
    assert_eq!(idx.schema, "public");
    assert_eq!(idx.table_name, "users");
    assert_eq!(idx.columns, vec!["username"]);
    assert!(idx.concurrently);
}

#[test]
fn parse_ddl_create_index_concurrently_no_name_fails() {
    let sql = "CREATE INDEX CONCURRENTLY ON public.users (username);";
    let err = parse_ddl(sql).unwrap_err();
    assert!(
        err.to_string().contains("explicit name"),
        "got {err}"
    );
}

#[test]
fn parse_ddl_create_index_without_concurrently_fails() {
    let sql = "CREATE INDEX idx_users_username ON public.users (username);";
    let err = parse_ddl(sql).unwrap_err();
    assert!(err.to_string().contains("CONCURRENTLY"), "got {err}");
}

#[test]
fn parse_ddl_jsonb_expression_index() {
    let sql = "CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_key_docs_key_kind ON public.key_docs (key, (doc->>'kind'));";
    let (_, indexes, _) = parse_ddl(sql).unwrap();
    assert_eq!(indexes.len(), 1);
    let idx = &indexes[0];
    assert_eq!(idx.name, "idx_key_docs_key_kind");
    assert_eq!(idx.columns.len(), 2);
    assert_eq!(idx.columns[0], "key");
    assert!(idx.columns[1].contains("->>"));
}

#[test]
fn parse_ddl_predicts_constraint_names() {
    let sql = r#"CREATE TABLE public.parent (
    id integer PRIMARY KEY,
    code text UNIQUE
);
CREATE TABLE public.child (
    id integer PRIMARY KEY,
    parent_code text REFERENCES public.parent (code)
);"#;
    let (tables, _, _) = parse_ddl(sql).unwrap();
    assert_eq!(tables.len(), 2);
    let mut parent = None;
    let mut child = None;
    for t in &tables {
        match t.name.as_str() {
            "parent" => parent = Some(t),
            "child" => child = Some(t),
            _ => {}
        }
    }
    let parent = parent.expect("parent");
    let child = child.expect("child");
    assert_eq!(parent.unique_keys.len(), 1);
    assert_eq!(parent.unique_keys[0].name, "parent_code_key");
    assert_eq!(child.foreign_keys.len(), 1);
    assert_eq!(child.foreign_keys[0].name, "child_parent_code_fkey");
}

#[test]
fn parse_ddl_create_schema_preamble() {
    const NS: &str = "schemify_test_users";
    let sql = format!(
        r#"
CREATE SCHEMA IF NOT EXISTS {NS};

CREATE TABLE {NS}.widgets (
    id integer NOT NULL PRIMARY KEY
);

CREATE INDEX CONCURRENTLY idx_widgets_id ON {NS}.widgets (id);
"#
    );
    let (tables, indexes, schemas) = parse_ddl(&sql).unwrap();
    assert!(schemas.contains(NS));
    assert_eq!(tables.len(), 1);
    let tbl = &tables[0];
    assert_eq!(tbl.schema, NS);
    assert_eq!(tbl.name, "widgets");
    assert_eq!(indexes.len(), 1);
    let idx = &indexes[0];
    assert_eq!(idx.schema, NS);
    assert_eq!(idx.table_schema, NS);
    assert_eq!(idx.table_name, "widgets");
    assert_eq!(idx.name, "idx_widgets_id");
}

#[test]
fn load_from_dir_create_schema_preamble() {
    const NS: &str = "schemify_test_users";
    let dir = tempdir().unwrap();
    let sql = format!(
        r#"
CREATE SCHEMA IF NOT EXISTS {NS};

CREATE TABLE {NS}.widgets (
    id integer NOT NULL PRIMARY KEY
);

CREATE INDEX CONCURRENTLY idx_widgets_id ON {NS}.widgets (id);
"#
    );
    fs::write(dir.path().join("schema.sql"), sql).unwrap();
    let got = load_from_dir(dir.path()).unwrap();
    assert!(got.tables.contains_key(&format!("{NS}.widgets")));
    assert!(got.indexes.contains_key(&format!("{NS}.idx_widgets_id")));
}

#[test]
fn extract_drop_table_no_closing_skipped() {
    let raw = "-- DROP TABLE public.foo (\n--     id integer\n";
    assert!(
        extract_drop_table_block_defs(raw)
            .expect("incomplete block should not parse")
            .is_empty()
    );
}

#[test]
fn load_from_dir_errors_on_parse_failure_in_any_file() {
    let dir = tempdir().unwrap();
    fs::write(
        dir.path().join("good.sql"),
        "CREATE TABLE public.users (id integer);",
    )
    .unwrap();
    fs::write(
        dir.path().join("bad.sql"),
        "CREATE INDEX idx_bad ON public.users (id);",
    )
    .unwrap();

    let err = load_from_dir(dir.path()).unwrap_err();
    let msg = err.to_string();
    assert!(
        msg.contains("bad.sql"),
        "expected bad.sql in error message, got: {msg}"
    );
    assert!(
        msg.contains("CONCURRENTLY") || msg.contains("parse SQL"),
        "expected parse failure detail, got: {msg}"
    );
}

#[test]
fn parse_ddl_column_default_zero_arg_funccall() {
    let sql = r#"CREATE TABLE example.thing (
    id UUID PRIMARY KEY,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);"#;
    let (tables, _, _) = parse_ddl(sql).unwrap();
    assert_eq!(tables.len(), 1);
    let created_at = tables[0]
        .columns
        .iter()
        .find(|c| c.name == "created_at")
        .expect("created_at column");
    assert_eq!(created_at.default, "now()");
}

#[test]
fn parse_ddl_column_default_funccall_pg_catalog_qualified() {
    let sql = r#"CREATE TABLE example.thing (
    created_at TIMESTAMPTZ NOT NULL DEFAULT pg_catalog.now()
);"#;
    let (tables, _, _) = parse_ddl(sql).unwrap();
    assert_eq!(tables.len(), 1);
    let created_at = tables[0]
        .columns
        .iter()
        .find(|c| c.name == "created_at")
        .expect("created_at column");
    assert_eq!(created_at.default, "now()");
}

#[test]
fn parse_ddl_column_default_funccall_with_args() {
    let sql = r#"CREATE TABLE example.thing (
    id integer PRIMARY KEY DEFAULT nextval('example_thing_id_seq'::regclass)
);"#;
    let (tables, _, _) = parse_ddl(sql).unwrap();
    assert_eq!(tables.len(), 1);
    let id = tables[0]
        .columns
        .iter()
        .find(|c| c.name == "id")
        .expect("id column");
    assert_eq!(id.default, "nextval('example_thing_id_seq'::regclass)");
}

#[test]
fn load_allow_drop_defs_columns() {
    let dir = tempdir().unwrap();
    let mut f = fs::File::create(dir.path().join("events.sql")).unwrap();
    writeln!(
        f,
        r"-- DROP TABLE public.events (
--     id integer,
--     event character varying(255)
-- );"
    )
    .unwrap();

    let defs = load_allow_drop_table_defs(dir.path()).unwrap();
    assert_eq!(defs.len(), 1);
    let tbl = defs.get("public.events").expect("events");
    assert_eq!(tbl.columns.len(), 2);
}
