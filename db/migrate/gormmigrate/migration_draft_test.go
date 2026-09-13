package gormmigrate

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	gormdb "gochen-contrib/data/db/gorm"
	"gochen-runtime/db/migrate"
	dbschema "gochen-runtime/db/schema"
	schemadiff "gochen-runtime/db/schema/diff"
	core "gochen/db"
	"gochen/db/dialect"

	gormsqlite "github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

type draftUser struct {
	ID    int64  `gorm:"primaryKey;autoIncrement"`
	Email string `gorm:"column:email;size:128;not null;uniqueIndex:idx_draft_users_email"`
	Name  string `gorm:"column:name;default:'anonymous'"`
}

func (draftUser) TableName() string { return "draft_users" }

type draftSafeUser struct {
	ID    int64  `gorm:"primaryKey;autoIncrement"`
	Email string `gorm:"column:email;size:128;uniqueIndex:idx_draft_safe_users_email"`
	Name  string `gorm:"column:name;default:'anonymous'"`
}

func (draftSafeUser) TableName() string { return "draft_safe_users" }

type draftExpressionIndexUser struct {
	ID    int64  `gorm:"primaryKey;autoIncrement"`
	Email string `gorm:"column:email;index:idx_lower_email,expression:lower(email)"`
}

func (draftExpressionIndexUser) TableName() string { return "draft_expression_index_users" }

type draftInlineUniqueUser struct {
	ID    int64  `gorm:"primaryKey;autoIncrement"`
	Email string `gorm:"column:email;unique"`
}

func (draftInlineUniqueUser) TableName() string { return "draft_inline_unique_users" }

type draftEmptyDefaultUser struct {
	ID   int64  `gorm:"primaryKey;autoIncrement"`
	Name string `gorm:"column:name;default:''"`
}

func (draftEmptyDefaultUser) TableName() string { return "draft_empty_default_users" }

func TestGenerateMigrationDraftCreatesTableSQL(t *testing.T) {
	database := openDraftDatabase(t)

	draft, err := GenerateMigrationDraft(context.Background(), database, &draftUser{})
	if err != nil {
		t.Fatalf("GenerateMigrationDraft: %v", err)
	}
	if len(draft.Changes) != 1 {
		t.Fatalf("expected one add table change, got %d", len(draft.Changes))
	}
	upSQL := draft.UpSQL()
	if !strings.Contains(upSQL, `CREATE TABLE "draft_users"`) {
		t.Fatalf("expected create table SQL, got:\n%s", upSQL)
	}
	if !strings.Contains(upSQL, `CREATE UNIQUE INDEX "idx_draft_users_email"`) {
		t.Fatalf("expected unique index SQL, got:\n%s", upSQL)
	}
	if !strings.Contains(upSQL, `DEFAULT 'anonymous'`) {
		t.Fatalf("expected quoted string default SQL, got:\n%s", upSQL)
	}
	downSQL := draft.DownSQL()
	if !strings.Contains(downSQL, downReviewGuard) || !strings.Contains(downSQL, downReviewWarning) {
		t.Fatalf("expected guarded down SQL, got:\n%s", downSQL)
	}
	if !strings.Contains(downSQL, `DROP TABLE "draft_users"`) {
		t.Fatalf("expected drop table down SQL, got:\n%s", downSQL)
	}
}

func TestGenerateMigrationDraftAddsColumnAndIndex(t *testing.T) {
	database := openDraftDatabase(t)
	if _, err := database.Exec(context.Background(), `CREATE TABLE draft_safe_users (id INTEGER PRIMARY KEY AUTOINCREMENT)`); err != nil {
		t.Fatalf("create existing table: %v", err)
	}

	draft, err := GenerateMigrationDraft(context.Background(), database, &draftSafeUser{})
	if err != nil {
		t.Fatalf("GenerateMigrationDraft: %v", err)
	}
	upSQL := draft.UpSQL()
	if !strings.Contains(upSQL, `ALTER TABLE "draft_safe_users" ADD COLUMN "email" VARCHAR(128)`) {
		t.Fatalf("expected add email column SQL, got:\n%s", upSQL)
	}
	if !strings.Contains(upSQL, `ALTER TABLE "draft_safe_users" ADD COLUMN "name" TEXT DEFAULT 'anonymous'`) {
		t.Fatalf("expected add name column SQL, got:\n%s", upSQL)
	}
	if !strings.Contains(upSQL, `CREATE UNIQUE INDEX "idx_draft_safe_users_email" ON "draft_safe_users" ("email")`) {
		t.Fatalf("expected add index SQL, got:\n%s", upSQL)
	}
	downSQL := draft.DownSQL()
	if !strings.Contains(downSQL, `DROP INDEX "idx_draft_safe_users_email"`) ||
		!strings.Contains(downSQL, `ALTER TABLE "draft_safe_users" DROP COLUMN "email"`) ||
		!strings.Contains(downSQL, `ALTER TABLE "draft_safe_users" DROP COLUMN "name"`) {
		t.Fatalf("expected reverse down SQL, got:\n%s", downSQL)
	}
}

func TestGenerateMigrationDraftWarnsUnsafeAddColumn(t *testing.T) {
	database := openDraftDatabase(t)
	if _, err := database.Exec(context.Background(), `CREATE TABLE draft_users (id INTEGER PRIMARY KEY AUTOINCREMENT)`); err != nil {
		t.Fatalf("create existing table: %v", err)
	}

	draft, err := GenerateMigrationDraft(context.Background(), database, &draftUser{})
	if err != nil {
		t.Fatalf("GenerateMigrationDraft: %v", err)
	}
	if containsWarning(draft.Warnings, "may fail when the table already contains rows") {
		t.Fatalf("expected skipped SQLite column not to also emit may-fail warning, got %#v", draft.Warnings)
	}
	if !containsWarning(draft.Warnings, "Skipped SQLite ADD COLUMN for NOT NULL column draft_users.email") {
		t.Fatalf("expected skipped unsafe column warning, got %#v", draft.Warnings)
	}
	if !containsWarning(draft.Warnings, "Skipped SQLite index idx_draft_users_email") {
		t.Fatalf("expected skipped dependent index warning, got %#v", draft.Warnings)
	}
	if containsStatement(draft.Statements, `ADD COLUMN "email"`) || containsStatement(draft.Statements, `idx_draft_users_email`) {
		t.Fatalf("expected unsafe column and dependent index to be omitted from executable up statements, got %#v", draft.Statements)
	}
	if containsStatement(draft.DownStatements, `DROP COLUMN "email"`) || containsStatement(draft.DownStatements, `idx_draft_users_email`) {
		t.Fatalf("expected unsafe column and dependent index to be omitted from executable down statements, got %#v", draft.DownStatements)
	}
	for _, change := range draft.Changes {
		if change.Kind == schemadiff.KindAddColumn && change.Column != nil && change.Column.Name == "email" {
			t.Fatalf("expected unsafe column to be omitted from renderable changes, got %#v", draft.Changes)
		}
		if change.Kind == schemadiff.KindAddIndex && change.Index != nil && change.Index.Name == "idx_draft_users_email" {
			t.Fatalf("expected dependent index to be omitted from renderable changes, got %#v", draft.Changes)
		}
	}
}

func TestGenerateMigrationDraftWithOptionsWarnsUnsafeAddColumnForNonSQLite(t *testing.T) {
	current := &dbschema.Schema{Tables: []dbschema.Table{
		{Name: "draft_users", Columns: []dbschema.Column{{Name: "id", Type: "BIGINT", PrimaryKey: true}}},
	}}
	desired := &dbschema.Schema{Tables: []dbschema.Table{
		{
			Name: "draft_users",
			Columns: []dbschema.Column{
				{Name: "id", Type: "BIGINT", PrimaryKey: true},
				{Name: "email", Type: "VARCHAR(128)", Nullable: false},
			},
		},
	}}
	changes := schemadiff.Between(current, desired)
	warnings := migrationDraftWarnings(dialect.New("postgres"), current, desired, changes, nil)

	if !containsWarning(warnings, "Adding NOT NULL column draft_users.email without a default may fail") {
		t.Fatalf("expected non-SQLite unsafe add column warning, got %#v", warnings)
	}
}

func TestGenerateMigrationDraftReportsDrifts(t *testing.T) {
	database := openDraftDatabase(t)
	if _, err := database.Exec(context.Background(), `CREATE TABLE draft_users (id INTEGER PRIMARY KEY AUTOINCREMENT, email TEXT)`); err != nil {
		t.Fatalf("create existing table: %v", err)
	}
	if _, err := database.Exec(context.Background(), `CREATE INDEX idx_draft_users_email ON draft_users(email)`); err != nil {
		t.Fatalf("create existing index: %v", err)
	}

	draft, err := GenerateMigrationDraft(context.Background(), database, &draftUser{})
	if err != nil {
		t.Fatalf("GenerateMigrationDraft: %v", err)
	}
	if len(draft.Drifts) != 2 {
		t.Fatalf("expected two drifts, got %#v", draft.Drifts)
	}
	if len(draft.Warnings) != 2 || !strings.Contains(draft.Warnings[0], "Schema drift requires manual review") {
		t.Fatalf("expected drift warnings, got %#v", draft.Warnings)
	}
	if !strings.Contains(draft.UpSQL(), "Schema drift requires manual review") {
		t.Fatalf("expected drift warning in up SQL, got:\n%s", draft.UpSQL())
	}
}

func TestGenerateMigrationDraftDoesNotDuplicateSQLiteUniqueConstraint(t *testing.T) {
	database := openDraftDatabase(t)
	if _, err := database.Exec(context.Background(), `CREATE TABLE draft_inline_unique_users (id INTEGER PRIMARY KEY AUTOINCREMENT, email TEXT UNIQUE)`); err != nil {
		t.Fatalf("create existing table: %v", err)
	}

	draft, err := GenerateMigrationDraft(context.Background(), database, &draftInlineUniqueUser{})
	if err != nil {
		t.Fatalf("GenerateMigrationDraft: %v", err)
	}
	if len(draft.Changes) != 0 {
		t.Fatalf("expected no changes, got %#v", draft.Changes)
	}
	if len(draft.Drifts) != 0 {
		t.Fatalf("expected no drifts, got %#v", draft.Drifts)
	}
	if strings.Contains(draft.UpSQL(), "CREATE UNIQUE INDEX") {
		t.Fatalf("expected no duplicate unique index SQL, got:\n%s", draft.UpSQL())
	}
}

func TestGenerateMigrationDraftWarnsSkippedGORMExpressionIndex(t *testing.T) {
	database := openDraftDatabase(t)
	draft, err := GenerateMigrationDraft(context.Background(), database, &draftExpressionIndexUser{})
	if err != nil {
		t.Fatalf("GenerateMigrationDraft: %v", err)
	}
	if !containsWarning(draft.Warnings, "expression index") {
		t.Fatalf("expected expression index warning, got %#v", draft.Warnings)
	}
	if strings.Contains(draft.UpSQL(), `CREATE INDEX "idx_lower_email"`) {
		t.Fatalf("expected expression index not to be rendered, got:\n%s", draft.UpSQL())
	}
}

func TestGenerateMigrationDraftPreservesEmptyStringDefault(t *testing.T) {
	database := openDraftDatabase(t)
	draft, err := GenerateMigrationDraft(context.Background(), database, &draftEmptyDefaultUser{})
	if err != nil {
		t.Fatalf("GenerateMigrationDraft: %v", err)
	}
	if !strings.Contains(draft.UpSQL(), `DEFAULT ''`) {
		t.Fatalf("expected empty string default SQL, got:\n%s", draft.UpSQL())
	}
}

func TestGenerateMigrationDraftPropagatesSQLiteIntrospectionWarnings(t *testing.T) {
	database := openDraftDatabase(t)
	if _, err := database.Exec(context.Background(), `CREATE TABLE draft_safe_users (id INTEGER PRIMARY KEY AUTOINCREMENT, email TEXT)`); err != nil {
		t.Fatalf("create existing table: %v", err)
	}
	if _, err := database.Exec(context.Background(), `CREATE INDEX idx_draft_safe_users_lower_email_name ON draft_safe_users(lower(email), id)`); err != nil {
		t.Fatalf("create expression index: %v", err)
	}

	draft, err := GenerateMigrationDraft(context.Background(), database, &draftSafeUser{})
	if err != nil {
		t.Fatalf("GenerateMigrationDraft: %v", err)
	}
	if !containsWarning(draft.Warnings, "expression index draft_safe_users.idx_draft_safe_users_lower_email_name") {
		t.Fatalf("expected SQLite introspection warning, got %#v", draft.Warnings)
	}
	if !containsDrift(draft.Drifts, "idx_draft_safe_users_lower_email_name", "not represented by the schema AST") {
		t.Fatalf("expected unsupported expression index drift, got %#v", draft.Drifts)
	}
}

func TestGenerateMigrationDraftWithOptionsUsesCurrentSchema(t *testing.T) {
	database := openDraftDatabase(t)
	current := &dbschema.Schema{Tables: []dbschema.Table{
		{
			Name: "draft_safe_users",
			Columns: []dbschema.Column{
				{Name: "id", Type: "INTEGER", PrimaryKey: true, AutoIncrement: true},
			},
		},
	}}

	draft, err := GenerateMigrationDraftWithOptions(context.Background(), database, MigrationDraftOptions{Current: current}, &draftSafeUser{})
	if err != nil {
		t.Fatalf("GenerateMigrationDraftWithOptions: %v", err)
	}
	if len(draft.Changes) != 3 {
		t.Fatalf("expected changes from injected current schema, got %#v", draft.Changes)
	}
	if !strings.Contains(draft.UpSQL(), `ALTER TABLE "draft_safe_users" ADD COLUMN "email"`) {
		t.Fatalf("expected injected current schema to drive diff, got:\n%s", draft.UpSQL())
	}
}

func TestGenerateMigrationDraftWithOptionsNormalizesEquivalentDefaults(t *testing.T) {
	database := openDraftDatabase(t)
	current := &dbschema.Schema{Tables: []dbschema.Table{
		{
			Name: "draft_safe_users",
			Columns: []dbschema.Column{
				{Name: "id", Type: "INTEGER", PrimaryKey: true, AutoIncrement: true},
				{Name: "email", Type: "VARCHAR(128)", Nullable: true},
				{Name: "name", Type: "TEXT", Nullable: true, DefaultValue: "anonymous"},
			},
			Indexes: []dbschema.Index{
				{Name: "idx_draft_safe_users_email", Columns: []string{"email"}, Unique: true},
			},
		},
	}}

	draft, err := GenerateMigrationDraftWithOptions(context.Background(), database, MigrationDraftOptions{Current: current}, &draftSafeUser{})
	if err != nil {
		t.Fatalf("GenerateMigrationDraftWithOptions: %v", err)
	}
	if len(draft.Changes) != 0 {
		t.Fatalf("expected no changes, got %#v", draft.Changes)
	}
	if len(draft.Drifts) != 0 {
		t.Fatalf("expected no drifts, got %#v", draft.Drifts)
	}
}

func TestGenerateMigrationDraftWithOptionsUsesCustomIntrospector(t *testing.T) {
	database := openDraftDatabase(t)
	inspector := &stubIntrospector{schema: &dbschema.Schema{
		Tables: []dbschema.Table{
			{Name: "draft_safe_users", Columns: []dbschema.Column{{Name: "id", Type: "INTEGER", PrimaryKey: true}}},
		},
		Warnings: []string{"custom introspector warning"},
	}}

	draft, err := GenerateMigrationDraftWithOptions(context.Background(), database, MigrationDraftOptions{Introspector: inspector}, &draftSafeUser{})
	if err != nil {
		t.Fatalf("GenerateMigrationDraftWithOptions: %v", err)
	}
	if inspector.calls != 1 {
		t.Fatalf("custom introspector calls = %d, want 1", inspector.calls)
	}
	if !containsWarning(draft.Warnings, "custom introspector warning") {
		t.Fatalf("expected custom introspector warning, got %#v", draft.Warnings)
	}
}

func TestWriteMigrationDraft(t *testing.T) {
	database := openDraftDatabase(t)
	draft, err := GenerateMigrationDraft(context.Background(), database, &draftUser{})
	if err != nil {
		t.Fatalf("GenerateMigrationDraft: %v", err)
	}

	dir := t.TempDir()
	files, err := WriteMigrationDraft(dir, 1, "create draft users", draft)
	if err != nil {
		t.Fatalf("WriteMigrationDraft: %v", err)
	}
	if filepath.Base(files.UpPath) != "000001.schema.create_draft_users.up.sql" {
		t.Fatalf("unexpected up path: %s", files.UpPath)
	}
	if filepath.Base(files.DownPath) != "000001.schema.create_draft_users.down.sql" {
		t.Fatalf("unexpected down path: %s", files.DownPath)
	}

	source, err := migrate.NewFileSource(dir)
	if err != nil {
		t.Fatalf("NewFileSource: %v", err)
	}
	migrations, err := source.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(migrations) != 1 || migrations[0].Version != 1 || migrations[0].Name != "create_draft_users" {
		t.Fatalf("unexpected migrations: %#v", migrations)
	}
	if migrations[0].Type != "schema" {
		t.Fatalf("unexpected migration type: %#v", migrations[0])
	}

	content, err := os.ReadFile(files.DownPath)
	if err != nil {
		t.Fatalf("ReadFile down: %v", err)
	}
	if !strings.Contains(string(content), downReviewGuard) {
		t.Fatalf("expected guarded down SQL, got:\n%s", string(content))
	}
	if !strings.Contains(string(content), `DROP TABLE "draft_users"`) {
		t.Fatalf("expected down SQL, got:\n%s", string(content))
	}
}

func TestWriteMigrationDraftChecksBothFilesBeforeWriting(t *testing.T) {
	draft := &MigrationDraft{Statements: []string{"SELECT 1"}, DownStatements: []string{"SELECT 1"}}
	dir := t.TempDir()
	downPath := filepath.Join(dir, "000001.schema.existing_down.down.sql")
	if err := os.WriteFile(downPath, []byte("exists"), 0o644); err != nil {
		t.Fatalf("WriteFile down: %v", err)
	}

	if _, err := WriteMigrationDraft(dir, 1, "existing down", draft); err == nil {
		t.Fatal("expected WriteMigrationDraft to fail")
	}
	upPath := filepath.Join(dir, "000001.schema.existing_down.up.sql")
	if _, err := os.Stat(upPath); !os.IsNotExist(err) {
		t.Fatalf("expected up file not to be created, stat err=%v", err)
	}
}

func TestWriteMigrationDraftNoChange(t *testing.T) {
	if _, err := WriteMigrationDraft(t.TempDir(), 1, "noop", &MigrationDraft{}); !errors.Is(err, migrate.ErrNoChange) {
		t.Fatalf("expected ErrNoChange, got %v", err)
	}
}

func TestMigrationDraftWarningsForLimitedIntrospection(t *testing.T) {
	warnings := migrationDraftWarnings(dialect.New("postgres"), nil, nil, nil, nil)
	if len(warnings) != 1 || !strings.Contains(warnings[0], "foreign keys") || !strings.Contains(warnings[0], "partial indexes") {
		t.Fatalf("unexpected warnings: %#v", warnings)
	}
	if warnings := migrationDraftWarnings(dialect.New("sqlite"), nil, nil, nil, nil); len(warnings) != 0 {
		t.Fatalf("sqlite warnings = %#v, want none", warnings)
	}
}

func containsWarning(warnings []string, sub string) bool {
	for _, warning := range warnings {
		if strings.Contains(warning, sub) {
			return true
		}
	}
	return false
}

func containsStatement(statements []string, sub string) bool {
	for _, statement := range statements {
		if strings.Contains(statement, sub) {
			return true
		}
	}
	return false
}

func containsDrift(drifts schemadiff.Drifts, name string, detailSub string) bool {
	for _, drift := range drifts {
		if drift.Name == name && strings.Contains(drift.Detail, detailSub) {
			return true
		}
	}
	return false
}

func openDraftDatabase(t *testing.T) *gormdb.Database {
	t.Helper()

	gdb, err := gorm.Open(gormsqlite.Open("file:"+filepath.Join(t.TempDir(), "draft.db")+"?cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("gorm.Open: %v", err)
	}
	database, err := gormdb.New(gdb)
	if err != nil {
		t.Fatalf("gormdb.New: %v", err)
	}
	t.Cleanup(func() {
		_ = database.Close()
	})
	return database
}

type stubIntrospector struct {
	schema *dbschema.Schema
	calls  int
}

func (s *stubIntrospector) Inspect(context.Context, core.IDatabase) (*dbschema.Schema, error) {
	s.calls++
	return s.schema, nil
}
