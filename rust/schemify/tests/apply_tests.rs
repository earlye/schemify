//! Port of Go `internal/apply` columnDef/SQL-rendering tests.

use schemify::diff::{Migration, MigrationDetail, KIND_ADD_COLUMN, KIND_ALTER_COLUMN, KIND_CREATE_TABLE};
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

fn alter_column_migration(old: Column, new: Column) -> Migration {
    Migration {
        kind: KIND_ALTER_COLUMN,
        schema: "public".into(),
        table: "things".into(),
        detail: MigrationDetail::AlterColumn {
            old_column: old,
            new_column: new,
        },
    }
}

#[test]
fn alter_column_sql_set_not_null() {
    let m = alter_column_migration(
        Column {
            name: "status".into(),
            type_: "text".into(),
            nullable: true,
            default: String::new(),
        },
        Column {
            name: "status".into(),
            type_: "text".into(),
            nullable: false,
            default: String::new(),
        },
    );
    let sql = migration_sql(&m).expect("migration_sql");
    assert_eq!(
        sql,
        "ALTER TABLE public.things ALTER COLUMN status SET NOT NULL"
    );
}

#[test]
fn alter_column_sql_drop_not_null() {
    let m = alter_column_migration(
        Column {
            name: "status".into(),
            type_: "text".into(),
            nullable: false,
            default: String::new(),
        },
        Column {
            name: "status".into(),
            type_: "text".into(),
            nullable: true,
            default: String::new(),
        },
    );
    let sql = migration_sql(&m).expect("migration_sql");
    assert_eq!(
        sql,
        "ALTER TABLE public.things ALTER COLUMN status DROP NOT NULL"
    );
}

#[test]
fn alter_column_sql_set_default() {
    let m = alter_column_migration(
        Column {
            name: "status".into(),
            type_: "text".into(),
            nullable: true,
            default: String::new(),
        },
        Column {
            name: "status".into(),
            type_: "text".into(),
            nullable: true,
            default: "'init'".into(),
        },
    );
    let sql = migration_sql(&m).expect("migration_sql");
    assert_eq!(
        sql,
        "ALTER TABLE public.things ALTER COLUMN status SET DEFAULT 'init'"
    );
}

#[test]
fn alter_column_sql_drop_default() {
    let m = alter_column_migration(
        Column {
            name: "status".into(),
            type_: "text".into(),
            nullable: true,
            default: "'init'".into(),
        },
        Column {
            name: "status".into(),
            type_: "text".into(),
            nullable: true,
            default: String::new(),
        },
    );
    let sql = migration_sql(&m).expect("migration_sql");
    assert_eq!(
        sql,
        "ALTER TABLE public.things ALTER COLUMN status DROP DEFAULT"
    );
}

#[test]
fn alter_column_sql_type_change() {
    let m = alter_column_migration(
        Column {
            name: "amount".into(),
            type_: "integer".into(),
            nullable: true,
            default: String::new(),
        },
        Column {
            name: "amount".into(),
            type_: "numeric(12,2)".into(),
            nullable: true,
            default: String::new(),
        },
    );
    let sql = migration_sql(&m).expect("migration_sql");
    assert_eq!(
        sql,
        "ALTER TABLE public.things ALTER COLUMN amount TYPE numeric(12,2)"
    );
}

#[test]
fn alter_column_sql_combination() {
    let m = alter_column_migration(
        Column {
            name: "status".into(),
            type_: "text".into(),
            nullable: true,
            default: String::new(),
        },
        Column {
            name: "status".into(),
            type_: "character varying(50)".into(),
            nullable: false,
            default: "'init'".into(),
        },
    );
    let sql = migration_sql(&m).expect("migration_sql");
    assert_eq!(
        sql,
        "ALTER TABLE public.things ALTER COLUMN status SET NOT NULL, ALTER COLUMN status SET DEFAULT 'init', ALTER COLUMN status TYPE character varying(50)"
    );
}
