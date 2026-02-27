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

// Apply runs the additive migrations in a single transaction (unless DryRun).
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

	if len(migrations) == 0 {
		return "", nil
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return "", fmt.Errorf("begin transaction: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback(ctx)
		}
	}()

	for _, m := range migrations {
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
	default:
		return "", fmt.Errorf("unknown migration kind: %s", m.Kind)
	}
}

func createTableSQL(t *schema.Table) string {
	var parts []string
	for _, c := range t.Columns {
		parts = append(parts, columnDef(&c))
	}
	cols := strings.Join(parts, ", ")
	return fmt.Sprintf("CREATE TABLE %s.%s (%s)", t.Schema, t.Name, cols)
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
