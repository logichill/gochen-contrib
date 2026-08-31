package migration

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"gochen/db"
	"gochen/db/dialect"
	"gochen/errors"
	"gorm.io/gorm"
)

const foreignKeyRestoreTimeout = 5 * time.Second

type dropDatabase interface {
	Query(ctx context.Context, query string, args ...any) (db.IRows, error)
	Exec(ctx context.Context, query string, args ...any) (sql.Result, error)
}

type sqlConnDropDatabase struct {
	conn    *sql.Conn
	dialect dialect.IDialect
}

func (d *sqlConnDropDatabase) Query(ctx context.Context, query string, args ...any) (db.IRows, error) {
	return d.conn.QueryContext(ctx, d.dialect.Rebind(query), args...)
}

func (d *sqlConnDropDatabase) Exec(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return d.conn.ExecContext(ctx, d.dialect.Rebind(query), args...)
}

type sqlDBProvider interface {
	SQLDB() *sql.DB
}

type gormDBProvider interface {
	GormDB() *gorm.DB
}

func withDropConnection(ctx context.Context, database db.IDatabase, d dialect.IDialect, fn func(dropDatabase) error) (err error) {
	if d.Name() != dialect.NameSQLite && d.Name() != dialect.NameMySQL {
		return fn(database)
	}

	raw, rawErr := dropSQLDB(database)
	if rawErr != nil {
		return rawErr
	}
	if raw == nil {
		return errors.NewCode(errors.Unsupported, "migration drop requires fixed database connection").
			WithContext("dialect", d.Name())
	}
	conn, err := raw.Conn(ctx)
	if err != nil {
		return errors.Wrap(err, errors.Database, "acquire migration drop connection failed")
	}
	defer func() {
		if closeErr := conn.Close(); closeErr != nil {
			err = errors.Join(err, errors.Wrap(closeErr, errors.Database, "release migration drop connection failed"))
		}
	}()
	return fn(&sqlConnDropDatabase{conn: conn, dialect: d})
}

func dropSQLDB(database db.IDatabase) (*sql.DB, error) {
	if provider, ok := database.(sqlDBProvider); ok && provider != nil {
		return provider.SQLDB(), nil
	}
	provider, ok := database.(gormDBProvider)
	if !ok || provider == nil || provider.GormDB() == nil {
		return nil, nil
	}
	gormDB := provider.GormDB()
	connPool := gormDB.ConnPool
	if gormDB.Statement != nil && gormDB.Statement.ConnPool != nil {
		connPool = gormDB.Statement.ConnPool
	}
	if _, transactionBound := connPool.(*sql.Tx); transactionBound {
		return nil, errors.NewCode(errors.Unsupported, "migration drop cannot pin a transaction-bound gorm database")
	}
	raw, err := gormDB.DB()
	if err != nil {
		return nil, errors.Wrap(err, errors.Database, "get gorm sql database failed")
	}
	return raw, nil
}

func foreignKeyRestoreContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithTimeout(context.WithoutCancel(ctx), foreignKeyRestoreTimeout)
}

func listTables(ctx context.Context, database dropDatabase, d dialect.IDialect, excluded map[string]struct{}) ([]string, error) {
	var query string
	switch d.Name() {
	case dialect.NameSQLite:
		query = "SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%' ORDER BY name"
	case dialect.NameMySQL:
		query = "SELECT table_name FROM information_schema.tables WHERE table_schema = DATABASE() AND table_type = 'BASE TABLE' ORDER BY table_name"
	case dialect.NamePostgres:
		query = "SELECT table_name FROM information_schema.tables WHERE table_schema = current_schema() AND table_type = 'BASE TABLE' ORDER BY table_name"
	default:
		return nil, errors.NewCode(errors.Unsupported, "unsupported migration drop dialect")
	}
	rows, err := database.Query(ctx, query)
	if err != nil {
		return nil, errors.Wrap(err, errors.Database, "list database tables failed")
	}
	defer rows.Close()
	var tables []string
	for rows.Next() {
		var table string
		if err := rows.Scan(&table); err != nil {
			return nil, errors.Wrap(err, errors.Database, "scan database table failed")
		}
		if isExcludedTable(table, excluded) {
			continue
		}
		tables = append(tables, table)
	}
	if err := rows.Err(); err != nil {
		return nil, errors.Wrap(err, errors.Database, "iterate database tables failed")
	}
	return tables, nil
}

func isExcludedTable(table string, excluded map[string]struct{}) bool {
	if len(excluded) == 0 {
		return false
	}
	_, ok := excluded[strings.TrimSpace(table)]
	return ok
}

func disableForeignKeys(ctx context.Context, database dropDatabase, d dialect.IDialect) error {
	switch d.Name() {
	case dialect.NameSQLite:
		_, err := database.Exec(ctx, "PRAGMA foreign_keys = OFF")
		return err
	case dialect.NameMySQL:
		_, err := database.Exec(ctx, "SET FOREIGN_KEY_CHECKS = 0")
		return err
	default:
		return nil
	}
}

func enableForeignKeys(ctx context.Context, database dropDatabase, d dialect.IDialect) error {
	switch d.Name() {
	case dialect.NameSQLite:
		_, err := database.Exec(ctx, "PRAGMA foreign_keys = ON")
		return err
	case dialect.NameMySQL:
		_, err := database.Exec(ctx, "SET FOREIGN_KEY_CHECKS = 1")
		return err
	default:
		return nil
	}
}
