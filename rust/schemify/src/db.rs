//! PostgreSQL connection and introspection (ported from Go internal/db).

use crate::error::{Error, Result};
use crate::helpers::{predicted_foreign_key_constraint_name, predicted_unique_constraint_name};
use crate::schema::{
    self, Column, ForeignKey, Index, PrimaryKeyConstraint, Table, UniqueConstraint,
    normalize_info_schema_type,
};
use std::collections::HashMap;
use tokio_postgres::{Client, NoTls};

#[derive(Debug, Clone)]
pub struct DatabaseConfig {
    pub host: String,
    pub port: String,
    pub user: String,
    pub password: String,
    pub database: String,
    pub ssl_mode: String,
    pub ssl_root_cert: String,
    pub ssl_cert: String,
    pub ssl_key: String,
}

impl Default for DatabaseConfig {
    fn default() -> Self {
        Self {
            host: String::new(),
            port: String::new(),
            user: String::new(),
            password: String::new(),
            database: String::new(),
            ssl_mode: String::new(),
            ssl_root_cert: String::new(),
            ssl_cert: String::new(),
            ssl_key: String::new(),
        }
    }
}

impl DatabaseConfig {
    pub fn dsn(&self) -> Result<String> {
        if self.host.is_empty()
            || self.user.is_empty()
            || self.database.is_empty()
            || self.password.is_empty()
        {
            return Err(Error::Connect(
                "schema: missing required DB_* env (need DB_HOST, DB_USER, DB_NAME, DB_PASSWORD)"
                    .into(),
            ));
        }
        let mut port = self.port.clone();
        if port.is_empty() {
            port = "5432".into();
        }
        let mut ssl_mode = self.ssl_mode.clone();
        if ssl_mode.is_empty() {
            ssl_mode = "require".into();
        }

        let mut q = format!("sslmode={}", urlencoding_encode(&ssl_mode));
        if !self.ssl_root_cert.is_empty() {
            q.push_str("&sslrootcert=");
            q.push_str(&urlencoding_encode(&self.ssl_root_cert));
        }
        if !self.ssl_cert.is_empty() {
            q.push_str("&sslcert=");
            q.push_str(&urlencoding_encode(&self.ssl_cert));
        }
        if !self.ssl_key.is_empty() {
            q.push_str("&sslkey=");
            q.push_str(&urlencoding_encode(&self.ssl_key));
        }

        let user_enc = urlencoding_encode(&self.user);
        let pass_enc = urlencoding_encode(&self.password);
        Ok(format!(
            "postgresql://{}:{}@{}:{}/{}?{}",
            user_enc,
            pass_enc,
            self.host,
            port,
            urlencoding_encode(&self.database),
            q
        ))
    }
}

fn urlencoding_encode(s: &str) -> String {
    let mut out = String::new();
    for b in s.bytes() {
        match b {
            b'A'..=b'Z' | b'a'..=b'z' | b'0'..=b'9' | b'-' | b'_' | b'.' | b'~' => {
                out.push(b as char)
            }
            _ => out.push_str(&format!("%{:02X}", b)),
        }
    }
    out
}

pub async fn connect(cfg: &DatabaseConfig) -> Result<Client> {
    let dsn = cfg.dsn()?;
    let (client, connection) = tokio_postgres::connect(&dsn, NoTls)
        .await
        .map_err(|e| Error::Connect(format!("connect: {e}")))?;

    tokio::spawn(async move {
        let _ = connection.await;
    });

    client
        .simple_query("SELECT 1")
        .await
        .map_err(|e| Error::Connect(format!("ping: {e}")))?;

    Ok(client)
}

#[derive(Debug, Default)]
pub struct IntrospectResult {
    pub tables: HashMap<String, Table>,
    pub indexes: HashMap<String, Index>,
}

pub async fn introspect(client: &Client, schema_name: &str) -> Result<IntrospectResult> {
    let schema_name = if schema_name.is_empty() {
        "public"
    } else {
        schema_name
    };

    let tables_list = list_tables(client, schema_name).await?;
    let mut tables_out: HashMap<String, Table> = HashMap::new();

    for table_name in tables_list {
        let cols = list_columns(client, schema_name, &table_name)
            .await
            .map_err(|e| Error::Introspect(format!("table {schema_name}.{table_name}: {e}")))?;
        let t = Table {
            schema: schema_name.into(),
            name: table_name.clone(),
            columns: cols,
            allow_drop_columns: Vec::new(),
            primary_key: None,
            unique_keys: Vec::new(),
            foreign_keys: Vec::new(),
        };
        tables_out.insert(schema::table_key(schema_name, &table_name), t);
    }

    list_primary_or_unique_constraints(client, schema_name, &mut tables_out).await?;
    list_foreign_keys(client, schema_name, &mut tables_out).await?;

    let indexes_out = list_indexes(client, schema_name)
        .await
        .map_err(|e| Error::Introspect(format!("list indexes: {e}")))?;

    Ok(IntrospectResult {
        tables: tables_out,
        indexes: indexes_out,
    })
}

/// Non-system namespace names present in the database.
pub async fn list_user_schemas(client: &Client) -> Result<std::collections::HashSet<String>> {
    let rows = client
        .query(
            "SELECT nspname FROM pg_namespace
             WHERE nspname NOT IN ('pg_catalog', 'information_schema')
               AND nspname NOT LIKE 'pg_toast%'
               AND nspname NOT LIKE 'pg\\_%'
             ORDER BY nspname",
            &[],
        )
        .await
        .map_err(|e| Error::Introspect(e.to_string()))?;
    let mut out = std::collections::HashSet::new();
    for row in rows {
        let name: String = row.get(0);
        let name = name.to_lowercase();
        if crate::namespace::is_system_namespace(&name) {
            continue;
        }
        out.insert(name);
    }
    Ok(out)
}

pub async fn introspect_all(
    client: &Client,
    schema_names: &[String],
) -> Result<IntrospectResult> {
    let mut tables_out = HashMap::new();
    let mut indexes_out = HashMap::new();
    for ns in schema_names {
        if crate::namespace::is_system_namespace(ns) {
            continue;
        }
        let part = introspect(client, ns).await?;
        tables_out.extend(part.tables);
        indexes_out.extend(part.indexes);
    }
    Ok(IntrospectResult {
        tables: tables_out,
        indexes: indexes_out,
    })
}

async fn list_tables(client: &Client, schema_name: &str) -> Result<Vec<String>> {
    let rows = client
        .query(
            "SELECT table_name FROM information_schema.tables
             WHERE table_schema = $1 AND table_type = 'BASE TABLE'
             ORDER BY table_name",
            &[&schema_name],
        )
        .await
        .map_err(|e| Error::Introspect(e.to_string()))?;
    Ok(rows.into_iter().map(|r| r.get::<_, String>(0)).collect())
}

async fn list_columns(client: &Client, schema_name: &str, table_name: &str) -> Result<Vec<Column>> {
    let rows = client
        .query(
            "SELECT column_name, column_default, data_type, character_maximum_length
             FROM information_schema.columns
             WHERE table_schema = $1 AND table_name = $2
             ORDER BY ordinal_position",
            &[&schema_name, &table_name],
        )
        .await
        .map_err(|e| Error::Introspect(e.to_string()))?;

    let mut cols = Vec::new();
    for row in rows {
        let name: String = row.get(0);
        let def: Option<String> = row.get(1);
        let data_type: String = row.get(2);
        let max_len: Option<i32> = row.get(3);
        let mut pg_type = data_type.clone();
        if let Some(n) = max_len {
            if n > 0 {
                pg_type = format!("{data_type}({n})");
            }
        }
        cols.push(Column {
            name,
            type_: normalize_info_schema_type(&pg_type),
            nullable: true,
            default: def.unwrap_or_default(),
        });
    }
    Ok(cols)
}

async fn list_primary_or_unique_constraints(
    client: &Client,
    schema_name: &str,
    tables: &mut HashMap<String, Table>,
) -> Result<()> {
    let rows = client
        .query(
            "SELECT tc.constraint_name, tc.constraint_type, tc.table_name, kcu.column_name, kcu.ordinal_position
             FROM information_schema.table_constraints tc
             JOIN information_schema.key_column_usage kcu
               ON tc.constraint_schema = kcu.constraint_schema AND tc.constraint_name = kcu.constraint_name AND tc.table_schema = kcu.table_schema AND tc.table_name = kcu.table_name
             WHERE tc.table_schema = $1 AND tc.constraint_type IN ('PRIMARY KEY', 'UNIQUE')
             ORDER BY tc.table_name, tc.constraint_name, kcu.ordinal_position",
            &[&schema_name],
        )
        .await
        .map_err(|e| Error::Introspect(e.to_string()))?;

    #[derive(Hash, Eq, PartialEq)]
    struct Key {
        table: String,
        name: String,
    }
    let mut pk_cols: HashMap<Key, Vec<String>> = HashMap::new();
    let mut unique_cols: HashMap<Key, Vec<String>> = HashMap::new();

    for row in rows {
        let cname: String = row.get(0);
        let ctype: String = row.get(1);
        let tname: String = row.get(2);
        let col: String = row.get(3);
        let k = Key {
            table: tname.clone(),
            name: cname.clone(),
        };
        if ctype == "PRIMARY KEY" {
            pk_cols.entry(k).or_default().push(col);
        } else {
            unique_cols.entry(k).or_default().push(col);
        }
    }

    for (k, cols) in pk_cols {
        let tbl_key = schema::table_key(schema_name, &k.table);
        if let Some(t) = tables.get_mut(&tbl_key) {
            t.primary_key = Some(PrimaryKeyConstraint {
                name: k.name.clone(),
                columns: cols,
            });
        }
    }

    for (k, cols) in unique_cols {
        let tbl_key = schema::table_key(schema_name, &k.table);
        if let Some(t) = tables.get_mut(&tbl_key) {
            let name = if k.name.is_empty() {
                predicted_unique_constraint_name(&k.table, &cols)
            } else {
                k.name.clone()
            };
            t.unique_keys.push(UniqueConstraint {
                name,
                columns: cols,
            });
        }
    }

    Ok(())
}

async fn list_foreign_keys(
    client: &Client,
    schema_name: &str,
    tables: &mut HashMap<String, Table>,
) -> Result<()> {
    use std::collections::hash_map::Entry;

    let rows = client
        .query(
            "SELECT tc.table_name, tc.constraint_name, kcu.column_name, kcu.ordinal_position,
                    rc.delete_rule, rc.update_rule,
                    kcu_ref.table_schema AS ref_schema, kcu_ref.table_name AS ref_table, kcu_ref.column_name AS ref_column
             FROM information_schema.table_constraints tc
             JOIN information_schema.key_column_usage kcu ON tc.constraint_schema = kcu.constraint_schema AND tc.constraint_name = kcu.constraint_name AND tc.table_schema = kcu.table_schema AND tc.table_name = kcu.table_name
             JOIN information_schema.referential_constraints rc ON tc.constraint_schema = rc.constraint_schema AND tc.constraint_name = rc.constraint_name
             JOIN information_schema.key_column_usage kcu_ref ON kcu_ref.constraint_schema = rc.unique_constraint_schema AND kcu_ref.constraint_name = rc.unique_constraint_name AND kcu_ref.ordinal_position = kcu.position_in_unique_constraint
             WHERE tc.table_schema = $1 AND tc.constraint_type = 'FOREIGN KEY'
             ORDER BY tc.table_name, tc.constraint_name, kcu.ordinal_position",
            &[&schema_name],
        )
        .await
        .map_err(|e| Error::Introspect(e.to_string()))?;

    #[derive(Clone)]
    struct FkRow {
        cols: Vec<String>,
        ref_cols: Vec<String>,
        ref_schema: String,
        ref_table: String,
        on_del: String,
        on_upd: String,
    }

    let mut by_fk: HashMap<(String, String), FkRow> = HashMap::new();
    for row in rows {
        let tname: String = row.get(0);
        let cname: String = row.get(1);
        let col: String = row.get(2);
        let del_rule: String = row.get(4);
        let up_rule: String = row.get(5);
        let rs: String = row.get(6);
        let rt: String = row.get(7);
        let rc: String = row.get(8);
        let key = (tname, cname);
        match by_fk.entry(key) {
            Entry::Occupied(mut e) => {
                e.get_mut().cols.push(col);
                e.get_mut().ref_cols.push(rc);
            }
            Entry::Vacant(v) => {
                v.insert(FkRow {
                    cols: vec![col],
                    ref_cols: vec![rc],
                    ref_schema: rs,
                    ref_table: rt,
                    on_del: del_rule,
                    on_upd: up_rule,
                });
            }
        }
    }

    for ((tname, cname), d) in by_fk {
        let tbl_key = schema::table_key(schema_name, &tname);
        if let Some(t) = tables.get_mut(&tbl_key) {
            let name = if cname.is_empty() {
                predicted_foreign_key_constraint_name(&tname, &d.cols)
            } else {
                cname.clone()
            };
            t.foreign_keys.push(ForeignKey {
                name,
                columns: d.cols,
                references_schema: d.ref_schema,
                references_table: d.ref_table,
                references_columns: d.ref_cols,
                on_delete: d.on_del,
                on_update: d.on_upd,
            });
        }
    }

    Ok(())
}

async fn list_indexes(client: &Client, schema_name: &str) -> Result<HashMap<String, Index>> {
    let rows = client
        .query(
            "SELECT n.nspname, c.relname AS indexname, t.relname AS tablename, tn.nspname AS tableschema,
                    i.indisunique, am.amname AS indextype,
                    (SELECT array_agg(pg_get_indexdef(i.indexrelid, (ord + 1)::int, false) ORDER BY ord)
                     FROM generate_subscripts(i.indkey, 1) AS ord) AS columns
             FROM pg_index i
             JOIN pg_class c ON c.oid = i.indexrelid
             JOIN pg_namespace n ON n.oid = c.relnamespace
             JOIN pg_class t ON t.oid = i.indrelid
             JOIN pg_namespace tn ON tn.oid = t.relnamespace
             JOIN pg_am am ON am.oid = c.relam
             WHERE n.nspname = $1 AND c.relkind = 'i'
               AND NOT EXISTS (
                   SELECT 1 FROM pg_constraint con
                   WHERE con.conindid = c.oid
                     AND con.contype IN ('p', 'u')
               )",
            &[&schema_name],
        )
        .await
        .map_err(|e| Error::Introspect(e.to_string()))?;

    let mut out = HashMap::new();
    for row in rows {
        let idx_schema: String = row.get(0);
        let idx_name: String = row.get(1);
        let table_name: String = row.get(2);
        let table_schema: String = row.get(3);
        let is_unique: bool = row.get(4);
        let index_type: String = row.get(5);
        let cols: Option<Vec<Option<String>>> = row.get(6);

        let cols: Vec<String> = cols.unwrap_or_default().into_iter().flatten().collect();

        let idx = Index {
            name: idx_name.clone(),
            schema: idx_schema.clone(),
            table_schema,
            table_name,
            columns: cols,
            unique: is_unique,
            index_type,
            concurrently: true,
        };
        let key = schema::index_key(&idx.schema, &idx.name);
        out.insert(key, idx);
    }

    Ok(out)
}
