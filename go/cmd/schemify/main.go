package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/earlye/schemify/schemify"
	"github.com/go-errors/errors"
	"github.com/spf13/cobra"
)

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

var (
	host      string
	port      string
	user      string
	password  string
	database  string
	schemaDir string
	dryRun    bool
	verbose   bool
)

var rootCmd = &cobra.Command{
	Use:   "schemify",
	Short: "Apply declarative SQL schema to PostgreSQL (additive only; fails on destructive changes)",
	RunE:  run,
	// Don't show usage or Cobra's "Error:" on RunE failure; main prints the error once.
	SilenceUsage:  true,
	SilenceErrors: true,
}

func init() {
	rootCmd.PersistentFlags().StringVarP(&host, "host", "H", envOrDefault("DB_HOST", "localhost"), "database host")
	rootCmd.PersistentFlags().StringVarP(&port, "port", "p", envOrDefault("DB_PORT", "5432"), "database port")
	rootCmd.PersistentFlags().StringVarP(&user, "user", "U", envOrDefault("DB_USER", "schemify"), "database user")
	rootCmd.PersistentFlags().StringVarP(&password, "password", "P", envOrDefault("DB_PASSWORD", "schemify"), "database password")
	rootCmd.PersistentFlags().StringVarP(&database, "database", "d", envOrDefault("DB_NAME", "schemify"), "database name")
	rootCmd.PersistentFlags().StringVarP(&schemaDir, "schema", "s", envOrDefault("SCHEMA_DIR", "./schemas/demo-v01"), "directory containing *.sql schema files")
	rootCmd.PersistentFlags().BoolVarP(&dryRun, "dry-run", "n", false, "print SQL only, do not apply")
	rootCmd.PersistentFlags().BoolVarP(&verbose, "verbose", "v", false, "verbose output")
}

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func run(cmd *cobra.Command, args []string) error {
	cfg := &schemify.Options{
		Host:      host,
		Port:      port,
		User:      user,
		Password:  password,
		Database:  database,
		SchemaDir: schemaDir,
	}
	opts := schemify.ApplyOptions{DryRun: dryRun, Verbose: verbose}

	sql, err := schemify.Run(context.Background(), cfg, opts)
	if err != nil {
		if gerr := DynamicCast[errors.Error](err); gerr != nil {
			slog.Error("Schemify Error", "error", gerr)
			fmt.Fprintln(os.Stderr, gerr.ErrorStack())
		}
		return err
	}
	if sql != "" {
		fmt.Print(sql)
	}
	return nil
}
