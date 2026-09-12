package gormfactory

import (
	"context"
	"testing"

	gormdb "gochen-contrib/data/db/gorm"
	core "gochen/db"
	"gochen/errors"
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
