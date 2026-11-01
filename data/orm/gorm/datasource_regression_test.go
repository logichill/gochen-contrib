package gormorm

import (
	"context"
	"fmt"
	"testing"

	gormdb "gochen-contrib/data/db/gorm"
	repopkg "gochen-runtime/db/orm/repo"
	checkpointstore "gochen-runtime/eventing/projection/sqlstore"
	"gochen/domain/spec"
	"gochen/errors"
	projection "gochen/eventing/projection"

	gormsqlite "github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// TestRegressionF01_SamePoolDerivationsShareDatasource 验证：同一连接池经
// WithContext 派生的多个 ORM 实例共享同一数据源身份，跨实例事务传播
// 不被误判为异源（身份取底层 *sql.DB，而非 *gorm.DB 实例指针）。
func TestRegressionF01_SamePoolDerivationsShareDatasource(t *testing.T) {
	gdb, err := gorm.Open(
		gormsqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())),
		&gorm.Config{},
	)
	if err != nil {
		t.Fatalf("gorm.Open: %v", err)
	}
	sqlDB, err := gdb.DB()
	if err != nil {
		t.Fatalf("gorm.DB: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := gdb.AutoMigrate(&txUser{}); err != nil {
		t.Fatalf("AutoMigrate: %v", err)
	}

	engine1, err := New(gdb)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	engine2, err := New(gdb.WithContext(context.Background()))
	if err != nil {
		t.Fatalf("New(with-context): %v", err)
	}

	repo1, err := repopkg.NewRepo[*txUser, int64](engine1, "tx_users")
	if err != nil {
		t.Fatalf("NewRepo: %v", err)
	}
	repo2, err := repopkg.NewRepo[*txUser, int64](engine2, "tx_users")
	if err != nil {
		t.Fatalf("NewRepo: %v", err)
	}

	err = repo1.WithinTx(context.Background(), func(ctx context.Context) error {
		return repo2.Create(ctx, &txUser{ID: 7, Version: 0}, spec.All())
	})
	if err != nil {
		t.Fatalf("same-pool propagation rejected: %v", err)
	}
}

// TestRegressionF01_DistinctPoolsAreRejected 验证：不同连接池的 ORM 实例
// 参与同一事务传播时被拒绝。
func TestRegressionF01_DistinctPoolsAreRejected(t *testing.T) {
	open := func(name string) *Orm {
		gdb, err := gorm.Open(
			gormsqlite.Open(fmt.Sprintf("file:%s-%s?mode=memory&cache=shared", t.Name(), name)),
			&gorm.Config{},
		)
		if err != nil {
			t.Fatalf("gorm.Open: %v", err)
		}
		sqlDB, err := gdb.DB()
		if err != nil {
			t.Fatalf("gorm.DB: %v", err)
		}
		t.Cleanup(func() { _ = sqlDB.Close() })
		if err := gdb.AutoMigrate(&txUser{}); err != nil {
			t.Fatalf("AutoMigrate: %v", err)
		}
		engine, err := New(gdb)
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		return engine
	}

	engineA := open("a")
	engineB := open("b")
	repoA, err := repopkg.NewRepo[*txUser, int64](engineA, "tx_users")
	if err != nil {
		t.Fatalf("NewRepo: %v", err)
	}
	repoB, err := repopkg.NewRepo[*txUser, int64](engineB, "tx_users")
	if err != nil {
		t.Fatalf("NewRepo: %v", err)
	}

	err = repoA.WithinTx(context.Background(), func(ctx context.Context) error {
		return repoB.Create(ctx, &txUser{ID: 8}, spec.All())
	})
	if err == nil {
		t.Fatal("cross-pool propagation must be rejected")
	}
	if !errors.Is(err, errors.FailedPrecondition) {
		t.Fatalf("unexpected error code: %v", err)
	}
}

// TestRegressionF01_CheckpointOverSamePoolGormDatabase 验证：gormdb.Database
// （检查点存储侧）与 gorm ORM 会话（仓储侧）对同一连接池给出相同身份——
// 同库检查点保存不再被误判为异源。
func TestRegressionF01_CheckpointOverSamePoolGormDatabase(t *testing.T) {
	gdb, err := gorm.Open(
		gormsqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())),
		&gorm.Config{},
	)
	if err != nil {
		t.Fatalf("gorm.Open: %v", err)
	}
	sqlDB, err := gdb.DB()
	if err != nil {
		t.Fatalf("gorm.DB: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := gdb.AutoMigrate(&txUser{}); err != nil {
		t.Fatalf("AutoMigrate: %v", err)
	}

	database, err := gormdb.New(gdb)
	if err != nil {
		t.Fatalf("gormdb.New: %v", err)
	}
	engine, err := New(gdb)
	if err != nil {
		t.Fatalf("gormorm.New: %v", err)
	}
	if err := gdb.Exec(`CREATE TABLE IF NOT EXISTS projection_checkpoints (
		projection_name TEXT PRIMARY KEY,
		position INTEGER NOT NULL DEFAULT 0,
		last_event_id TEXT,
		last_event_time DATETIME,
		updated_at DATETIME)`).Error; err != nil {
		t.Fatalf("create checkpoints table: %v", err)
	}
	store := checkpointstore.NewSQLCheckpointStore(database, "projection_checkpoints")

	repository, err := repopkg.NewRepo[*txUser, int64](engine, "tx_users")
	if err != nil {
		t.Fatalf("NewRepo: %v", err)
	}
	err = repository.WithinTx(context.Background(), func(ctx context.Context) error {
		return store.Save(ctx, &projection.Checkpoint{
			ProjectionName: "reg-checkpoint",
			Position:       42,
		})
	})
	if err != nil {
		t.Fatalf("checkpoint save within same-pool tx rejected: %v", err)
	}
}
