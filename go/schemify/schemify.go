// Package schemify provides a library API for declarative schema application
// to PostgreSQL: load schema from SQL files, diff against the database,
// and apply only additive changes (fail on destructive changes).
package schemify

import (
	"context"
	"io/fs"
	"strings"

	"github.com/earlye/schemify/go/schemify/internal/apply"
	"github.com/earlye/schemify/go/schemify/internal/db"
	"github.com/earlye/schemify/go/schemify/internal/diff"
	"github.com/earlye/schemify/go/schemify/internal/schema"
	"github.com/go-errors/errors"
	"github.com/jackc/pgx/v5/pgxpool"
)

type DatabaseConfig = db.Config

// ApplyOptions configures apply behavior (dry-run, verbose).
type ApplyOptions struct {
	DryRun  bool
	Verbose bool
}

// Options holds database connection and schema directory.
type Options struct {
	Schema       fs.FS
	Database     DatabaseConfig
	ApplyOptions ApplyOptions
}

// LoadSchema reads desired schema from SQL files in fsys (e.g. *.sql). Returns tables and indexes.
func LoadSchema(fsys fs.FS) (*schema.LoadResult, error) {
	return schema.LoadFromFS(fsys)
}

// Connect creates a connection pool. Be sure to defer Close() on the returned pool.
func Connect(ctx context.Context, opts Options) (*pgxpool.Pool, error) {
	return db.Connect(ctx, &opts.Database)
}

// Introspect returns the current schema from the database (tables with constraints, and indexes).
func Introspect(ctx context.Context, pool *pgxpool.Pool, schemaName string) (*db.IntrospectResult, error) {
	return db.Introspect(ctx, pool, schemaName)
}

// Diff compares desired vs actual (tables and indexes) and returns migrations and disallowed changes.
// allowDropTableDefs is from LoadAllowDropTableDefs (expected table defs from "-- DROP TABLE ... (" blocks); pass nil if none.
func Diff(desired, actual map[string]*schema.Table, desiredIndexes, actualIndexes map[string]*schema.Index, allowDropTableDefs map[string]*schema.Table) (migrations []diff.Migration, disallowed []diff.DestructiveChange) {
	return diff.Diff(desired, actual, desiredIndexes, actualIndexes, allowDropTableDefs)
}

// LoadAllowDropTableDefs reads "-- DROP TABLE ... (" ... "-- );" blocks from SQL files in fsys,
// parses each as CREATE TABLE and returns expected definitions for strict drop-table comparison.
func LoadAllowDropTableDefs(fsys fs.FS) (map[string]*schema.Table, error) {
	return schema.LoadAllowDropTableDefs(fsys)
}

// Apply runs additive migrations. Use ApplyOptions.DryRun to only print SQL.
func Apply(ctx context.Context, pool *pgxpool.Pool, migrations []diff.Migration, opts ApplyOptions) (string, error) {
	return apply.Apply(ctx, pool, migrations, apply.Options{
		DryRun:  opts.DryRun,
		Verbose: opts.Verbose,
	})
}

// Plan computes the minimal set of migrations to apply to the database, and enforces
// that destructive changes are disallowed (barring overrides).
func Plan(ctx context.Context, cfg *Options, pool *pgxpool.Pool) ([]diff.Migration, error) {
	loadResult, err := LoadSchema(cfg.Schema)
	if err != nil {
		return nil, errors.WrapPrefix(err, "load schema", 0)
	}

	introResult, err := Introspect(ctx, pool, "public")
	if err != nil {
		return nil, errors.WrapPrefix(err, "introspect", 0)
	}

	allowDropTableDefs, err := LoadAllowDropTableDefs(cfg.Schema)
	if err != nil {
		return nil, errors.WrapPrefix(err, "load allow-drop table defs", 0)
	}

	migrations, disallowed := Diff(loadResult.Tables, introResult.Tables, loadResult.Indexes, introResult.Indexes, allowDropTableDefs)
	if len(disallowed) > 0 {
		var msg []string
		msg = append(msg, "destructive changes are not allowed:")
		for _, d := range disallowed {
			msg = append(msg, "  - "+d.String())
		}
		return nil, errors.Errorf("%s", strings.Join(msg, "\n"))
	}
	return migrations, nil
}

// Run connects to the database, loads desired schema from Config.SchemaDir,
// diffs against the current DB, and applies changes (or exits with error on destructive).
// It returns applied SQL (or dry-run SQL), or an error including destructive change messages.
func Run(ctx context.Context, cfg *Options) (sql string, err error) {
	pool, err := db.Connect(ctx, &cfg.Database)
	if err != nil {
		return "", errors.WrapPrefix(err, "connect", 0)
	}
	defer pool.Close()

	migrations, err := Plan(ctx, cfg, pool)
	if err != nil {
		return "", err
	}

	return Apply(ctx, pool, migrations, cfg.ApplyOptions)
}
