package gormorm

import (
	"testing"
	"time"

	dbschema "gochen-runtime/db/schema"
	"gochen/db/dialect"
)

type schemaUser struct {
	ID       int64  `gorm:"primaryKey;autoIncrement"`
	Code     string `gorm:"column:code;size:64;not null;uniqueIndex:idx_schema_users_code"`
	Email    string `gorm:"column:email;unique"`
	Name     string `gorm:"column:display_name;type:varchar(128);default:'anonymous'"`
	Age      int    `gorm:"column:age;default:18"`
	Payload  []byte `gorm:"column:payload"`
	Created  time.Time
	TenantID int64  `gorm:"column:tenant_id;index:idx_schema_users_tenant_status,priority:1"`
	Status   string `gorm:"column:status;size:16;index:idx_schema_users_tenant_status,priority:2"`
	Ignored  string `gorm:"-:migration"`
	Skipped  string `gorm:"-"`
}

func (schemaUser) TableName() string { return "schema_users" }

type schemaUnsignedUser struct {
	ID       uint64 `gorm:"primaryKey"`
	TenantID uint   `gorm:"column:tenant_id"`
	Flags    uint32 `gorm:"column:flags"`
}

func (schemaUnsignedUser) TableName() string { return "schema_unsigned_users" }

type schemaSQLDefaultTextUser struct {
	ID        int64  `gorm:"primaryKey;autoIncrement"`
	CreatedAt string `gorm:"column:created_at;default:CURRENT_TIMESTAMP"`
}

func (schemaSQLDefaultTextUser) TableName() string { return "schema_sql_default_text_users" }

type schemaEmptyStringDefaultUser struct {
	ID   int64  `gorm:"primaryKey;autoIncrement"`
	Name string `gorm:"column:name;default:''"`
}

func (schemaEmptyStringDefaultUser) TableName() string { return "schema_empty_string_default_users" }

func TestSchemaFromModels(t *testing.T) {
	got, err := SchemaFromModels(&schemaUser{})
	if err != nil {
		t.Fatalf("SchemaFromModels: %v", err)
	}
	if len(got.Tables) != 1 {
		t.Fatalf("expected one table, got %d", len(got.Tables))
	}

	table := got.Tables[0]
	if table.Name != "schema_users" {
		t.Fatalf("expected table schema_users, got %q", table.Name)
	}
	if _, ok := table.FindColumn("ignored"); ok {
		t.Fatalf("expected ignored column skipped")
	}
	if _, ok := table.FindColumn("skipped"); ok {
		t.Fatalf("expected skipped column skipped")
	}

	assertColumn(t, &table, "id", "INTEGER", false, "", true, true)
	assertColumn(t, &table, "code", "VARCHAR(64)", false, "", false, false)
	assertColumn(t, &table, "email", "TEXT", true, "", false, false)
	assertColumn(t, &table, "display_name", "varchar(128)", true, "'anonymous'", false, false)
	assertColumn(t, &table, "age", "INTEGER", true, "18", false, false)
	assertColumn(t, &table, "payload", "BLOB", true, "", false, false)
	assertColumn(t, &table, "created", "DATETIME", true, "", false, false)
	assertColumn(t, &table, "tenant_id", "BIGINT", true, "", false, false)
	assertColumn(t, &table, "status", "VARCHAR(16)", true, "", false, false)

	codeIndex, ok := table.FindIndex("idx_schema_users_code")
	if !ok {
		t.Fatalf("expected code unique index")
	}
	if !codeIndex.Unique || len(codeIndex.Columns) != 1 || codeIndex.Columns[0] != "code" {
		t.Fatalf("unexpected code index: %+v", *codeIndex)
	}

	compositeIndex, ok := table.FindIndex("idx_schema_users_tenant_status")
	if !ok {
		t.Fatalf("expected composite index")
	}
	if compositeIndex.Unique || len(compositeIndex.Columns) != 2 ||
		compositeIndex.Columns[0] != "tenant_id" ||
		compositeIndex.Columns[1] != "status" {
		t.Fatalf("unexpected composite index: %+v", *compositeIndex)
	}

	emailUnique, ok := table.FindIndex("uni_schema_users_email")
	if !ok {
		t.Fatalf("expected email unique constraint")
	}
	if !emailUnique.Unique || len(emailUnique.Columns) != 1 || emailUnique.Columns[0] != "email" {
		t.Fatalf("unexpected email unique constraint: %+v", *emailUnique)
	}
}

func TestSchemaFromModelsRejectsNilModel(t *testing.T) {
	if _, err := SchemaFromModels(nil); err == nil {
		t.Fatalf("expected nil model error")
	}
}

func TestSchemaFromModelsWithDialectPostgres(t *testing.T) {
	got, err := SchemaFromModelsWithDialect(dialect.New("postgres"), &schemaUser{})
	if err != nil {
		t.Fatalf("SchemaFromModelsWithDialect: %v", err)
	}
	if len(got.Tables) != 1 {
		t.Fatalf("expected one table, got %d", len(got.Tables))
	}
	table := got.Tables[0]
	assertColumn(t, &table, "id", "BIGINT", false, "", true, true)
	assertColumn(t, &table, "payload", "BYTEA", true, "", false, false)
	assertColumn(t, &table, "created", "TIMESTAMPTZ", true, "", false, false)
}

func TestSchemaFromModelsWithDialectMySQLPreservesUnsignedIntegers(t *testing.T) {
	got, err := SchemaFromModelsWithDialect(dialect.New("mysql"), &schemaUnsignedUser{})
	if err != nil {
		t.Fatalf("SchemaFromModelsWithDialect: %v", err)
	}
	if len(got.Tables) != 1 {
		t.Fatalf("expected one table, got %d", len(got.Tables))
	}
	table := got.Tables[0]
	assertColumn(t, &table, "id", "BIGINT UNSIGNED", false, "", true, true)
	assertColumn(t, &table, "tenant_id", "INTEGER UNSIGNED", true, "", false, false)
	assertColumn(t, &table, "flags", "INTEGER UNSIGNED", true, "", false, false)
}

func TestSchemaFromModelsPreservesSQLKeywordDefaultOnTextColumn(t *testing.T) {
	got, err := SchemaFromModels(&schemaSQLDefaultTextUser{})
	if err != nil {
		t.Fatalf("SchemaFromModels: %v", err)
	}
	if len(got.Tables) != 1 {
		t.Fatalf("expected one table, got %d", len(got.Tables))
	}
	assertColumn(t, &got.Tables[0], "created_at", "TEXT", true, "CURRENT_TIMESTAMP", false, false)
}

func TestSchemaFromModelsPreservesEmptyStringDefault(t *testing.T) {
	got, err := SchemaFromModels(&schemaEmptyStringDefaultUser{})
	if err != nil {
		t.Fatalf("SchemaFromModels: %v", err)
	}
	if len(got.Tables) != 1 {
		t.Fatalf("expected one table, got %d", len(got.Tables))
	}
	assertColumn(t, &got.Tables[0], "name", "TEXT", true, "''", false, false)
}

func assertColumn(
	t *testing.T,
	table *dbschema.Table,
	name string,
	typ string,
	nullable bool,
	defaultValue string,
	primaryKey bool,
	autoIncrement bool,
) {
	t.Helper()

	column, ok := table.FindColumn(name)
	if !ok {
		t.Fatalf("expected column %s", name)
	}
	if column.Type != typ ||
		column.Nullable != nullable ||
		column.DefaultValue != defaultValue ||
		column.PrimaryKey != primaryKey ||
		column.AutoIncrement != autoIncrement {
		t.Fatalf("unexpected column %s: %+v", name, *column)
	}
}
