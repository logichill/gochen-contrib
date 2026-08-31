package driver_test

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	"gochen-contrib/data/db/driver"
)

func TestNormalizeSQLiteDSN(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "empty",
			in:   "",
			want: "",
		},
		{
			name: "bare path",
			in:   "app.db",
			want: "file:app.db?cache=shared&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=synchronous(NORMAL)",
		},
		{
			name: "memory",
			in:   ":memory:",
			want: ":memory:?cache=shared&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=synchronous(NORMAL)",
		},
		{
			name: "file prefix with query",
			in:   "file:app.db?mode=ro",
			want: "file:app.db?mode=ro&cache=shared&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=synchronous(NORMAL)",
		},
		{
			name: "file prefix with trailing ampersand",
			in:   "file:app.db?mode=ro&",
			want: "file:app.db?mode=ro&cache=shared&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=synchronous(NORMAL)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := driver.NormalizeSQLiteDSN(tt.in)
			if got != tt.want {
				t.Fatalf("NormalizeSQLiteDSN(%q) = %q, want %q", tt.in, got, tt.want)
			}
			if tt.in != "" && !strings.Contains(got, "journal_mode(WAL)") {
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
	defer dbConn.Close()

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
