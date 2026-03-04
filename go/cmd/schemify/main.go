package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/earlye/eaux/go/types"
	"github.com/earlye/schemify/go/schemify"
	"github.com/go-errors/errors"
	"github.com/spf13/cobra"
)

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

var options = schemify.Options{}

var rootCmd = &cobra.Command{
	Use:   "schemify",
	Short: "Apply declarative SQL schema to PostgreSQL (additive only; fails on destructive changes)",
	RunE:  run,
	// Don't show usage or Cobra's "Error:" on RunE failure; main prints the error once.
	SilenceUsage:  true,
	SilenceErrors: true,
}

func init() {
	rootCmd.PersistentFlags().StringVarP(&options.Database.Host, "host", "H", envOrDefault("DB_HOST", "localhost"), "database host")
	rootCmd.PersistentFlags().StringVarP(&options.Database.Port, "port", "p", envOrDefault("DB_PORT", "5432"), "database port")
	rootCmd.PersistentFlags().StringVarP(options.Database.User.PValue(), "user", "U", envOrDefault("DB_USER", "schemify"), "database user")
	rootCmd.PersistentFlags().StringVarP(options.Database.Password.PValue(), "password", "P", envOrDefault("DB_PASSWORD", "schemify"), "database password")
	rootCmd.PersistentFlags().StringVarP(&options.Database.Database, "database", "d", envOrDefault("DB_NAME", "schemify"), "database name")
	rootCmd.PersistentFlags().StringVarP(&options.Database.SSLMode, "ssl-mode", "S", envOrDefault("DB_SSLMODE", "require"), "database SSL mode")
	rootCmd.PersistentFlags().StringVarP(&options.Database.SSLRootCert, "ssl-root-cert", "R", envOrDefault("DB_SSLROOTCERT", ""), "database SSL root certificate")
	rootCmd.PersistentFlags().StringVarP(&options.Database.SSLCert, "ssl-cert", "C", envOrDefault("DB_SSLCERT", ""), "database SSL certificate")
	rootCmd.PersistentFlags().StringVarP(&options.Database.SSLKey, "ssl-key", "K", envOrDefault("DB_SSLKEY", ""), "database SSL key")
	rootCmd.PersistentFlags().StringVarP(&options.SchemaDir, "schema", "s", envOrDefault("SCHEMA_DIR", "./schemas/demo-v01"), "directory containing *.sql schema files")
	rootCmd.PersistentFlags().BoolVarP(&options.ApplyOptions.DryRun, "dry-run", "n", false, "print SQL only, do not apply")
	rootCmd.PersistentFlags().BoolVarP(&options.ApplyOptions.Verbose, "verbose", "v", false, "verbose output")
}

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func run(cmd *cobra.Command, args []string) error {

	sql, err := schemify.Run(context.Background(), &options)
	if err != nil {
		if gerr := types.DynamicCast[errors.Error](err); gerr != nil {
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
