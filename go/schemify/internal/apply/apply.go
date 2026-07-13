package apply

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/earlye/schemify/go/schemify/internal/diff"
	"github.com/earlye/schemify/go/schemify/internal/schema"
	"github.com/go-errors/errors"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Options configures apply behavior.
type Options struct {
	DryRun bool
}

// Apply runs the additive migrations. create_index (CONCURRENTLY) runs outside a transaction;
// all other migrations run in a single transaction (unless DryRun).
// If DryRun is true, SQL is returned as a string and no changes are made.
func Apply(ctx context.Context, pool *pgxpool.Pool, migrations []diff.Migration, opts Options) error {
	for _, m := range migrations {
		sql, err := migrationSQL(m)
		if err != nil {
			return err
		}
		slog.Debug("Plan", "sql", sql)
	}
	if opts.DryRun {
		return nil
	}

	// Split: create_index must run outside transaction (CONCURRENTLY); rest in one tx.
	var inTx, outTx []diff.Migration
	for _, m := range migrations {
		if m.Kind == diff.KindCreateIndex || m.Kind == diff.KindDropIndex {
			outTx = append(outTx, m)
		} else {
			inTx = append(inTx, m)
		}
	}

	if len(inTx) > 0 {
		tx, err := pool.Begin(ctx)
		if err != nil {
			return errors.WrapPrefix(err, "begin transaction", 0)
		}
		defer tx.Rollback(ctx) //nolint:errcheck

		for _, m := range inTx {
			sql, err := migrationSQL(m)
			if err != nil {
				return err
			}
			slog.Debug("Running:", "sql", sql)
			if _, err := tx.Exec(ctx, sql); err != nil {
				return errors.WrapPrefix(err, fmt.Sprintf("execute %s", sql), 0)
			}
		}

		if err := tx.Commit(ctx); err != nil {
			return errors.WrapPrefix(err, "commit", 0)
		}
	}

	for _, m := range outTx {
		sql, err := migrationSQL(m)
		if err != nil {
			return err
		}
		slog.Debug("Running:", "sql", sql)
		if _, err := pool.Exec(ctx, sql); err != nil {
			return errors.WrapPrefix(err, fmt.Sprintf("pool.Exec %s", sql), 0)
		}
	}
	return nil
}

func migrationSQL(m diff.Migration) (string, error) {
	switch d := m.Detail.(type) {
	case *diff.CreateSchemaDetail:
		return createSchemaSQL(m.Schema), nil
	case *diff.CreateTableDetail:
		return createTableSQL(d.TableDef), nil
	case *diff.AddColumnDetail:
		return addColumnSQL(m.Schema, m.Table, d.Column), nil
	case *diff.AlterColumnDetail:
		return alterColumnSQL(m.Schema, m.Table, d.OldColumn, d.NewColumn), nil
	case *diff.DropColumnDetail:
		return dropColumnSQL(m.Schema, m.Table, d.ColumnName), nil
	case *diff.DropTableDetail:
		return dropTableSQL(m.Schema, m.Table), nil
	case *diff.CreateIndexDetail:
		return createIndexSQL(d.Index), nil
	case *diff.AddPKDetail:
		return addPKSQL(m.Schema, m.Table, d.PrimaryKey), nil
	case *diff.AddUniqueDetail:
		return addUniqueSQL(m.Schema, m.Table, d.UniqueKey), nil
	case *diff.AddFKDetail:
		return addFKSQL(m.Schema, m.Table, d.ForeignKey), nil
	case *diff.DropUniqueDetail:
		return fmt.Sprintf("ALTER TABLE %s.%s DROP CONSTRAINT IF EXISTS %s", m.Schema, m.Table, d.ConstraintName), nil
	case *diff.DropFKDetail:
		return fmt.Sprintf("ALTER TABLE %s.%s DROP CONSTRAINT IF EXISTS %s", m.Schema, m.Table, d.ConstraintName), nil
	case *diff.DropIndexDetail:
		return fmt.Sprintf("DROP INDEX CONCURRENTLY IF EXISTS %s.%s", d.Index.Schema, d.Index.Name), nil
	default:
		return "", fmt.Errorf("unknown migration detail: %T", m.Detail)
	}
}

func createSchemaSQL(schemaName string) string {
	return fmt.Sprintf("CREATE SCHEMA IF NOT EXISTS %s", schemaName)
}

func createTableSQL(t *schema.Table) string {
	var parts []string
	for _, c := range t.Columns {
		parts = append(parts, columnDef(&c))
	}
	if t.PrimaryKey != nil && len(t.PrimaryKey.Columns) > 0 {
		parts = append(parts, fmt.Sprintf("PRIMARY KEY (%s)", strings.Join(t.PrimaryKey.Columns, ", ")))
	}
	for _, u := range t.UniqueKeys {
		if u.Name == "" {
			panic("internal invariant violation: unique constraint name is empty")
		}
		if len(u.Columns) > 0 {
			clause := fmt.Sprintf("UNIQUE (%s)", strings.Join(u.Columns, ", "))
			if u.Name != "" {
				clause = fmt.Sprintf("CONSTRAINT %s %s", u.Name, clause)
			}
			parts = append(parts, clause)
		}
	}
	for _, fk := range t.ForeignKeys {
		if fk.Name == "" {
			panic("internal invariant violation: foreign key name is empty")
		}
		if len(fk.Columns) > 0 && fk.ReferencesTable != "" {
			ref := fmt.Sprintf("%s.%s", fk.ReferencesSchema, fk.ReferencesTable)
			if fk.ReferencesSchema == "" {
				ref = fk.ReferencesTable
			}
			clause := fmt.Sprintf("FOREIGN KEY (%s) REFERENCES %s (%s)", strings.Join(fk.Columns, ", "), ref, strings.Join(fk.ReferencesColumns, ", "))
			if fk.Name != "" {
				clause = fmt.Sprintf("CONSTRAINT %s %s", fk.Name, clause)
			}
			if fk.OnDelete != "" && fk.OnDelete != "NO ACTION" {
				clause += " ON DELETE " + fk.OnDelete
			}
			if fk.OnUpdate != "" && fk.OnUpdate != "NO ACTION" {
				clause += " ON UPDATE " + fk.OnUpdate
			}
			parts = append(parts, clause)
		}
	}
	cols := strings.Join(parts, ", ")
	return fmt.Sprintf("CREATE TABLE %s.%s (%s)", t.Schema, t.Name, cols)
}

func createIndexSQL(idx *schema.Index) string {
	// CONCURRENTLY is required (enforced at load); must be run outside transaction.
	// Index name is not schema-qualified; PostgreSQL creates the index in the table's schema.
	qual := ""
	if idx.Unique {
		qual = "UNIQUE "
	}
	using := ""
	if idx.IndexType != "" && idx.IndexType != "btree" {
		using = " USING " + idx.IndexType
	}
	return fmt.Sprintf("CREATE %sINDEX CONCURRENTLY IF NOT EXISTS %s ON %s.%s (%s)%s", qual, idx.Name, idx.TableSchema, idx.TableName, strings.Join(idx.Columns, ", "), using)
}

func addPKSQL(schemaName, tableName string, pk *schema.PrimaryKeyConstraint) string {
	return fmt.Sprintf("ALTER TABLE %s.%s ADD PRIMARY KEY (%s)", schemaName, tableName, strings.Join(pk.Columns, ", "))
}

func addUniqueSQL(schemaName, tableName string, u *schema.UniqueConstraint) string {
	if u.Name == "" {
		panic("internal invariant violation: unique constraint name is empty")
	}
	return fmt.Sprintf("ALTER TABLE %s.%s ADD CONSTRAINT %s UNIQUE (%s)", schemaName, tableName, u.Name, strings.Join(u.Columns, ", "))
}

func addFKSQL(schemaName, tableName string, fk *schema.ForeignKey) string {
	if fk.Name == "" {
		panic("internal invariant violation: foreign key name is empty")
	}
	ref := fmt.Sprintf("%s.%s", fk.ReferencesSchema, fk.ReferencesTable)
	if fk.ReferencesSchema == "" {
		ref = fk.ReferencesTable
	}
	s := fmt.Sprintf("ALTER TABLE %s.%s ADD CONSTRAINT %s FOREIGN KEY (%s) REFERENCES %s (%s)", schemaName, tableName, fk.Name, strings.Join(fk.Columns, ", "), ref, strings.Join(fk.ReferencesColumns, ", "))
	if fk.OnDelete != "" && fk.OnDelete != "NO ACTION" {
		s += " ON DELETE " + fk.OnDelete
	}
	if fk.OnUpdate != "" && fk.OnUpdate != "NO ACTION" {
		s += " ON UPDATE " + fk.OnUpdate
	}
	return s
}

func addColumnSQL(schema, table string, c *schema.Column) string {
	return fmt.Sprintf("ALTER TABLE %s.%s ADD COLUMN %s", schema, table, columnDef(c))
}

func alterColumnSQL(schemaName, table string, old, new *schema.Column) string {
	var clauses []string
	if old.Nullable != new.Nullable {
		if new.Nullable {
			clauses = append(clauses, fmt.Sprintf("ALTER COLUMN %s DROP NOT NULL", new.Name))
		} else {
			clauses = append(clauses, fmt.Sprintf("ALTER COLUMN %s SET NOT NULL", new.Name))
		}
	}
	if old.Default != new.Default {
		if new.Default == "" {
			clauses = append(clauses, fmt.Sprintf("ALTER COLUMN %s DROP DEFAULT", new.Name))
		} else {
			clauses = append(clauses, fmt.Sprintf("ALTER COLUMN %s SET DEFAULT %s", new.Name, new.Default))
		}
	}
	if old.Type != new.Type {
		clauses = append(clauses, fmt.Sprintf("ALTER COLUMN %s TYPE %s", new.Name, new.Type))
	}
	return fmt.Sprintf("ALTER TABLE %s.%s %s", schemaName, table, strings.Join(clauses, ", "))
}

func dropColumnSQL(schema, table, columnName string) string {
	return fmt.Sprintf("ALTER TABLE %s.%s DROP COLUMN IF EXISTS %s", schema, table, columnName)
}

func dropTableSQL(schema, table string) string {
	return fmt.Sprintf("DROP TABLE %s.%s", schema, table)
}

func columnDef(c *schema.Column) string {
	s := c.Name + " " + c.Type
	if !c.Nullable {
		s += " NOT NULL"
	}
	if c.Default != "" {
		s += " DEFAULT " + c.Default
	}
	return s
}
