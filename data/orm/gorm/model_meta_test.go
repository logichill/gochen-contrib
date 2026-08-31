package gormorm

import (
	"context"
	"testing"

	repopkg "gochen-runtime/db/orm/repo"
	"gochen/app/query"
	"gochen/db/orm"
)

type queryUser struct {
	ID       int64  `gorm:"primaryKey"`
	TenantID string `gorm:"column:tenant_id;index"`
	Name     string `gorm:"column:name"`
	Version  uint64 `gorm:"column:version"`
}

func (u *queryUser) GetID() int64       { return u.ID }
func (u *queryUser) GetVersion() uint64 { return u.Version }

func TestOrm_ModelInfersFieldMetadataForRepositoryQueries(t *testing.T) {
	db := setupDB(t)
	if err := db.AutoMigrate(&queryUser{}); err != nil {
		t.Fatalf("AutoMigrate queryUser: %v", err)
	}
	if err := db.Create(&queryUser{TenantID: "tenant-a", Name: "alice"}).Error; err != nil {
		t.Fatalf("create queryUser: %v", err)
	}

	adapter, err := New(db)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	repository, err := repopkg.NewRepo[*queryUser, int64](adapter, "query_users")
	if err != nil {
		t.Fatalf("NewRepo: %v", err)
	}
	items, err := repository.Query(context.Background(), query.QueryOptions{
		Filters: query.QueryFilters{
			"tenant_id": {{Op: query.FilterOpEq, Value: query.StringValue("tenant-a")}},
		},
	})
	if err != nil {
		t.Fatalf("Query with inferred tenant_id field: %v", err)
	}
	if len(items) != 1 || items[0].Name != "alice" {
		t.Fatalf("unexpected query result: %+v", items)
	}

	model, err := adapter.Model(&orm.ModelMeta{
		ModelFactory: orm.NewModelFactory[*queryUser](),
		Table:        "query_users",
	})
	if err != nil {
		t.Fatalf("Model: %v", err)
	}
	fields := model.Meta().Fields
	if len(fields) == 0 {
		t.Fatal("expected inferred model fields")
	}
	if !containsModelField(fields, "TenantID", "tenant_id") {
		t.Fatalf("expected TenantID/tenant_id metadata, got %+v", fields)
	}
}

func TestOrm_ModelPreservesExplicitFieldMetadata(t *testing.T) {
	db := setupDB(t)
	adapter, err := New(db)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	explicit := []orm.FieldMeta{{Name: "Alias", Column: "alias"}}
	model, err := adapter.Model(&orm.ModelMeta{
		ModelFactory: orm.NewModelFactory[*queryUser](),
		Table:        "query_users",
		Fields:       explicit,
	})
	if err != nil {
		t.Fatalf("Model: %v", err)
	}
	if len(model.Meta().Fields) != 1 ||
		model.Meta().Fields[0].Name != explicit[0].Name ||
		model.Meta().Fields[0].Column != explicit[0].Column {
		t.Fatalf("explicit fields were replaced: %+v", model.Meta().Fields)
	}
}

func containsModelField(fields []orm.FieldMeta, name string, column string) bool {
	for _, field := range fields {
		if field.Name == name && field.Column == column {
			return true
		}
	}
	return false
}
