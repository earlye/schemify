package db

import (
	"context"
	"log/slog"
	"net/url"

	ss "github.com/earlye/sensitive-strings/golang/ss"
	"github.com/go-errors/errors"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Config holds connection parameters.
type Config struct {
	Host        string
	Port        string
	User        ss.SensitiveString
	Password    ss.SensitiveString
	Database    string
	SSLMode     string
	SSLRootCert string
	SSLCert     string
	SSLKey      string
}

func (c *Config) DSN() (result ss.SensitiveString, err error) {
	if c.Host == "" || c.User.Value() == "" || c.Database == "" || c.Password.Value() == "" {
		err = errors.Errorf("schema: missing required DB_* env (need DB_HOST, DB_USER, DB_NAME, DB_PASSWORD)")
		return
	}
	if c.Port == "" {
		c.Port = "5432"
	}
	if c.SSLMode == "" {
		c.SSLMode = "require"
	}

	q := url.Values{}
	q.Set("sslmode", c.SSLMode)
	if c.SSLRootCert != "" {
		q.Set("sslrootcert", c.SSLRootCert)
	}
	if c.SSLCert != "" {
		q.Set("sslcert", c.SSLCert)
	}
	if c.SSLKey != "" {
		q.Set("sslkey", c.SSLKey)
	}
	slog.Debug("Config.DSN()",
		"config", c, // This is safe because user/pass are sensitive string objects
	)
	u := &url.URL{
		Scheme:   "postgres",
		User:     url.UserPassword(c.User.Value(), c.Password.Value()),
		Host:     c.Host + ":" + c.Port,
		Path:     "/" + c.Database,
		RawQuery: q.Encode(),
	}
	result = *ss.New(u.String()) // TODO: update sensitive string to have a SetPlaintext method.
	return
}

// Connect creates a connection pool.
func Connect(ctx context.Context, cfg *Config) (*pgxpool.Pool, error) {
	dsn, err := cfg.DSN()
	if err != nil {
		return nil, err
	}
	pool, err := pgxpool.New(ctx, dsn.Value())
	if err != nil {
		return nil, errors.WrapPrefix(err, "pgxpool.New failed", 0)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, errors.WrapPrefix(err, "pool.Ping failed", 0)
	}
	return pool, nil
}
