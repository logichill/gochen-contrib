package gormfactory

import (
	"context"
	"strings"
	"testing"

	gormdb "gochen-contrib/data/db/gorm"
	core "gochen/db"
	"gochen/errors"

	"github.com/jackc/pgx/v5"
	"github.com/lib/pq"
)

func TestDatabase_NewFromDSN_SQLite(t *testing.T) {
	db, err := NewFromDSN(context.Background(), "sqlite", "file::memory:?cache=shared")
	if err != nil {
		t.Fatalf("NewFromDSN: %v", err)
	}
	if err := db.Ping(context.Background()); err != nil {
		t.Fatalf("Ping: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestDatabase_NewFromDSN_EmptyDSN(t *testing.T) {
	if _, err := NewFromDSN(context.Background(), "sqlite", ""); err == nil {
		t.Fatalf("expected error")
	} else if errors.Code(err) != errors.InvalidInput {
		t.Fatalf("expected INVALID_INPUT, got %v", err)
	}
}

func TestDatabase_SQLiteEmptyDSNFails(t *testing.T) {
	if _, err := NewFromConfig(context.Background(), core.DBConfig{
		Driver:   "sqlite",
		Database: "",
	}); err == nil {
		t.Fatalf("expected error for empty sqlite DSN")
	} else if errors.Code(err) != errors.InvalidInput {
		t.Fatalf("expected INVALID_INPUT, got %v", err)
	}
}

func TestDatabase_InvalidParameterBudget(t *testing.T) {
	if _, err := NewFromConfig(context.Background(), core.DBConfig{
		Driver:            "sqlite",
		Database:          "file::memory:?cache=shared",
		MaxBindParameters: 10,
	}); err == nil {
		t.Fatalf("expected error for NewFromConfig with maxBindParameters = 10")
	} else if errors.Code(err) != errors.InvalidInput {
		t.Fatalf("expected INVALID_INPUT, got %v", err)
	}
}

func TestDatabase_ConfigPreservesPoolAndOptions(t *testing.T) {
	db, err := NewFromConfig(context.Background(), core.DBConfig{
		Driver: "sqlite", Database: ":memory:", MaxOpenConns: 1, MaxBindParameters: 1000,
	}, gormdb.WithMaxBindParameters(777), gormdb.WithLoggerConfig(gormdb.LoggerConfig{Level: "silent"}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	budget, ok := db.(core.IBindParameterLimitProvider)
	if !ok || budget.MaxBindParameters() != 777 {
		t.Fatalf("parameter budget was lost: %v", budget)
	}
	gdb, ok := gormdb.DBOf(db)
	if !ok {
		t.Fatal("database is not backed by GORM")
	}
	sqlDB, err := gdb.DB()
	if err != nil {
		t.Fatal(err)
	}
	if sqlDB.Stats().MaxOpenConnections != 1 {
		t.Fatalf("pool limit = %d, want 1", sqlDB.Stats().MaxOpenConnections)
	}
}

func TestPostgresDSNQuotesCredentialKeywordValues(t *testing.T) {
	tests := []struct {
		name     string
		user     string
		password string
		database string
	}{
		{name: "plain", user: "appuser", password: "secret", database: "appdb"},
		{name: "password with space", user: "appuser", password: "p ass", database: "appdb"},
		{name: "database with space", user: "appuser", password: "secret", database: "app db"},
		{name: "single quote", user: "app'user", password: "p'ass", database: "app'db"},
		{name: "backslash", user: `app\user`, password: `p\ass`, database: `app\db`},
		{name: "mixed", user: `app \u'ser`, password: `p '\a ss`, database: `a\p 'db`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dsn := postgresDSN(core.DBConfig{
				Host:     "127.0.0.1",
				Port:     5432,
				Username: tt.user,
				Password: tt.password,
				Database: tt.database,
				Options:  map[string]any{"sslmode": "disable"},
			})

			pqCfg, err := pq.NewConfig(dsn)
			if err != nil {
				t.Fatalf("lib/pq rejected DSN %q: %v", dsn, err)
			}
			if pqCfg.Host != "127.0.0.1" || pqCfg.Port != 5432 || pqCfg.User != tt.user ||
				pqCfg.Password != tt.password || pqCfg.Database != tt.database {
				t.Fatalf("lib/pq parsed DSN %q as host=%q port=%d user=%q password=%q database=%q",
					dsn, pqCfg.Host, pqCfg.Port, pqCfg.User, pqCfg.Password, pqCfg.Database)
			}

			pgxCfg, err := pgx.ParseConfig(dsn)
			if err != nil {
				t.Fatalf("pgx rejected DSN %q: %v", dsn, err)
			}
			if pgxCfg.Host != "127.0.0.1" || pgxCfg.Port != 5432 || pgxCfg.User != tt.user ||
				pgxCfg.Password != tt.password || pgxCfg.Database != tt.database {
				t.Fatalf("pgx parsed DSN %q as host=%q port=%d user=%q password=%q database=%q",
					dsn, pgxCfg.Host, pgxCfg.Port, pgxCfg.User, pgxCfg.Password, pgxCfg.Database)
			}
		})
	}
}

func TestPostgresDSNQuotesSSLOptionPaths(t *testing.T) {
	const (
		rootCert = "/etc/gochen certs/root ca.pem"
		cert     = "/etc/gochen certs/client cert.pem"
		key      = `/etc/gochen certs/client\key.pem`
	)
	dsn := postgresDSN(core.DBConfig{
		Host:     "db.internal",
		Port:     5432,
		Username: "appuser",
		Password: "secret",
		Database: "appdb",
		Options: map[string]any{
			"sslmode":     "verify-full",
			"sslrootcert": rootCert,
			"sslcert":     cert,
			"sslkey":      key,
		},
	})

	pqCfg, err := pq.NewConfig(dsn)
	if err != nil {
		t.Fatalf("lib/pq rejected DSN %q: %v", dsn, err)
	}
	if string(pqCfg.SSLMode) != "verify-full" || pqCfg.SSLRootCert != rootCert || pqCfg.SSLCert != cert || pqCfg.SSLKey != key {
		t.Fatalf("lib/pq parsed DSN %q as sslmode=%q sslrootcert=%q sslcert=%q sslkey=%q",
			dsn, pqCfg.SSLMode, pqCfg.SSLRootCert, pqCfg.SSLCert, pqCfg.SSLKey)
	}

	quotedKey := "sslkey='" + strings.ReplaceAll(key, `\`, `\\`) + "'"
	if !strings.Contains(dsn, quotedKey) {
		t.Fatalf("DSN %q does not carry quoted sslkey %q", dsn, quotedKey)
	}
}
