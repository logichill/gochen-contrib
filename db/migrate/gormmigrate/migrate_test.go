package gormmigrate

import (
	"context"
	"testing"

	"gochen/errors"
)

type migrateTestEntity struct {
	ID   int64  `gorm:"primaryKey;autoIncrement"`
	Name string `gorm:"column:name;size:64"`
}

func (migrateTestEntity) TableName() string { return "migrate_test_entities" }

func TestAutoMigrate_RejectsNilDatabase(t *testing.T) {
	err := AutoMigrate(context.Background(), nil, &migrateTestEntity{})
	if err == nil || errors.Code(err) != errors.InvalidInput {
		t.Fatalf("expected InvalidInput error, got %v", err)
	}
}

func TestAutoMigrate_Success(t *testing.T) {
	database := openDraftDatabase(t)
	// nil context 使用默认上下文，迁移结果应包含可读写的模型列。
	if err := AutoMigrate(nil, database, &migrateTestEntity{}); err != nil {
		t.Fatalf("AutoMigrate failed: %v", err)
	}
	ctx := context.Background()
	if _, err := database.Exec(ctx, "INSERT INTO migrate_test_entities (name) VALUES (?)", "migrated"); err != nil {
		t.Fatalf("insert into migrated table: %v", err)
	}
	var entity migrateTestEntity
	if err := database.QueryRow(ctx, "SELECT id, name FROM migrate_test_entities").Scan(&entity.ID, &entity.Name); err != nil {
		t.Fatalf("read migrated model columns: %v", err)
	}
	if entity.ID <= 0 || entity.Name != "migrated" {
		t.Fatalf("unexpected migrated entity: %+v", entity)
	}
}
