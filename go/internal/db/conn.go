package db

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Config holds connection parameters.
type Config struct {
	Host     string
	Port     string
	User     string
	Password string
	Database string
}

// DSN returns a PostgreSQL connection string.
func (c *Config) DSN() string {
	return fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=disable",
		c.User, c.Password, c.Host, c.Port, c.Database)
}

// Connect creates a connection pool.
func Connect(ctx context.Context, cfg *Config) (*pgxpool.Pool, error) {
	pool, err := pgxpool.New(ctx, cfg.DSN())
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping: %w", err)
	}
	return pool, nil
}
