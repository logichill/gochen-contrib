package gormmigrate

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gochen-runtime/db/migrate"
	dbschema "gochen-runtime/db/schema"
	schemadiff "gochen-runtime/db/schema/diff"
	"gochen-runtime/db/schema/introspect"
	"gochen-runtime/db/schema/render"
	"gochen/contextx"
	"gochen/db"
	"gochen/db/dialect"
	"gochen/errors"

	gormschema "gorm.io/gorm/schema"
)

// MigrationDraft 描述基于 GORM 模型生成的新增类 migration SQL 草稿。
type MigrationDraft struct {
	Current        *dbschema.Schema
	Desired        *dbschema.Schema
	Changes        schemadiff.Changes
	Drifts         schemadiff.Drifts
	Statements     []string
	DownStatements []string
	Warnings       []string
}

// MigrationFiles 描述已写入的 migration 草稿文件路径。
type MigrationFiles struct {
	UpPath   string
	DownPath string
}

// MigrationDraftOptions 配置 migration 草稿生成过程。
type MigrationDraftOptions struct {
	// Namer 指定 GORM 表/列命名策略；为空时使用 GORM 默认命名策略。
	Namer gormschema.Namer
	// Current 指定当前数据库结构快照；为空时通过 Introspector 或默认 introspect 读取数据库。
	Current *dbschema.Schema
	// Introspector 指定当前结构读取器；为空时使用 gochen 默认 introspector。
	Introspector introspect.IIntrospector
}

// GenerateMigrationDraft 比较当前数据库结构与 GORM 模型，并渲染新增类 SQL 草稿。
func GenerateMigrationDraft(ctx context.Context, database db.IDatabase, models ...any) (*MigrationDraft, error) {
	return GenerateMigrationDraftWithNamer(ctx, database, gormschema.NamingStrategy{}, models...)
}

// GenerateMigrationDraftWithNamer 使用指定 GORM 命名策略生成 migration SQL 草稿。
func GenerateMigrationDraftWithNamer(ctx context.Context, database db.IDatabase, namer gormschema.Namer, models ...any) (*MigrationDraft, error) {
	return GenerateMigrationDraftWithOptions(ctx, database, MigrationDraftOptions{Namer: namer}, models...)
}

// GenerateMigrationDraftWithOptions 使用自定义选项生成 migration SQL 草稿。
func GenerateMigrationDraftWithOptions(ctx context.Context, database db.IDatabase, opts MigrationDraftOptions, models ...any) (*MigrationDraft, error) {
	if database == nil {
		return nil, errors.NewCode(errors.InvalidInput, "database cannot be nil")
	}
	if ctx == nil {
		ctx = contextx.Background()
	}

	dbDialect := dialect.FromDatabase(database)
	if dbDialect.Name() == dialect.NameUnknown {
		return nil, errors.NewCode(errors.InvalidInput, "unknown database dialect")
	}
	desired, err := SchemaFromModelsWithNamerAndDialect(opts.Namer, dbDialect, models...)
	if err != nil {
		return nil, err
	}
	current := opts.Current
	if current == nil {
		inspector := opts.Introspector
		if inspector == nil {
			inspector = defaultIntrospector{}
		}
		var err error
		current, err = inspector.Inspect(ctx, database)
		if err != nil {
			return nil, errors.Wrap(err, errors.Database, "inspect current schema failed")
		}
	}

	changes := schemadiff.Between(current, desired)
	drifts := schemadiff.DetectDrifts(current, desired)
	renderChanges, renderWarnings := renderableMigrationChanges(dbDialect, changes)
	statements, err := render.RenderSQL(renderChanges, dbDialect)
	if err != nil {
		return nil, errors.Wrap(err, errors.InvalidInput, "render migration SQL failed")
	}
	downStatements, err := render.RenderDownSQL(renderChanges, dbDialect)
	if err != nil {
		return nil, errors.Wrap(err, errors.InvalidInput, "render migration down SQL failed")
	}

	warnings := migrationDraftWarnings(dbDialect, current, desired, changes, drifts)
	warnings = append(warnings, renderWarnings...)
	return &MigrationDraft{
		Current:        current,
		Desired:        desired,
		Changes:        renderChanges,
		Drifts:         drifts,
		Statements:     statements,
		DownStatements: downStatements,
		Warnings:       warnings,
	}, nil
}

type defaultIntrospector struct{}

func (defaultIntrospector) Inspect(ctx context.Context, database db.IDatabase) (*dbschema.Schema, error) {
	return introspect.Inspect(ctx, database)
}

// UpSQL 返回 migration up SQL 文本。
func (d *MigrationDraft) UpSQL() string {
	if d == nil {
		return ""
	}
	return formatMigrationSQL(d.Statements, "up", d.Warnings, false)
}

// DownSQL 返回带人工 review guard 的反向回滚 SQL 草稿。
func (d *MigrationDraft) DownSQL() string {
	if d == nil {
		return ""
	}
	warnings := append([]string{downReviewWarning}, d.Warnings...)
	return formatMigrationSQL(d.DownStatements, "down", warnings, true)
}

// WriteMigrationDraft 写入 runner 兼容的 .up.sql 和 .down.sql 文件。
func WriteMigrationDraft(dir string, version uint64, name string, draft *MigrationDraft) (*MigrationFiles, error) {
	if draft == nil {
		return nil, errors.NewCode(errors.InvalidInput, "migration draft cannot be nil")
	}
	if len(draft.Statements) == 0 {
		return nil, migrate.ErrNoChange
	}

	dir = strings.TrimSpace(dir)
	if dir == "" {
		return nil, errors.NewCode(errors.InvalidInput, "migration dir cannot be empty")
	}
	if version == 0 {
		return nil, errors.NewCode(errors.InvalidInput, "migration version cannot be zero")
	}
	fileName, err := migrationFileBase(version, name)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, errors.Wrap(err, errors.Dependency, "create migration dir failed").
			WithContext("dir", dir)
	}

	files := &MigrationFiles{
		UpPath:   filepath.Join(dir, fileName+".up.sql"),
		DownPath: filepath.Join(dir, fileName+".down.sql"),
	}
	if err := ensureNewFileAvailable(files.UpPath); err != nil {
		return nil, err
	}
	if err := ensureNewFileAvailable(files.DownPath); err != nil {
		return nil, err
	}
	if err := writeNewFile(files.UpPath, []byte(draft.UpSQL())); err != nil {
		return nil, err
	}
	if err := writeNewFile(files.DownPath, []byte(draft.DownSQL())); err != nil {
		_ = os.Remove(files.UpPath)
		return nil, err
	}
	return files, nil
}

func migrationFileBase(version uint64, name string) (string, error) {
	return typedMigrationFileBase(version, defaultDraftMigrationType, name)
}

func typedMigrationFileBase(version uint64, migrationType string, name string) (string, error) {
	migrationType = strings.TrimSpace(migrationType)
	if migrationType == "" {
		migrationType = defaultDraftMigrationType
	}
	if !migrationNamePattern.MatchString(migrationType) {
		return "", errors.NewCode(errors.InvalidInput, "migration type can only contain letters, numbers and underscore").
			WithContext("type", migrationType)
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return "", errors.NewCode(errors.InvalidInput, "migration name cannot be empty")
	}
	name = strings.ToLower(strings.ReplaceAll(name, "-", "_"))
	name = strings.Join(strings.Fields(name), "_")
	if !migrationNamePattern.MatchString(name) {
		return "", errors.NewCode(errors.InvalidInput, "migration name can only contain letters, numbers and underscore").
			WithContext("name", name)
	}
	return fmt.Sprintf("%06d.%s.%s", version, migrationType, name), nil
}

var migrationNamePattern = regexp.MustCompile(`^[a-z0-9_]+$`)

func writeNewFile(path string, content []byte) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return errors.Wrap(err, errors.Dependency, "write migration file failed").
			WithContext("path", path)
	}
	closed := false
	defer func() {
		if !closed {
			_ = file.Close()
		}
	}()

	if _, err := file.Write(content); err != nil {
		return errors.Wrap(err, errors.Dependency, "write migration file failed").
			WithContext("path", path)
	}
	if err := file.Sync(); err != nil {
		return errors.Wrap(err, errors.Dependency, "sync migration file failed").
			WithContext("path", path)
	}
	closed = true
	if err := file.Close(); err != nil {
		return errors.Wrap(err, errors.Dependency, "close migration file failed").
			WithContext("path", path)
	}
	return nil
}

const (
	downReviewWarning         = "MANUAL REVIEW REQUIRED: down SQL is a reverse draft and may drop tables, columns, or indexes."
	downReviewGuard           = migrate.ManualReviewGuardStatement
	defaultDraftMigrationType = "schema"
)

func migrationDraftWarnings(d dialect.IDialect, current *dbschema.Schema, desired *dbschema.Schema, changes schemadiff.Changes, drifts schemadiff.Drifts) []string {
	var warnings []string
	switch d.Name() {
	case dialect.NameMySQL, dialect.NamePostgres:
		warnings = append(warnings, "MySQL/Postgres introspection covers tables, columns, and simple column indexes only; review foreign keys, check constraints, expression/partial indexes, and complex constraints manually.")
	}
	if current != nil {
		warnings = append(warnings, current.Warnings...)
	}
	if desired != nil {
		warnings = append(warnings, desired.Warnings...)
	}
	warnings = append(warnings, drifts.Messages()...)
	warnings = append(warnings, additiveRiskWarnings(d, changes)...)
	return warnings
}

func additiveRiskWarnings(d dialect.IDialect, changes schemadiff.Changes) []string {
	var warnings []string
	for _, change := range changes {
		if change.Kind != schemadiff.KindAddColumn || change.Column == nil {
			continue
		}
		if isUnsafeNotNullAddColumn(change) && d.Name() != dialect.NameSQLite {
			warnings = append(warnings, fmt.Sprintf("Adding NOT NULL column %s.%s without a default may fail when the table already contains rows", change.Table, change.Column.Name))
		}
	}
	return warnings
}

func renderableMigrationChanges(d dialect.IDialect, changes schemadiff.Changes) (schemadiff.Changes, []string) {
	if d.Name() != dialect.NameSQLite {
		return changes, nil
	}

	renderable := make(schemadiff.Changes, 0, len(changes))
	skippedColumns := make(map[string]map[string]struct{})
	var warnings []string
	for _, change := range changes {
		if isSQLiteUnsafeAddColumn(change) {
			rememberSkippedColumn(skippedColumns, change.Table, change.Column.Name)
			warnings = append(warnings, fmt.Sprintf("Skipped SQLite ADD COLUMN for NOT NULL column %s.%s without a default; use a manual table rebuild migration for this column.", change.Table, change.Column.Name))
			continue
		}
		renderable = append(renderable, change)
	}

	if len(skippedColumns) == 0 {
		return renderable, warnings
	}
	// 复用 renderable 的底层数组，避免二次分配；此处只在本函数内返回过滤结果。
	filtered := renderable[:0]
	for _, change := range renderable {
		if indexDependsOnSkippedColumn(change, skippedColumns) {
			warnings = append(warnings, fmt.Sprintf("Skipped SQLite index %s on %s because it depends on a column omitted from the executable draft.", change.Index.Name, change.Table))
			continue
		}
		filtered = append(filtered, change)
	}
	return filtered, warnings
}

func isUnsafeNotNullAddColumn(change schemadiff.Change) bool {
	return change.Kind == schemadiff.KindAddColumn &&
		change.Column != nil &&
		!change.Column.Nullable &&
		strings.TrimSpace(change.Column.DefaultValue) == ""
}

func isSQLiteUnsafeAddColumn(change schemadiff.Change) bool {
	// 必须与 core renderAddColumn 的 SQLite 硬错误守卫保持同步；
	// 下游在这里提前过滤，只是为了生成可执行草稿并附带人工修复提示。
	return isUnsafeNotNullAddColumn(change)
}

func rememberSkippedColumn(skipped map[string]map[string]struct{}, table string, column string) {
	if skipped[table] == nil {
		skipped[table] = make(map[string]struct{})
	}
	skipped[table][column] = struct{}{}
}

func indexDependsOnSkippedColumn(change schemadiff.Change, skipped map[string]map[string]struct{}) bool {
	if change.Kind != schemadiff.KindAddIndex || change.Index == nil {
		return false
	}
	columns := skipped[change.Table]
	if len(columns) == 0 {
		return false
	}
	for _, column := range change.Index.Columns {
		if _, ok := columns[column]; ok {
			return true
		}
	}
	return false
}

func formatMigrationSQL(statements []string, direction string, warnings []string, guarded bool) string {
	var builder strings.Builder
	builder.WriteString("-- Generated migration ")
	builder.WriteString(direction)
	builder.WriteString(" SQL. Review before applying.\n\n")
	for _, warning := range warnings {
		warning = strings.TrimSpace(warning)
		if warning == "" {
			continue
		}
		builder.WriteString("-- ")
		builder.WriteString(warning)
		builder.WriteString("\n")
	}
	if len(warnings) > 0 {
		builder.WriteString("\n")
	}
	if guarded {
		builder.WriteString("-- Remove this guard only after reviewing the destructive down statements below.\n")
		builder.WriteString(downReviewGuard)
		builder.WriteString(";\n\n")
	}
	for _, statement := range statements {
		statement = strings.TrimSpace(statement)
		if statement == "" {
			continue
		}
		builder.WriteString(statement)
		if !strings.HasSuffix(statement, ";") {
			builder.WriteString(";")
		}
		builder.WriteString("\n\n")
	}
	return builder.String()
}

func ensureNewFileAvailable(path string) error {
	if _, err := os.Stat(path); err == nil {
		return errors.NewCode(errors.Conflict, "migration file already exists").
			WithContext("path", path)
	} else if !os.IsNotExist(err) {
		return errors.Wrap(err, errors.Dependency, "check migration file failed").
			WithContext("path", path)
	}
	return nil
}
