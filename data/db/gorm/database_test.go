package gormdb

import (
	"context"
	"database/sql"
	stderrors "errors"
	"testing"
	"time"

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

type transactionControlConnPool struct {
	commitErr     error
	rollbackErrs  []error
	commitCalls   int
	rollbackCalls int
}

func (p *transactionControlConnPool) PrepareContext(context.Context, string) (*sql.Stmt, error) {
	return nil, stderrors.New("PrepareContext is not implemented")
}

func (p *transactionControlConnPool) ExecContext(context.Context, string, ...interface{}) (sql.Result, error) {
	return nil, stderrors.New("ExecContext is not implemented")
}

func (p *transactionControlConnPool) QueryContext(context.Context, string, ...interface{}) (*sql.Rows, error) {
	return nil, stderrors.New("QueryContext is not implemented")
}

func (p *transactionControlConnPool) QueryRowContext(context.Context, string, ...interface{}) *sql.Row {
	return nil
}

func (p *transactionControlConnPool) Commit() error {
	p.commitCalls++
	return p.commitErr
}

func (p *transactionControlConnPool) Rollback() error {
	p.rollbackCalls++
	if len(p.rollbackErrs) == 0 {
		return nil
	}
	err := p.rollbackErrs[0]
	p.rollbackErrs = p.rollbackErrs[1:]
	return err
}

func newTransactionControlDB(pool *transactionControlConnPool) *gorm.DB {
	return &gorm.DB{
		Config:    &gorm.Config{},
		Statement: &gorm.Statement{ConnPool: pool},
	}
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
	defer func() {
		if err := rows.Close(); err != nil {
			t.Errorf("Close rows: %v", err)
		}
	}()

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
	if columns, err := rows.Columns(); err != nil || len(columns) != 2 {
		t.Fatalf("Columns = %v, %v; want two columns", columns, err)
	}
	if columnTypes, err := rows.ColumnTypes(); err != nil || len(columnTypes) != 2 {
		t.Fatalf("ColumnTypes = %v, %v; want two columns", columnTypes, err)
	}

	if d.DialectName() != "sqlite" {
		t.Fatalf("expected dialect sqlite, got %q", d.DialectName())
	}
}

func TestOpenAndConnectionOptions(t *testing.T) {
	if _, err := Open(nil, gormsqlite.Open("file:open_invalid?mode=memory&cache=shared"), core.DBConfig{}); errors.Code(err) != errors.InvalidInput { //nolint:staticcheck // intentionally verifies the nil-context contract.
		t.Fatalf("Open(nil context) error = %v, want InvalidInput", err)
	}
	if _, err := Open(context.Background(), nil, core.DBConfig{}); errors.Code(err) != errors.InvalidInput {
		t.Fatalf("Open(nil dialector) error = %v, want InvalidInput", err)
	}

	database, err := Open(context.Background(), gormsqlite.Open("file:open_valid?mode=memory&cache=shared"), core.DBConfig{},
		WithLoggerConfig(LoggerConfig{Level: "silent", IgnoreRecordNotFound: true}),
		WithMaxBindParameters(777),
	)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if database.GormDB() == nil || database.DialectName() != "sqlite" {
		t.Fatalf("opened database did not retain GORM handle/dialect: %v/%q", database.GormDB(), database.DialectName())
	}
	if err := database.Ping(context.Background()); err != nil {
		t.Fatalf("Ping: %v", err)
	}
	if err := database.Close(); err != nil {
		t.Fatalf("Close: %v", err)
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
	if tx.(interface{ Ping(context.Context) error }).Ping(context.Background()) != nil {
		t.Fatal("transaction Ping should succeed")
	}
	if tx.(interface{ GormDB() *gorm.DB }).GormDB() == nil {
		t.Fatal("transaction GormDB should expose underlying handle")
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

func TestNormalizeSavepointNameUsesValidatedValue(t *testing.T) {
	name, err := normalizeSavepointName("  savepoint_one  ")
	if err != nil {
		t.Fatalf("normalizeSavepointName: %v", err)
	}
	if name != "savepoint_one" {
		t.Fatalf("normalized savepoint name = %q, want %q", name, "savepoint_one")
	}
	if _, err := normalizeSavepointName("bad.name"); errors.Code(err) != errors.InvalidInput {
		t.Fatalf("invalid savepoint name error = %v, want InvalidInput", err)
	}
}

func TestDatabase_SavepointFailureIsReturned(t *testing.T) {
	db, err := gorm.Open(gormsqlite.Open("file::memory:?cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	d, err := New(db)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := d.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	sp := tx.(core.ISavepointTransaction)
	if err := sp.RollbackToSavepoint(context.Background(), "missing"); err == nil {
		t.Fatal("expected rollback to missing savepoint to fail")
	}
}

func TestTransactionCloseOwnership(t *testing.T) {
	db, err := gorm.Open(gormsqlite.Open("file:close_ownership?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	d, err := New(db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(context.Background(), "CREATE TABLE close_rows (id INTEGER PRIMARY KEY)"); err != nil {
		t.Fatal(err)
	}
	tx, err := d.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(context.Background(), "INSERT INTO close_rows(id) VALUES (1)"); err != nil {
		t.Fatal(err)
	}
	if err := tx.Close(); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := d.QueryRow(context.Background(), "SELECT COUNT(*) FROM close_rows").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("close should rollback owned transaction, got %d rows", count)
	}

	raw := db.Begin()
	borrowed := NewTransactionDatabase(raw)
	if _, err := borrowed.Exec(context.Background(), "INSERT INTO close_rows(id) VALUES (2)"); err != nil {
		t.Fatal(err)
	}
	if err := borrowed.Close(); err != nil {
		t.Fatal(err)
	}
	if err := raw.Commit().Error; err != nil {
		t.Fatal(err)
	}
	if err := d.QueryRow(context.Background(), "SELECT COUNT(*) FROM close_rows WHERE id = 2").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("borrowed Close must not rollback external transaction, got %d rows", count)
	}
}

func TestTransactionControlErrorsDoNotPolluteHandle(t *testing.T) {
	commitErr := stderrors.New("commit failed")
	pool := &transactionControlConnPool{commitErr: commitErr}
	raw := newTransactionControlDB(pool)
	tx := &transaction{db: raw, owned: true}

	if err := tx.Commit(); !stderrors.Is(err, commitErr) {
		t.Fatalf("Commit error = %v, want %v", err, commitErr)
	}
	if raw.Error != nil {
		t.Fatalf("raw GORM handle Error = %v, want nil", raw.Error)
	}
	if err := tx.Close(); err != nil {
		t.Fatalf("Close after failed Commit: %v", err)
	}
	if pool.commitCalls != 1 || pool.rollbackCalls != 1 {
		t.Fatalf("control calls = commit %d, rollback %d; want 1, 1", pool.commitCalls, pool.rollbackCalls)
	}
}

func TestTransactionRollbackCanRetryAfterFailure(t *testing.T) {
	rollbackErr := stderrors.New("rollback failed")
	pool := &transactionControlConnPool{rollbackErrs: []error{rollbackErr}}
	raw := newTransactionControlDB(pool)
	tx := &transaction{db: raw, owned: true}

	if err := tx.Rollback(); !stderrors.Is(err, rollbackErr) {
		t.Fatalf("Rollback error = %v, want %v", err, rollbackErr)
	}
	if raw.Error != nil {
		t.Fatalf("raw GORM handle Error = %v, want nil", raw.Error)
	}
	if err := tx.Close(); err != nil {
		t.Fatalf("Close retry after failed Rollback: %v", err)
	}
	if pool.rollbackCalls != 2 {
		t.Fatalf("rollback calls = %d, want 2", pool.rollbackCalls)
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
	defer func() { _ = tx.Rollback() }()

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

func TestDatabase_ContractEdges(t *testing.T) {
	db, err := gorm.Open(gormsqlite.Open("file:contract_edges?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	database, err := New(db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Query(nil, "SELECT 1"); errors.Code(err) != errors.InvalidInput { //nolint:staticcheck // intentionally verifies the nil-context contract.
		t.Fatalf("Query(nil) error = %v, want InvalidInput", err)
	}
	if _, err := database.Exec(nil, "SELECT 1"); errors.Code(err) != errors.InvalidInput { //nolint:staticcheck // intentionally verifies the nil-context contract.
		t.Fatalf("Exec(nil) error = %v, want InvalidInput", err)
	}
	if err := database.QueryRow(nil, "SELECT 1").Scan(new(int)); errors.Code(err) != errors.InvalidInput { //nolint:staticcheck // intentionally verifies the nil-context contract.
		t.Fatalf("QueryRow(nil) error = %v, want InvalidInput", err)
	}
	if err := database.QueryRow(nil, "SELECT 1").Err(); errors.Code(err) != errors.InvalidInput { //nolint:staticcheck // intentionally verifies the nil-context contract.
		t.Fatalf("QueryRow(nil).Err = %v, want InvalidInput", err)
	}
	if _, err := database.Begin(nil); errors.Code(err) != errors.InvalidInput { //nolint:staticcheck // intentionally verifies the nil-context contract.
		t.Fatalf("Begin(nil) error = %v, want InvalidInput", err)
	}
	if _, err := database.BeginTx(nil, nil); errors.Code(err) != errors.InvalidInput { //nolint:staticcheck // intentionally verifies the nil-context contract.
		t.Fatalf("BeginTx(nil) error = %v, want InvalidInput", err)
	}
	if err := database.Ping(nil); errors.Code(err) != errors.InvalidInput { //nolint:staticcheck // intentionally verifies the nil-context contract.
		t.Fatalf("Ping(nil) error = %v, want InvalidInput", err)
	}
	if err := database.QueryRow(context.Background(), "SELECT * FROM missing_table").Err(); err == nil {
		t.Fatal("expected deferred QueryRow error")
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := database.Exec(canceled, "SELECT 1"); err == nil {
		t.Fatal("expected canceled Exec to fail")
	}

	tx, err := database.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Close() }()
	if _, err := tx.Begin(context.Background()); errors.Code(err) != errors.Unsupported {
		t.Fatalf("nested Begin error = %v, want Unsupported", err)
	}
	if _, err := tx.BeginTx(context.Background(), nil); errors.Code(err) != errors.Unsupported {
		t.Fatalf("nested BeginTx error = %v, want Unsupported", err)
	}
	if _, err := tx.Query(nil, "SELECT 1"); errors.Code(err) != errors.InvalidInput { //nolint:staticcheck // intentionally verifies the nil-context contract.
		t.Fatalf("tx Query(nil) error = %v, want InvalidInput", err)
	}
	if err := tx.QueryRow(nil, "SELECT 1").Err(); errors.Code(err) != errors.InvalidInput { //nolint:staticcheck // intentionally verifies the nil-context contract.
		t.Fatalf("tx QueryRow(nil) error = %v, want InvalidInput", err)
	}
	if err := tx.QueryRow(nil, "SELECT 1").Scan(new(int)); errors.Code(err) != errors.InvalidInput { //nolint:staticcheck // intentionally verifies the nil-context contract.
		t.Fatalf("tx QueryRow(nil).Scan = %v, want InvalidInput", err)
	}
	if _, err := tx.Exec(nil, "SELECT 1"); errors.Code(err) != errors.InvalidInput { //nolint:staticcheck // intentionally verifies the nil-context contract.
		t.Fatalf("tx Exec(nil) error = %v, want InvalidInput", err)
	}
	if _, err := tx.Begin(nil); errors.Code(err) != errors.InvalidInput { //nolint:staticcheck // intentionally verifies the nil-context contract.
		t.Fatalf("tx Begin(nil) error = %v, want InvalidInput", err)
	}
	if _, err := tx.BeginTx(nil, nil); errors.Code(err) != errors.InvalidInput { //nolint:staticcheck // intentionally verifies the nil-context contract.
		t.Fatalf("tx BeginTx(nil) error = %v, want InvalidInput", err)
	}
	if err := tx.Ping(nil); errors.Code(err) != errors.InvalidInput { //nolint:staticcheck // intentionally verifies the nil-context contract.
		t.Fatalf("tx Ping(nil) error = %v, want InvalidInput", err)
	}
	sp := tx.(core.ISavepointTransaction)
	if err := sp.CreateSavepoint(context.Background(), "bad.name"); errors.Code(err) != errors.InvalidInput {
		t.Fatalf("invalid savepoint error = %v, want InvalidInput", err)
	}
	if err := sp.CreateSavepoint(nil, "valid_name"); errors.Code(err) != errors.InvalidInput { //nolint:staticcheck // intentionally verifies the nil-context contract.
		t.Fatalf("nil savepoint context error = %v, want InvalidInput", err)
	}
	if err := sp.CreateSavepoint(canceled, "canceled_savepoint"); err == nil {
		t.Fatal("expected canceled savepoint creation to fail")
	}

	if result, err := database.Exec(context.Background(), "CREATE TABLE contract_edges (id INTEGER PRIMARY KEY)"); err != nil {
		t.Fatal(err)
	} else {
		if _, err := result.LastInsertId(); errors.Code(err) != errors.Unsupported {
			t.Fatalf("LastInsertId error = %v, want Unsupported", err)
		}
		if rows, err := result.RowsAffected(); err != nil || rows < 0 {
			t.Fatalf("RowsAffected = %d, %v", rows, err)
		}
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

// ctxCase 描述取消与超时两类已失效上下文及其期望的底层错误。
type ctxCase struct {
	name string
	ctx  context.Context
	want error
}

func newContextCases(t *testing.T) []ctxCase {
	t.Helper()
	canceled, cancel := context.WithCancel(context.Background())
	cancel()

	expired, expire := context.WithTimeout(context.Background(), time.Millisecond)
	t.Cleanup(expire)
	<-expired.Done()

	return []ctxCase{
		{name: "canceled", ctx: canceled, want: context.Canceled},
		{name: "expired", ctx: expired, want: context.DeadlineExceeded},
	}
}

func newContractEdgeDB(t *testing.T, name string) *Database {
	t.Helper()
	db, err := gorm.Open(gormsqlite.Open("file:"+name+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("gorm.Open: %v", err)
	}
	database, err := New(db)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	return database
}

func TestDatabase_ContextCancelAndTimeout(t *testing.T) {
	database := newContractEdgeDB(t, "ctx_cancel_db")
	for _, tc := range newContextCases(t) {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := database.Query(tc.ctx, "SELECT 1"); !errors.Is(err, tc.want) {
				t.Fatalf("Query error = %v, want %v", err, tc.want)
			}
			if _, err := database.Exec(tc.ctx, "SELECT 1"); !errors.Is(err, tc.want) {
				t.Fatalf("Exec error = %v, want %v", err, tc.want)
			}
			row := database.QueryRow(tc.ctx, "SELECT 1")
			if err := row.Scan(new(int)); !errors.Is(err, tc.want) {
				t.Fatalf("QueryRow.Scan error = %v, want %v", err, tc.want)
			}
			if err := row.Err(); !errors.Is(err, tc.want) {
				t.Fatalf("QueryRow.Err error = %v, want %v", err, tc.want)
			}
			if _, err := database.Begin(tc.ctx); !errors.Is(err, tc.want) {
				t.Fatalf("Begin error = %v, want %v", err, tc.want)
			}
			if _, err := database.BeginTx(tc.ctx, nil); !errors.Is(err, tc.want) {
				t.Fatalf("BeginTx error = %v, want %v", err, tc.want)
			}
			if err := database.Ping(tc.ctx); !errors.Is(err, tc.want) {
				t.Fatalf("Ping error = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestTransaction_ContextCancelAndTimeout(t *testing.T) {
	database := newContractEdgeDB(t, "ctx_cancel_tx")
	tx, err := database.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Close() }()

	for _, tc := range newContextCases(t) {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := tx.Query(tc.ctx, "SELECT 1"); !errors.Is(err, tc.want) {
				t.Fatalf("Query error = %v, want %v", err, tc.want)
			}
			if _, err := tx.Exec(tc.ctx, "SELECT 1"); !errors.Is(err, tc.want) {
				t.Fatalf("Exec error = %v, want %v", err, tc.want)
			}
			row := tx.QueryRow(tc.ctx, "SELECT 1")
			if err := row.Scan(new(int)); !errors.Is(err, tc.want) {
				t.Fatalf("QueryRow.Scan error = %v, want %v", err, tc.want)
			}
			if err := row.Err(); !errors.Is(err, tc.want) {
				t.Fatalf("QueryRow.Err error = %v, want %v", err, tc.want)
			}
			// 嵌套事务规则优先于取消语义：非 nil ctx 一律 Unsupported。
			if _, err := tx.Begin(tc.ctx); !errors.Is(err, errors.Unsupported) {
				t.Fatalf("nested Begin error = %v, want Unsupported", err)
			}
			if _, err := tx.BeginTx(tc.ctx, nil); !errors.Is(err, errors.Unsupported) {
				t.Fatalf("nested BeginTx error = %v, want Unsupported", err)
			}
			// 事务 Ping 只做 nil 检查，不扩展为额外 SQL，故已失效 ctx 也不失败。
			if err := tx.Ping(tc.ctx); err != nil {
				t.Fatalf("tx Ping error = %v, want nil", err)
			}
		})
	}
}

func TestBorrowedTransaction_ContextContract(t *testing.T) {
	db, err := gorm.Open(gormsqlite.Open("file:borrowed_ctx?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("gorm.Open: %v", err)
	}
	database, err := New(db)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = database.Close() }()

	ctx := context.Background()
	if _, err := database.Exec(ctx, "CREATE TABLE borrowed_ctx (id INTEGER PRIMARY KEY)"); err != nil {
		t.Fatal(err)
	}

	raw := db.Begin()
	if raw.Error != nil {
		t.Fatal(raw.Error)
	}
	borrowed := NewTransactionDatabase(raw)
	defer func() { _ = borrowed.Close() }()

	if _, err := borrowed.Exec(ctx, "INSERT INTO borrowed_ctx(id) VALUES (1)"); err != nil {
		t.Fatalf("borrowed Exec: %v", err)
	}
	var count int
	if err := borrowed.QueryRow(ctx, "SELECT COUNT(*) FROM borrowed_ctx").Scan(&count); err != nil || count != 1 {
		t.Fatalf("borrowed QueryRow = (%d, %v), want (1, nil)", count, err)
	}
	rows, err := borrowed.Query(ctx, "SELECT id FROM borrowed_ctx")
	if err != nil {
		t.Fatalf("borrowed Query: %v", err)
	}
	_ = rows.Close()
	if err := borrowed.Ping(ctx); err != nil {
		t.Fatalf("borrowed Ping: %v", err)
	}

	if _, err := borrowed.Query(nil, "SELECT 1"); errors.Code(err) != errors.InvalidInput { //nolint:staticcheck // intentionally verifies the nil-context contract.
		t.Fatalf("borrowed Query(nil) error = %v, want InvalidInput", err)
	}
	if err := borrowed.QueryRow(nil, "SELECT 1").Scan(new(int)); errors.Code(err) != errors.InvalidInput { //nolint:staticcheck // intentionally verifies the nil-context contract.
		t.Fatalf("borrowed QueryRow(nil).Scan = %v, want InvalidInput", err)
	}
	if err := borrowed.QueryRow(nil, "SELECT 1").Err(); errors.Code(err) != errors.InvalidInput { //nolint:staticcheck // intentionally verifies the nil-context contract.
		t.Fatalf("borrowed QueryRow(nil).Err = %v, want InvalidInput", err)
	}
	if _, err := borrowed.Exec(nil, "SELECT 1"); errors.Code(err) != errors.InvalidInput { //nolint:staticcheck // intentionally verifies the nil-context contract.
		t.Fatalf("borrowed Exec(nil) error = %v, want InvalidInput", err)
	}
	if _, err := borrowed.Begin(nil); errors.Code(err) != errors.InvalidInput { //nolint:staticcheck // intentionally verifies the nil-context contract.
		t.Fatalf("borrowed Begin(nil) error = %v, want InvalidInput", err)
	}
	if _, err := borrowed.BeginTx(nil, nil); errors.Code(err) != errors.InvalidInput { //nolint:staticcheck // intentionally verifies the nil-context contract.
		t.Fatalf("borrowed BeginTx(nil) error = %v, want InvalidInput", err)
	}
	if err := borrowed.Ping(nil); errors.Code(err) != errors.InvalidInput { //nolint:staticcheck // intentionally verifies the nil-context contract.
		t.Fatalf("borrowed Ping(nil) error = %v, want InvalidInput", err)
	}

	for _, tc := range newContextCases(t) {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := borrowed.Query(tc.ctx, "SELECT 1"); !errors.Is(err, tc.want) {
				t.Fatalf("Query error = %v, want %v", err, tc.want)
			}
			if _, err := borrowed.Exec(tc.ctx, "SELECT 1"); !errors.Is(err, tc.want) {
				t.Fatalf("Exec error = %v, want %v", err, tc.want)
			}
			row := borrowed.QueryRow(tc.ctx, "SELECT 1")
			if err := row.Scan(new(int)); !errors.Is(err, tc.want) {
				t.Fatalf("QueryRow.Scan error = %v, want %v", err, tc.want)
			}
			if err := row.Err(); !errors.Is(err, tc.want) {
				t.Fatalf("QueryRow.Err error = %v, want %v", err, tc.want)
			}
			if _, err := borrowed.Begin(tc.ctx); !errors.Is(err, errors.Unsupported) {
				t.Fatalf("nested Begin error = %v, want Unsupported", err)
			}
			if _, err := borrowed.BeginTx(tc.ctx, nil); !errors.Is(err, errors.Unsupported) {
				t.Fatalf("nested BeginTx error = %v, want Unsupported", err)
			}
			if err := borrowed.Ping(tc.ctx); err != nil {
				t.Fatalf("borrowed Ping error = %v, want nil", err)
			}
		})
	}

	// 借用视图的 Close 必须为 no-op：既不能结束外部事务，也不能阻断其后的写入与提交。
	if err := borrowed.Close(); err != nil {
		t.Fatalf("borrowed Close: %v", err)
	}
	if err := borrowed.Close(); err != nil {
		t.Fatalf("borrowed Close must be idempotent: %v", err)
	}
	if _, err := borrowed.Exec(ctx, "INSERT INTO borrowed_ctx(id) VALUES (2)"); err != nil {
		t.Fatalf("write after borrowed Close: %v", err)
	}
	if err := raw.Commit().Error; err != nil {
		t.Fatalf("external commit after borrowed Close: %v", err)
	}
	if err := database.QueryRow(ctx, "SELECT COUNT(*) FROM borrowed_ctx").Scan(&count); err != nil || count != 2 {
		t.Fatalf("external commit result = (%d, %v), want (2, nil)", count, err)
	}
}

func TestRow_ScanAndErrContract(t *testing.T) {
	database := newContractEdgeDB(t, "row_contract")
	ctx := context.Background()

	if err := database.QueryRow(ctx, "SELECT 1").Scan(new(int)); err != nil {
		t.Fatalf("normal QueryRow: %v", err)
	}

	badSQL := database.QueryRow(ctx, "SELECT * FROM missing_table")
	if err := badSQL.Scan(new(int)); err == nil {
		t.Fatal("expected bad SQL Scan error")
	}
	if err := badSQL.Err(); err == nil {
		t.Fatal("expected bad SQL Err to expose the deferred error")
	}

	empty := database.QueryRow(ctx, "SELECT 1 WHERE 0")
	if err := empty.Scan(new(int)); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("empty result Scan error = %v, want sql.ErrNoRows", err)
	}
	if err := empty.Err(); err != nil {
		t.Fatalf("empty result Err = %v, want nil", err)
	}

	detached := &dbRow{}
	if err := detached.Scan(new(int)); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("nil raw row Scan error = %v, want sql.ErrNoRows", err)
	}
	if err := detached.Err(); err != nil {
		t.Fatalf("nil raw row Err = %v, want nil", err)
	}
}
