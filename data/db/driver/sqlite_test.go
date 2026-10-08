package driver_test

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gochen-contrib/data/db/driver"
)

func TestNormalizeSQLiteDSN(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    string
		wantWAL bool
	}{
		{
			name: "empty",
			in:   "",
			want: "",
		},
		{
			name:    "bare path",
			in:      "app.db",
			wantWAL: true,
			want:    "file:app.db?_txlock=immediate&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=synchronous(NORMAL)",
		},
		{
			name:    "memory",
			in:      ":memory:",
			wantWAL: true,
			want:    ":memory:?cache=shared&_txlock=immediate&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=synchronous(NORMAL)",
		},
		{
			name: "file prefix with query",
			in:   "file:app.db?mode=ro",
			want: "file:app.db?mode=ro&_txlock=immediate&_pragma=busy_timeout(5000)&_pragma=synchronous(NORMAL)",
		},
		{
			name: "file prefix with trailing ampersand",
			in:   "file:app.db?mode=ro&",
			want: "file:app.db?mode=ro&_txlock=immediate&_pragma=busy_timeout(5000)&_pragma=synchronous(NORMAL)",
		},
		{
			name: "readonly mode",
			in:   "file:app.db?mode=readonly",
			want: "file:app.db?mode=readonly&_txlock=immediate&_pragma=busy_timeout(5000)&_pragma=synchronous(NORMAL)",
		},
		{
			name: "immutable file",
			in:   "file:app.db?immutable=1",
			want: "file:app.db?immutable=1&_txlock=immediate&_pragma=busy_timeout(5000)&_pragma=synchronous(NORMAL)",
		},
		{
			name: "explicit journal mode pragma",
			in:   "file:app.db?_pragma=journal_mode(DELETE)",
			want: "file:app.db?_pragma=journal_mode(DELETE)&_txlock=immediate&_pragma=busy_timeout(5000)&_pragma=synchronous(NORMAL)",
		},
		{
			name:    "named memory",
			in:      "file:session?mode=memory",
			wantWAL: true,
			want:    "file:session?mode=memory&cache=shared&_txlock=immediate&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=synchronous(NORMAL)",
		},
		{
			name:    "explicit cache",
			in:      "file:session?mode=memory&cache=private",
			wantWAL: true,
			want:    "file:session?mode=memory&cache=private&_txlock=immediate&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=synchronous(NORMAL)",
		},
		{
			name:    "explicit transaction mode",
			in:      "file:app.db?_txlock=deferred",
			wantWAL: true,
			want:    "file:app.db?_txlock=deferred&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=synchronous(NORMAL)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := driver.NormalizeSQLiteDSN(tt.in)
			if got != tt.want {
				t.Fatalf("NormalizeSQLiteDSN(%q) = %q, want %q", tt.in, got, tt.want)
			}
			if tt.wantWAL && !strings.Contains(got, "journal_mode(WAL)") {
				t.Fatalf("expected WAL pragma in result: %s", got)
			}
		})
	}
}

func TestNormalizeSQLiteDSNOpenAndPragma(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test_wal.db")
	dsn := driver.NormalizeSQLiteDSN(dbPath)

	dbConn, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("sql.Open with normalized DSN: %v", err)
	}
	defer func() { _ = dbConn.Close() }()

	var journalMode string
	if err := dbConn.QueryRow("PRAGMA journal_mode").Scan(&journalMode); err != nil {
		t.Fatalf("query journal_mode: %v", err)
	}
	if !strings.EqualFold(journalMode, "wal") {
		t.Fatalf("PRAGMA journal_mode = %q, want wal", journalMode)
	}

	var busyTimeout int
	if err := dbConn.QueryRow("PRAGMA busy_timeout").Scan(&busyTimeout); err != nil {
		t.Fatalf("query busy_timeout: %v", err)
	}
	if busyTimeout != 5000 {
		t.Fatalf("PRAGMA busy_timeout = %d, want 5000", busyTimeout)
	}
}

func TestNormalizeSQLiteDSNReadOnlyOpensNonWALDatabase(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "readonly_legacy.db")
	ctx := t.Context()

	// 预置非 WAL 库（默认 journal_mode=delete），只读打开时若被强制切换 WAL 会报 SQLITE_READONLY。
	writer, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open writable database: %v", err)
	}
	if _, err := writer.ExecContext(ctx, "CREATE TABLE items (id INTEGER PRIMARY KEY); INSERT INTO items VALUES (1)"); err != nil {
		t.Fatalf("seed database: %v", err)
	}
	var journalMode string
	if err := writer.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&journalMode); err != nil {
		t.Fatalf("query journal_mode: %v", err)
	}
	if strings.EqualFold(journalMode, "wal") {
		t.Fatalf("precondition failed: seeded database must not use WAL, got %q", journalMode)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close writable database: %v", err)
	}

	dsn := driver.NormalizeSQLiteDSN("file:" + dbPath + "?mode=ro")
	if strings.Contains(dsn, "journal_mode(WAL)") {
		t.Fatalf("read-only DSN must not force WAL: %s", dsn)
	}

	reader, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("open read-only database: %v", err)
	}
	defer func() { _ = reader.Close() }()

	var one int
	if err := reader.QueryRowContext(ctx, "SELECT 1").Scan(&one); err != nil {
		t.Fatalf("SELECT 1 on read-only database: %v", err)
	}
	var count int
	if err := reader.QueryRowContext(ctx, "SELECT COUNT(*) FROM items").Scan(&count); err != nil {
		t.Fatalf("read seeded table on read-only database: %v", err)
	}
	if count != 1 {
		t.Fatalf("rows = %d, want 1", count)
	}
}

func TestNormalizedSQLiteFileAllowsWriterDuringReadTransaction(t *testing.T) {
	database, err := sql.Open("sqlite", driver.NormalizeSQLiteDSN(filepath.Join(t.TempDir(), "concurrent.db")))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = database.Close() }()
	ctx := t.Context()
	if _, err := database.ExecContext(ctx, "CREATE TABLE items (id INTEGER PRIMARY KEY); INSERT INTO items VALUES (1)"); err != nil {
		t.Fatal(err)
	}
	writer, err := database.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = writer.Close() }()
	reader, err := database.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reader.Rollback() }()
	var count int
	if err := reader.QueryRowContext(ctx, "SELECT COUNT(*) FROM items").Scan(&count); err != nil || count != 1 {
		t.Fatalf("initial read = %d, error = %v", count, err)
	}
	// WAL 应允许另一个连接写入，同时保持当前读事务的快照。
	written := make(chan error, 1)
	go func() {
		_, err := writer.ExecContext(ctx, "INSERT INTO items VALUES (2)")
		written <- err
	}()
	select {
	case err := <-written:
		if err != nil {
			t.Fatalf("write while read transaction is open: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("writer was blocked by the read transaction")
	}
	if err := reader.QueryRowContext(ctx, "SELECT COUNT(*) FROM items").Scan(&count); err != nil || count != 1 {
		t.Fatalf("snapshot read = %d, error = %v", count, err)
	}
	if err := reader.Rollback(); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRowContext(ctx, "SELECT COUNT(*) FROM items").Scan(&count); err != nil || count != 2 {
		t.Fatalf("committed read = %d, error = %v", count, err)
	}
}

func TestNormalizedSQLiteWriteTransactionAvoidsLockUpgradeFailure(t *testing.T) {
	database, err := sql.Open("sqlite", driver.NormalizeSQLiteDSN(filepath.Join(t.TempDir(), "write.db")))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = database.Close() }()
	ctx := t.Context()
	if _, err := database.ExecContext(ctx, "CREATE TABLE items (id INTEGER PRIMARY KEY)"); err != nil {
		t.Fatal(err)
	}
	other, err := database.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = other.Close() }()
	first, err := database.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = first.Rollback() }()
	var count int
	if err := first.QueryRowContext(ctx, "SELECT COUNT(*) FROM items").Scan(&count); err != nil {
		t.Fatal(err)
	}
	written := make(chan error, 1)
	go func() {
		_, err := other.ExecContext(ctx, "INSERT INTO items VALUES (2)")
		written <- err
	}()
	// 第二个写入应等待当前可写事务，不能抢先提交并使它的读取快照失效。
	select {
	case err := <-written:
		t.Fatalf("concurrent writer completed before first transaction: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	if _, err := first.ExecContext(ctx, "INSERT INTO items VALUES (1)"); err != nil {
		t.Fatalf("write after read: %v", err)
	}
	if err := first.Commit(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-written:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("concurrent writer did not finish after first transaction committed")
	}
	if err := database.QueryRowContext(ctx, "SELECT COUNT(*) FROM items").Scan(&count); err != nil || count != 2 {
		t.Fatalf("committed rows = %d, error = %v", count, err)
	}
}
