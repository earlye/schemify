use clap::Parser;
use schemify::{ApplyOptions, DatabaseConfig, Options};
use std::path::PathBuf;
use tracing_subscriber::EnvFilter;

#[derive(Parser, Debug)]
#[command(name = "schemify")]
#[command(
    about = "Apply declarative SQL schema to PostgreSQL (additive only; fails on destructive changes)"
)]
struct Cli {
    #[arg(short = 'H', long, env = "DB_HOST", default_value = "localhost")]
    host: String,
    #[arg(short = 'p', long, env = "DB_PORT", default_value = "5432")]
    port: String,
    #[arg(short = 'U', long, env = "DB_USER", default_value = "schemify")]
    user: String,
    #[arg(short = 'P', long, env = "DB_PASSWORD", default_value = "schemify")]
    password: String,
    #[arg(short = 'd', long, env = "DB_NAME", default_value = "schemify")]
    database: String,
    #[arg(
        short = 'S',
        long = "ssl-mode",
        env = "DB_SSLMODE",
        default_value = "require"
    )]
    ssl_mode: String,
    #[arg(
        short = 'R',
        long = "ssl-root-cert",
        env = "DB_SSLROOTCERT",
        default_value = ""
    )]
    ssl_root_cert: String,
    #[arg(short = 'C', long = "ssl-cert", env = "DB_SSLCERT", default_value = "")]
    ssl_cert: String,
    #[arg(short = 'K', long = "ssl-key", env = "DB_SSLKEY", default_value = "")]
    ssl_key: String,
    #[arg(
        short = 's',
        long = "schema",
        env = "SCHEMA_DIR",
        default_value = "./schemas/demo-v01"
    )]
    schema_dir: PathBuf,
    #[arg(short = 'n', long = "dry-run")]
    dry_run: bool,
    /// Increase verbosity (-v debug, -vv trace); overrides RUST_LOG when set.
    #[arg(short = 'v', long, action = clap::ArgAction::Count)]
    verbose: u8,
}

#[tokio::main]
async fn main() -> std::process::ExitCode {
    let cli = Cli::parse();

    let filter = match cli.verbose {
        0 => EnvFilter::try_from_default_env().unwrap_or_else(|_| EnvFilter::new("info")),
        1 => EnvFilter::new("debug"),
        _ => EnvFilter::new("trace"),
    };
    tracing_subscriber::fmt()
        .with_env_filter(filter)
        .with_target(false)
        .init();

    let cfg = Options {
        schema_dir: cli.schema_dir,
        database: DatabaseConfig {
            host: cli.host,
            port: cli.port,
            user: cli.user,
            password: cli.password,
            database: cli.database,
            ssl_mode: cli.ssl_mode,
            ssl_root_cert: cli.ssl_root_cert,
            ssl_cert: cli.ssl_cert,
            ssl_key: cli.ssl_key,
        },
        apply_options: ApplyOptions {
            dry_run: cli.dry_run,
        },
    };

    match schemify::run(&cfg).await {
        Ok(()) => std::process::ExitCode::SUCCESS,
        Err(e) => {
            eprintln!("{e}");
            std::process::ExitCode::from(1)
        }
    }
}
