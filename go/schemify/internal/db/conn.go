package db

import (
	"context"
	"log/slog"
	"net/url"

	logging "github.com/earlye/eaux/go/log"
	ss "github.com/earlye/sensitive-strings/golang/ss"
	"github.com/go-errors/errors"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/tracelog"
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

type pgxSlogTracer struct{}

func (t *pgxSlogTracer) Log(ctx context.Context, level tracelog.LogLevel, msg string, data map[string]any) {
	attrs := make([]any, 0, len(data)*2)
	for k, v := range data {
		attrs = append(attrs, k, v)
	}
	slogLevel := pgxLevelToSlog(level)
	slog.Default().Log(ctx, slogLevel, "pgx: "+msg, attrs...)
}

func pgxLevelToSlog(level tracelog.LogLevel) slog.Level {
	switch level {
	case tracelog.LogLevelTrace:
		return logging.LevelTrace
	case tracelog.LogLevelDebug:
		return slog.LevelDebug
	case tracelog.LogLevelInfo:
		return slog.LevelInfo
	case tracelog.LogLevelWarn:
		return slog.LevelWarn
	default:
		return slog.LevelError
	}
}

func (c *Config) DSN() (result ss.SensitiveString, err error) {
	if c.Host == "" || c.User.PlainText() == "" || c.Database == "" || c.Password.PlainText() == "" {
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
		User:     url.UserPassword(c.User.PlainText(), c.Password.PlainText()),
		Host:     c.Host + ":" + c.Port,
		Path:     "/" + c.Database,
		RawQuery: q.Encode(),
	}
	result = ss.New(u.String())
	return
}

// Connect creates a connection pool with pgx trace logging wired into slog.
func Connect(ctx context.Context, cfg *Config) (*pgxpool.Pool, error) {
	dsn, err := cfg.DSN()
	if err != nil {
		return nil, err
	}
	config, err := pgxpool.ParseConfig(dsn.PlainText())
	if err != nil {
		return nil, errors.WrapPrefix(err, "pgxpool.ParseConfig failed", 0)
	}
	config.ConnConfig.Tracer = &tracelog.TraceLog{
		Logger:   &pgxSlogTracer{},
		LogLevel: tracelog.LogLevelTrace,
	}
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, errors.WrapPrefix(err, "pgxpool.NewWithConfig failed", 0)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, errors.WrapPrefix(err, "pool.Ping failed", 0)
	}
	return pool, nil
}
