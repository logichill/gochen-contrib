package gormdb

import (
	"context"
	stderrors "errors"
	"fmt"
	"os"
	"testing"
	"time"

	core "gochen/db"

	gormmysql "gorm.io/driver/mysql"
	gormpostgres "gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// TestExternalDialectSavepoints exercises the same savepoint contract against
// real PostgreSQL/MySQL services when their DSNs are explicitly supplied. It
// is skipped by default so local quality checks never start or modify services.
func TestExternalDialectSavepoints(t *testing.T) {
	cases := []struct {
		name string
		dsn  string
		open func(string) gorm.Dialector
	}{
		{name: "postgres", dsn: os.Getenv("GOCHEN_POSTGRES_DSN"), open: gormpostgres.Open},
		{name: "mysql", dsn: os.Getenv("GOCHEN_MYSQL_DSN"), open: gormmysql.Open},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			if tc.dsn == "" {
				if os.Getenv("GOCHEN_REQUIRE_DATABASE_TESTS") == "1" {
					t.Fatal("database DSN is required for this verification run")
				}
				t.Skip("set GOCHEN_POSTGRES_DSN or GOCHEN_MYSQL_DSN to run external adapter verification")
			}
			gdb, err := gorm.Open(tc.open(tc.dsn), &gorm.Config{})
			if err != nil {
				t.Fatalf("open %s: %v", tc.name, err)
			}
			database, err := New(gdb)
			if err != nil {
				t.Fatalf("wrap %s: %v", tc.name, err)
			}
			t.Cleanup(func() {
				if err := database.Close(); err != nil {
					t.Errorf("close database: %v", err)
				}
			})
			ctx := context.Background()
			table := fmt.Sprintf("gochen_savepoint_%d", time.Now().UnixNano())
			if _, err := database.Exec(ctx, "CREATE TABLE "+table+" (id INTEGER PRIMARY KEY)"); err != nil {
				t.Fatalf("create table: %v", err)
			}
			t.Cleanup(func() {
				if _, err := database.Exec(context.Background(), "DROP TABLE "+table); err != nil {
					t.Errorf("drop test table: %v", err)
				}
			})

			tx, err := database.Begin(ctx)
			if err != nil {
				t.Fatalf("begin: %v", err)
			}
			sp := tx.(core.ISavepointTransaction)
			if err := sp.RollbackToSavepoint(ctx, "missing_savepoint"); err == nil {
				t.Fatal("rollback to missing savepoint unexpectedly succeeded")
			}
			// PostgreSQL marks the transaction aborted after an invalid savepoint
			// command. The contract requires the caller to be able to roll back the
			// whole transaction, not to continue using the failed transaction.
			if err := tx.Rollback(); err != nil {
				t.Fatalf("rollback after failed savepoint: %v", err)
			}
			tx, err = database.Begin(ctx)
			if err != nil {
				t.Fatalf("begin after failed savepoint rollback: %v", err)
			}
			cancelCtx, cancel := context.WithCancel(ctx)
			cancel()
			if _, cancelErr := tx.Exec(cancelCtx, "SELECT 1"); cancelErr == nil {
				t.Fatal("canceled statement unexpectedly succeeded")
			} else if !stderrors.Is(cancelErr, context.Canceled) {
				t.Fatalf("canceled statement error = %v, want context.Canceled", cancelErr)
			}
			if err := tx.Rollback(); err != nil {
				t.Fatalf("rollback after canceled statement: %v", err)
			}
			tx, err = database.Begin(ctx)
			if err != nil {
				t.Fatalf("begin after canceled statement rollback: %v", err)
			}
			defer func() { _ = tx.Rollback() }()
			sp = tx.(core.ISavepointTransaction)
			if _, err := tx.Exec(ctx, "INSERT INTO "+table+" (id) VALUES (1)"); err != nil {
				t.Fatalf("insert first row after failed savepoint: %v", err)
			}
			if err := sp.CreateSavepoint(ctx, "sp_one"); err != nil {
				t.Fatalf("create savepoint: %v", err)
			}
			if _, err := tx.Exec(ctx, "INSERT INTO "+table+" (id) VALUES (2)"); err != nil {
				t.Fatalf("insert second row: %v", err)
			}
			if err := sp.RollbackToSavepoint(ctx, "sp_one"); err != nil {
				t.Fatalf("rollback savepoint: %v", err)
			}
			if err := sp.ReleaseSavepoint(ctx, "sp_one"); err != nil {
				t.Fatalf("release savepoint: %v", err)
			}
			if err := tx.Commit(); err != nil {
				t.Fatalf("commit: %v", err)
			}

			var count int
			if err := database.QueryRow(ctx, "SELECT COUNT(*) FROM "+table).Scan(&count); err != nil {
				t.Fatalf("count rows: %v", err)
			}
			if count != 1 {
				t.Fatalf("row count = %d, want 1", count)
			}
		})
	}
}
