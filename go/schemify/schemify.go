// Package schemify provides a library API for declarative schema application
// to PostgreSQL: load schema from SQL files, diff against the database,
// and apply only additive changes (fail on destructive changes).
package schemify

import (
	"context"
	"strings"

	"github.com/earlye/schemify/go/schemify/internal/apply"
	"github.com/earlye/schemify/go/schemify/internal/db"
	"github.com/earlye/schemify/go/schemify/internal/diff"
	"github.com/earlye/schemify/go/schemify/internal/schema"
	"github.com/go-errors/errors"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Options holds database connection and schema directory.
type Options struct {
	Host      string
	Port      string
	User      string
	Password  string
	Database  string
	SchemaDir string
}

// DBConfig returns connection config for the db package.
func (c *Options) DBConfig() *db.Config {
	return &db.Config{
		Host:     c.Host,
		Port:     c.Port,
		User:     c.User,
		Password: c.Password,
		Database: c.Database,
	}
}

// ApplyOptions configures apply behavior (dry-run, verbose).
type ApplyOptions struct {
	DryRun  bool
	Verbose bool
}

// LoadSchema reads desired schema from SQL files in dir (e.g. *.sql). Returns tables and indexes.
func LoadSchema(dir string) (*schema.LoadResult, error) {
	return schema.LoadFromDir(dir)
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

// LoadAllowDropTableDefs reads "-- DROP TABLE ... (" ... "-- );" blocks from SQL files in dir,
// parses each as CREATE TABLE and returns expected definitions for strict drop-table comparison.
func LoadAllowDropTableDefs(dir string) (map[string]*schema.Table, error) {
	return schema.LoadAllowDropTableDefs(dir)
}

// Apply runs additive migrations. Use ApplyOptions.DryRun to only print SQL.
func Apply(ctx context.Context, pool *pgxpool.Pool, migrations []diff.Migration, opts ApplyOptions) (string, error) {
	return apply.Apply(ctx, pool, migrations, apply.Options{
		DryRun:  opts.DryRun,
		Verbose: opts.Verbose,
	})
}

// Run connects to the database, loads desired schema from Config.SchemaDir,
// diffs against the current DB, and applies changes (or exits with error on destructive).
// It returns applied SQL (or dry-run SQL), or an error including destructive change messages.
func Run(ctx context.Context, cfg *Options, opts ApplyOptions) (sql string, err error) {
	pool, err := db.Connect(ctx, cfg.DBConfig())
	if err != nil {
		return "", errors.WrapPrefix(err, "connect", 0)
	}
	defer pool.Close()

	loadResult, err := LoadSchema(cfg.SchemaDir)
	if err != nil {
		return "", errors.WrapPrefix(err, "load schema", 0)
	}

	introResult, err := Introspect(ctx, pool, "public")
	if err != nil {
		return "", errors.WrapPrefix(err, "introspect", 0)
	}

	allowDropTableDefs, err := LoadAllowDropTableDefs(cfg.SchemaDir)
	if err != nil {
		return "", errors.WrapPrefix(err, "load allow-drop table defs", 0)
	}

	migrations, disallowed := Diff(loadResult.Tables, introResult.Tables, loadResult.Indexes, introResult.Indexes, allowDropTableDefs)
	if len(disallowed) > 0 {
		var msg []string
		msg = append(msg, "destructive changes are not allowed:")
		for _, d := range disallowed {
			msg = append(msg, "  - "+d.String())
		}
		return "", errors.Errorf("%s", strings.Join(msg, "\n"))
	}

	return Apply(ctx, pool, migrations, opts)
}
