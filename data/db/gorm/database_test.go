package gormdb

import (
	"context"
	"testing"

	"gochen-runtime/eventing/store/sqlstore"
	core "gochen/db"
	"gochen/db/dialect"
	"gochen/errors"
	"gochen/eventing"

	gormsqlite "github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

type testRow struct {
	ID   int64  `gorm:"primaryKey"`
	Name string `gorm:"column:name"`
}

func TestDatabase_QueryAndExec(t *testing.T) {
	db, err := gorm.Open(
		gormsqlite.Open("file::memory:?cache=shared"),
		&gorm.Config{},
	)
	if err != nil {
		t.Fatalf("gorm.Open: %v", err)
	}
	if err := db.AutoMigrate(&testRow{}); err != nil {
		t.Fatalf("AutoMigrate: %v", err)
	}

	d, err := New(db)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if _, err := d.Exec(context.Background(), "INSERT INTO test_rows(name) VALUES (?)", "alice"); err != nil {
		t.Fatalf("Exec: %v", err)
	}

	rows, err := d.Query(context.Background(), "SELECT id, name FROM test_rows WHERE name = ?", "alice")
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	defer rows.Close()

	if !rows.Next() {
		t.Fatalf("expected row")
	}
	var id int64
	var name string
	if err := rows.Scan(&id, &name); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if name != "alice" {
		t.Fatalf("expected alice, got %q", name)
	}

	if d.DialectName() != "sqlite" {
		t.Fatalf("expected dialect sqlite, got %q", d.DialectName())
	}
}

func TestTransaction_PreservesDialect(t *testing.T) {
	db, err := gorm.Open(
		gormsqlite.Open("file::memory:?cache=shared"),
		&gorm.Config{},
	)
	if err != nil {
		t.Fatalf("gorm.Open: %v", err)
	}
	database, err := New(db)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	tx, err := database.Begin(context.Background())
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	defer func() { _ = tx.Rollback() }()

	if got := dialect.FromDatabase(tx).Name(); got != dialect.NameSQLite {
		t.Fatalf("transaction dialect = %q, want %q", got, dialect.NameSQLite)
	}
}

func TestTransaction_AppendsEventWithGlobalPosition(t *testing.T) {
	ctx := context.Background()
	db, err := gorm.Open(
		gormsqlite.Open("file:gorm_event_store?mode=memory&cache=shared"),
		&gorm.Config{},
	)
	if err != nil {
		t.Fatalf("gorm.Open: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("db.DB: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })

	database, err := New(db)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	for _, statement := range []string{
		`CREATE TABLE event_store (
			id TEXT PRIMARY KEY,
			type TEXT NOT NULL,
			aggregate_id INTEGER NOT NULL,
			aggregate_type TEXT NOT NULL,
			version INTEGER NOT NULL,
			schema_version INTEGER NOT NULL,
			global_position INTEGER,
			timestamp DATETIME NOT NULL,
			payload TEXT NOT NULL,
			metadata TEXT NOT NULL,
			UNIQUE (aggregate_type, aggregate_id, version)
		)`,
		`CREATE UNIQUE INDEX idx_event_store_global_position ON event_store(global_position)`,
		`CREATE TABLE event_store_positions (
			store_name TEXT PRIMARY KEY,
			next_position INTEGER NOT NULL CHECK (next_position > 0)
		)`,
	} {
		if _, err := database.Exec(ctx, statement); err != nil {
			t.Fatalf("create event store schema: %v", err)
		}
	}
	if _, err := database.Exec(ctx,
		`INSERT INTO event_store_positions (store_name, next_position) VALUES (?, ?)`,
		"event_store", 1,
	); err != nil {
		t.Fatalf("seed global position allocator: %v", err)
	}

	store, err := sqlstore.NewSQLEventStore(database, "event_store")
	if err != nil {
		t.Fatalf("NewSQLEventStore: %v", err)
	}
	event := eventing.NewEventWithID[int64](
		"gorm-event-1",
		1,
		"TestAggregate",
		"Created",
		1,
		map[string]any{"name": "alice"},
	)
	if err := store.AppendEvents(ctx, "TestAggregate", 1, []eventing.IStorableEvent[int64]{event}, 0); err != nil {
		t.Fatalf("AppendEvents: %v", err)
	}

	var globalPosition int64
	if err := database.QueryRow(ctx,
		`SELECT global_position FROM event_store WHERE id = ?`,
		event.GetID(),
	).Scan(&globalPosition); err != nil {
		t.Fatalf("query global_position: %v", err)
	}
	if globalPosition != 1 {
		t.Fatalf("global_position = %d, want 1", globalPosition)
	}

	var nextPosition int64
	if err := database.QueryRow(ctx,
		`SELECT next_position FROM event_store_positions WHERE store_name = ?`,
		"event_store",
	).Scan(&nextPosition); err != nil {
		t.Fatalf("query allocator: %v", err)
	}
	if nextPosition != 2 {
		t.Fatalf("next_position = %d, want 2", nextPosition)
	}
}

func TestDatabase_NewNil(t *testing.T) {
	if _, err := New(nil); err == nil {
		t.Fatalf("expected error")
	} else if errors.Code(err) != errors.InvalidInput {
		t.Fatalf("expected INVALID_INPUT, got %v", err)
	}
}

func TestDBOf_TypedNilSafety(t *testing.T) {
	if gdb, ok := DBOf(nil); ok || gdb != nil {
		t.Fatalf("expected (nil, false) for nil DBOf")
	}
	var nilDB *Database
	if gdb, ok := DBOf(nilDB); ok || gdb != nil {
		t.Fatalf("expected (nil, false) for typed-nil DBOf")
	}
}

func TestDatabase_Savepoints(t *testing.T) {
	db, err := gorm.Open(
		gormsqlite.Open("file::memory:?cache=shared"),
		&gorm.Config{},
	)
	if err != nil {
		t.Fatalf("gorm.Open: %v", err)
	}

	d, err := New(db)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	spCap, ok := any(d).(core.ISavepointCapabilityProvider)
	if !ok || !spCap.SupportsSavepoints() {
		t.Fatalf("expected SupportsSavepoints() = true on Database")
	}

	if _, err := d.Exec(context.Background(), "CREATE TABLE sp_rows (id INT PRIMARY KEY, val TEXT)"); err != nil {
		t.Fatalf("create table: %v", err)
	}

	tx, err := d.Begin(context.Background())
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}

	spTx, ok := tx.(core.ISavepointTransaction)
	if !ok {
		t.Fatalf("expected tx to implement ISavepointTransaction")
	}
	if spCapTx, ok := tx.(core.ISavepointCapabilityProvider); !ok || !spCapTx.SupportsSavepoints() {
		t.Fatalf("expected SupportsSavepoints() = true on transaction")
	}

	if _, err := tx.Exec(context.Background(), "INSERT INTO sp_rows(id, val) VALUES (1, 'initial')"); err != nil {
		t.Fatalf("insert initial: %v", err)
	}

	if err := spTx.CreateSavepoint(context.Background(), "sp1"); err != nil {
		t.Fatalf("CreateSavepoint: %v", err)
	}

	if _, err := tx.Exec(context.Background(), "INSERT INTO sp_rows(id, val) VALUES (2, 'secondary')"); err != nil {
		t.Fatalf("insert secondary: %v", err)
	}

	if err := spTx.RollbackToSavepoint(context.Background(), "sp1"); err != nil {
		t.Fatalf("RollbackToSavepoint: %v", err)
	}

	if err := spTx.ReleaseSavepoint(context.Background(), "sp1"); err != nil {
		t.Fatalf("ReleaseSavepoint: %v", err)
	}

	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	row := d.QueryRow(context.Background(), "SELECT COUNT(*) FROM sp_rows WHERE id = 2")
	var count int
	if err := row.Scan(&count); err != nil {
		t.Fatalf("scan count: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected count = 0 after savepoint rollback, got %d", count)
	}
}

func TestDatabase_MaxBindParameters(t *testing.T) {
	db, err := gorm.Open(
		gormsqlite.Open("file::memory:?cache=shared"),
		&gorm.Config{},
	)
	if err != nil {
		t.Fatalf("gorm.Open: %v", err)
	}

	// Default
	dDefault, err := New(db)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if dDefault.MaxBindParameters() != dialect.DefaultMaxBindParameters {
		t.Fatalf("expected %d, got %d", dialect.DefaultMaxBindParameters, dDefault.MaxBindParameters())
	}

	// Custom via option
	dCustom, err := New(db, WithMaxBindParameters(999))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if dCustom.MaxBindParameters() != 999 {
		t.Fatalf("expected 999, got %d", dCustom.MaxBindParameters())
	}

	tx, err := dCustom.Begin(context.Background())
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	defer tx.Rollback()

	if bindProvider, ok := tx.(core.IBindParameterLimitProvider); !ok || bindProvider.MaxBindParameters() != 999 {
		t.Fatalf("expected tx to implement IBindParameterLimitProvider with 999")
	}
}

func TestDatabase_ValidateMaxBindParametersFailFast(t *testing.T) {
	db, err := gorm.Open(
		gormsqlite.Open("file::memory:?cache=shared"),
		&gorm.Config{},
	)
	if err != nil {
		t.Fatalf("gorm.Open: %v", err)
	}

	// Value 10 is < dialect.MinMaxBindParameters (100)
	if _, err := New(db, WithMaxBindParameters(10)); err == nil {
		t.Fatalf("expected error for maxBindParameters = 10")
	} else if errors.Code(err) != errors.InvalidInput {
		t.Fatalf("expected INVALID_INPUT, got %v", err)
	}

}

func TestNewTransactionDatabase_PreservesMaxBindParameters(t *testing.T) {
	db, err := gorm.Open(
		gormsqlite.Open("file::memory:?cache=shared"),
		&gorm.Config{},
	)
	if err != nil {
		t.Fatalf("gorm.Open: %v", err)
	}

	tx := db.Begin()
	defer tx.Rollback()

	txDB := NewTransactionDatabase(tx, WithMaxBindParameters(777))
	if p, ok := txDB.(core.IBindParameterLimitProvider); !ok || p.MaxBindParameters() != 777 {
		t.Fatalf("expected txDB to have maxBindParams = 777, got %v", p.MaxBindParameters())
	}

	// Invalid value (<100) falls back to default
	txDBInvalid := NewTransactionDatabase(tx, WithMaxBindParameters(10))
	if p, ok := txDBInvalid.(core.IBindParameterLimitProvider); !ok || p.MaxBindParameters() != dialect.DefaultMaxBindParameters {
		t.Fatalf("expected txDBInvalid to fall back to default %d, got %v", dialect.DefaultMaxBindParameters, p.MaxBindParameters())
	}
}
