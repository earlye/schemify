package apply

import (
	"context"
	"fmt"
	"strings"

	"github.com/earlye/schemify/internal/diff"
	"github.com/earlye/schemify/internal/schema"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Options configures apply behavior.
type Options struct {
	DryRun bool
	Verbose bool
}

// Apply runs the additive migrations. create_index (CONCURRENTLY) runs outside a transaction;
// all other migrations run in a single transaction (unless DryRun).
// If DryRun is true, SQL is returned as a string and no changes are made.
func Apply(ctx context.Context, pool *pgxpool.Pool, migrations []diff.Migration, opts Options) (executedSQL string, err error) {
	var sb strings.Builder
	if opts.DryRun {
		for _, m := range migrations {
			sql, err := migrationSQL(m)
			if err != nil {
				return "", err
			}
			if opts.Verbose {
				sb.WriteString(sql)
				sb.WriteString("\n")
			}
			sb.WriteString("-- would run: ")
			sb.WriteString(sql)
			sb.WriteString("\n")
		}
		return sb.String(), nil
	}

	// Split: create_index must run outside transaction (CONCURRENTLY); rest in one tx.
	var inTx, outTx []diff.Migration
	for _, m := range migrations {
		if m.Kind == "create_index" {
			outTx = append(outTx, m)
		} else {
			inTx = append(inTx, m)
		}
	}

	if len(inTx) > 0 {
		tx, err := pool.Begin(ctx)
		if err != nil {
			return "", fmt.Errorf("begin transaction: %w", err)
		}
		defer func() {
			if err != nil {
				_ = tx.Rollback(ctx)
			}
		}()

		for _, m := range inTx {
			sql, err := migrationSQL(m)
			if err != nil {
				return sb.String(), err
			}
			sb.WriteString(sql)
			sb.WriteString("\n")
			if _, err := tx.Exec(ctx, sql); err != nil {
				return sb.String(), fmt.Errorf("execute %s: %w", sql, err)
			}
		}

		if err := tx.Commit(ctx); err != nil {
			return sb.String(), fmt.Errorf("commit: %w", err)
		}
	}

	for _, m := range outTx {
		sql, err := migrationSQL(m)
		if err != nil {
			return sb.String(), err
		}
		sb.WriteString(sql)
		sb.WriteString("\n")
		if _, err := pool.Exec(ctx, sql); err != nil {
			return sb.String(), fmt.Errorf("execute %s: %w", sql, err)
		}
	}
	return sb.String(), nil
}

func migrationSQL(m diff.Migration) (string, error) {
	switch m.Kind {
	case "create_table":
		return createTableSQL(m.TableDef), nil
	case "add_column":
		return addColumnSQL(m.Schema, m.Table, m.Column), nil
	case "drop_column":
		return dropColumnSQL(m.Schema, m.Table, m.Column.Name), nil
	case "drop_table":
		return dropTableSQL(m.Schema, m.Table), nil
	case "create_index":
		return createIndexSQL(m.Index), nil
	case "add_constraint":
		return addConstraintSQL(m.Schema, m.Table, m), nil
	default:
		return "", fmt.Errorf("unknown migration kind: %s", m.Kind)
	}
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
		if len(u.Columns) > 0 {
			parts = append(parts, fmt.Sprintf("UNIQUE (%s)", strings.Join(u.Columns, ", ")))
		}
	}
	for _, fk := range t.ForeignKeys {
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
	return fmt.Sprintf("CREATE %sINDEX CONCURRENTLY %s ON %s.%s (%s)%s", qual, idx.Name, idx.TableSchema, idx.TableName, strings.Join(idx.Columns, ", "), using)
}

func addConstraintSQL(schemaName, tableName string, m diff.Migration) string {
	if m.PrimaryKey != nil {
		return fmt.Sprintf("ALTER TABLE %s.%s ADD PRIMARY KEY (%s)", schemaName, tableName, strings.Join(m.PrimaryKey.Columns, ", "))
	}
	if m.UniqueKey != nil {
		name := m.UniqueKey.Name
		if name == "" {
			name = "unique_" + tableName + "_" + strings.Join(m.UniqueKey.Columns, "_")
		}
		return fmt.Sprintf("ALTER TABLE %s.%s ADD CONSTRAINT %s UNIQUE (%s)", schemaName, tableName, name, strings.Join(m.UniqueKey.Columns, ", "))
	}
	if m.ForeignKey != nil {
		fk := m.ForeignKey
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
	return ""
}

func addColumnSQL(schema, table string, c *schema.Column) string {
	return fmt.Sprintf("ALTER TABLE %s.%s ADD COLUMN %s", schema, table, columnDef(c))
}

func dropColumnSQL(schema, table, columnName string) string {
	return fmt.Sprintf("ALTER TABLE %s.%s DROP COLUMN %s", schema, table, columnName)
}

func dropTableSQL(schema, table string) string {
	return fmt.Sprintf("DROP TABLE %s.%s", schema, table)
}

func columnDef(c *schema.Column) string {
	return c.Name + " " + c.Type
}
