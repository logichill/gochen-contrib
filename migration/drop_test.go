package migration

import (
	"context"
	"database/sql"
	stderrors "errors"
	"path/filepath"
	"strings"
	"testing"

	gormdb "gochen-contrib/data/db/gorm"
	"gochen-runtime/db/sql/stdsql"
	"gochen/db"
	"gochen/db/dialect"

	gormsqlite "github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestWithDropConnectionPinsSQLiteOperations(t *testing.T) {
	ctx := context.Background()
	database, err := stdsql.NewWithContext(ctx, db.DBConfig{
		Driver:       "sqlite",
		Database:     filepath.Join(t.TempDir(), "drop.db"),
		MaxOpenConns: 2,
	})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer func() { _ = database.Close() }()
	raw, ok := stdsql.DBOf(database)
	if !ok {
		t.Fatal("expected stdsql database provider")
	}
	held, err := raw.Conn(ctx)
	if err != nil {
		t.Fatalf("hold first connection: %v", err)
	}
	defer func() { _ = held.Close() }()
	if _, err := held.ExecContext(ctx, "PRAGMA foreign_keys = ON"); err != nil {
		t.Fatalf("enable foreign keys on held connection: %v", err)
	}

	d := dialect.New("sqlite")
	err = withDropConnection(ctx, database, d, func(conn dropDatabase) error {
		if _, ok := conn.(*sqlConnDropDatabase); !ok {
			t.Fatalf("drop database type = %T, want fixed sql connection", conn)
		}
		if _, err := conn.Exec(ctx, "PRAGMA foreign_keys = OFF"); err != nil {
			return err
		}
		rows, err := conn.Query(ctx, "PRAGMA foreign_keys")
		if err != nil {
			return err
		}
		defer func() { _ = rows.Close() }()
		if !rows.Next() {
			return rows.Err()
		}
		var enabled int
		if err := rows.Scan(&enabled); err != nil {
			return err
		}
		if enabled != 0 {
			t.Fatalf("drop connection foreign_keys = %d, want 0", enabled)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("withDropConnection: %v", err)
	}
	var heldEnabled int
	if err := held.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&heldEnabled); err != nil {
		t.Fatalf("query held connection foreign_keys: %v", err)
	}
	if heldEnabled != 1 {
		t.Fatalf("held connection foreign_keys = %d, want 1", heldEnabled)
	}
}

func TestDropTablesRestoresForeignKeysAfterCancellationAndReturnsRestoreError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	dropErr := stderrors.New("drop failed")
	restoreErr := stderrors.New("restore failed")
	database := &restoreRecordingDropDatabase{
		cancel:     cancel,
		dropErr:    dropErr,
		restoreErr: restoreErr,
	}

	err := dropTables(ctx, database, dialect.New("sqlite"), "schema_migrations")
	if !stderrors.Is(err, dropErr) {
		t.Fatalf("dropTables error = %v, want drop error", err)
	}
	if !stderrors.Is(err, restoreErr) {
		t.Fatalf("dropTables error = %v, want restore error", err)
	}
	if database.restoreContextErr != nil {
		t.Fatalf("restore context error = %v, want active cleanup context", database.restoreContextErr)
	}
}

func TestDropSQLDBSupportsGORMAdapter(t *testing.T) {
	gdb, err := gorm.Open(gormsqlite.Open(filepath.Join(t.TempDir(), "gorm.db")), &gorm.Config{})
	if err != nil {
		t.Fatalf("open gorm sqlite: %v", err)
	}
	database, err := gormdb.New(gdb)
	if err != nil {
		t.Fatalf("new gorm database adapter: %v", err)
	}
	defer func() { _ = database.Close() }()

	got, err := dropSQLDB(database)
	if err != nil {
		t.Fatalf("dropSQLDB: %v", err)
	}
	want, err := gdb.DB()
	if err != nil {
		t.Fatalf("gorm DB: %v", err)
	}
	if got != want {
		t.Fatal("dropSQLDB returned a different connection pool")
	}
}

type restoreRecordingDropDatabase struct {
	cancel            context.CancelFunc
	dropErr           error
	restoreErr        error
	restoreContextErr error
}

func (d *restoreRecordingDropDatabase) Query(context.Context, string, ...any) (db.IRows, error) {
	return &tableNameRows{tables: []string{"children"}}, nil
}

func (d *restoreRecordingDropDatabase) Exec(ctx context.Context, query string, _ ...any) (sql.Result, error) {
	switch {
	case strings.HasPrefix(query, "DROP TABLE"):
		d.cancel()
		return nil, d.dropErr
	case query == "PRAGMA foreign_keys = ON":
		d.restoreContextErr = ctx.Err()
		return nil, d.restoreErr
	default:
		return nil, nil
	}
}

type tableNameRows struct {
	tables []string
	index  int
}

func (r *tableNameRows) Next() bool { return r.index < len(r.tables) }

func (r *tableNameRows) Scan(dest ...any) error {
	value, ok := dest[0].(*string)
	if !ok {
		return stderrors.New("expected string destination")
	}
	*value = r.tables[r.index]
	r.index++
	return nil
}

func (*tableNameRows) Close() error                            { return nil }
func (*tableNameRows) Err() error                              { return nil }
func (*tableNameRows) Columns() ([]string, error)              { return []string{"name"}, nil }
func (*tableNameRows) ColumnTypes() ([]*sql.ColumnType, error) { return nil, nil }
