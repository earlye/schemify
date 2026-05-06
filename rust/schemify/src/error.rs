use thiserror::Error;

#[derive(Debug, Error)]
pub enum Error {
    #[error("load schema: {0}")]
    LoadSchema(String),
    #[error("connect: {0}")]
    Connect(String),
    #[error("introspect: {0}")]
    Introspect(String),
    #[error("destructive changes are not allowed:\n{0}")]
    Destructive(String),
    #[error("apply: {0}")]
    Apply(String),
    #[error("parse SQL: {0}")]
    ParseSql(String),
    #[error("io: {0}")]
    Io(#[from] std::io::Error),
}

pub type Result<T> = std::result::Result<T, Error>;
