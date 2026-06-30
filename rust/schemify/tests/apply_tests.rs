//! Port of Go `internal/apply` columnDef/SQL-rendering tests.

use schemify::diff::{Migration, MigrationDetail, KIND_ADD_COLUMN, KIND_CREATE_TABLE};
use schemify::migration_sql;
use schemify::schema::Column;

#[test]
fn add_column_sql_includes_not_null_default() {
    let m = Migration {
        kind: KIND_ADD_COLUMN,
        schema: "public".into(),
        table: "things".into(),
        detail: MigrationDetail::AddColumn {
            column: Column {
                name: "status".into(),
                type_: "text".into(),
                nullable: false,
                default: "'init'".into(),
            },
        },
    };
    let sql = migration_sql(&m).expect("migration_sql");
    assert_eq!(
        sql,
        "ALTER TABLE public.things ADD COLUMN status text NOT NULL DEFAULT 'init'"
    );
}

#[test]
fn create_table_sql_includes_not_null_default() {
    let m = Migration {
        kind: KIND_CREATE_TABLE,
        schema: "public".into(),
        table: "things".into(),
        detail: MigrationDetail::CreateTable {
            table_def: schemify::schema::Table {
                schema: "public".into(),
                name: "things".into(),
                columns: vec![
                    Column {
                        name: "id".into(),
                        type_: "text".into(),
                        nullable: false,
                        default: String::new(),
                    },
                    Column {
                        name: "status".into(),
                        type_: "text".into(),
                        nullable: false,
                        default: "'init'".into(),
                    },
                ],
                allow_drop_columns: Vec::new(),
                primary_key: None,
                unique_keys: Vec::new(),
                foreign_keys: Vec::new(),
            },
        },
    };
    let sql = migration_sql(&m).expect("migration_sql");
    assert_eq!(
        sql,
        "CREATE TABLE public.things (id text NOT NULL, status text NOT NULL DEFAULT 'init')"
    );
}
