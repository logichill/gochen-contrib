package gormmigrate

import (
	"fmt"
	"reflect"
	"sort"
	"strings"
	"sync"

	dbschema "gochen-runtime/db/schema"
	"gochen/db/dialect"
	"gochen/errors"

	gormschema "gorm.io/gorm/schema"
)

// SchemaFromModels 把 GORM 模型转换为 gochen 的中立 schema AST。
func SchemaFromModels(models ...any) (*dbschema.Schema, error) {
	return SchemaFromModelsWithNamer(gormschema.NamingStrategy{}, models...)
}

// SchemaFromModelsWithNamer 使用指定命名策略转换 GORM 模型。
func SchemaFromModelsWithNamer(namer gormschema.Namer, models ...any) (*dbschema.Schema, error) {
	return SchemaFromModelsWithNamerAndDialect(namer, dialect.New("sqlite"), models...)
}

// SchemaFromModelsWithDialect 使用方言感知的 SQL 类型映射转换 GORM 模型。
func SchemaFromModelsWithDialect(d dialect.IDialect, models ...any) (*dbschema.Schema, error) {
	return SchemaFromModelsWithNamerAndDialect(gormschema.NamingStrategy{}, d, models...)
}

// SchemaFromModelsWithNamerAndDialect 使用指定命名策略和 SQL 方言转换 GORM 模型。
func SchemaFromModelsWithNamerAndDialect(namer gormschema.Namer, d dialect.IDialect, models ...any) (*dbschema.Schema, error) {
	if namer == nil {
		namer = gormschema.NamingStrategy{}
	}

	cache := &sync.Map{}
	result := &dbschema.Schema{Tables: make([]dbschema.Table, 0, len(models))}
	for _, model := range models {
		if model == nil {
			return nil, errors.NewCode(errors.InvalidInput, "gorm model cannot be nil")
		}
		parsed, err := gormschema.Parse(model, cache, namer)
		if err != nil {
			return nil, errors.Wrap(err, errors.InvalidInput, "parse gorm model schema failed")
		}
		table, warnings := convertGormSchema(parsed, d)
		result.Tables = append(result.Tables, table)
		result.Warnings = append(result.Warnings, warnings...)
	}
	return result, nil
}

func convertGormSchema(parsed *gormschema.Schema, d dialect.IDialect) (dbschema.Table, []string) {
	table := dbschema.Table{
		Name:    parsed.Table,
		Columns: make([]dbschema.Column, 0, len(parsed.DBNames)),
	}
	var warnings []string

	for _, dbName := range parsed.DBNames {
		field := parsed.FieldsByDBName[dbName]
		if field == nil || field.DBName == "" || field.IgnoreMigration {
			continue
		}
		columnType := gormFieldType(field, d)
		table.Columns = append(table.Columns, dbschema.Column{
			Name:          field.DBName,
			Type:          columnType,
			Nullable:      !field.NotNull && !field.PrimaryKey,
			DefaultValue:  dbschema.NormalizeDefaultValue(columnType, gormDefaultValue(field), string(d.Name())),
			PrimaryKey:    field.PrimaryKey,
			AutoIncrement: field.AutoIncrement,
		})
	}

	indexesByName := make(map[string]dbschema.Index)
	for _, index := range parsed.ParseIndexes() {
		if index == nil {
			continue
		}
		if hasExpressionIndexField(index) {
			warnings = append(warnings, fmt.Sprintf("GORM expression index %s.%s requires manual migration review", parsed.Table, index.Name))
			continue
		}
		columns := make([]string, 0, len(index.Fields))
		for _, field := range index.Fields {
			if field.Field == nil || field.DBName == "" || field.IgnoreMigration {
				continue
			}
			columns = append(columns, field.DBName)
		}
		if len(columns) == 0 {
			continue
		}
		indexesByName[index.Name] = dbschema.Index{
			Name:    index.Name,
			Columns: columns,
			Unique:  strings.EqualFold(index.Class, "UNIQUE"),
		}
	}

	uniqueConstraints := parsed.ParseUniqueConstraints()
	uniqueNames := make([]string, 0, len(uniqueConstraints))
	for name := range uniqueConstraints {
		uniqueNames = append(uniqueNames, name)
	}
	sort.Strings(uniqueNames)
	for _, name := range uniqueNames {
		constraint := uniqueConstraints[name]
		if constraint.Field == nil || constraint.Field.DBName == "" || constraint.Field.IgnoreMigration {
			continue
		}
		if _, ok := indexesByName[name]; ok {
			continue
		}
		indexesByName[name] = dbschema.Index{
			Name:    name,
			Columns: []string{constraint.Field.DBName},
			Unique:  true,
		}
	}

	indexNames := make([]string, 0, len(indexesByName))
	for name := range indexesByName {
		indexNames = append(indexNames, name)
	}
	sort.Strings(indexNames)
	table.Indexes = make([]dbschema.Index, 0, len(indexNames))
	for _, name := range indexNames {
		table.Indexes = append(table.Indexes, indexesByName[name])
	}
	return table, warnings
}

func hasExpressionIndexField(index *gormschema.Index) bool {
	for _, field := range index.Fields {
		if strings.TrimSpace(field.Expression) != "" {
			return true
		}
	}
	return false
}

func gormFieldType(field *gormschema.Field, d dialect.IDialect) string {
	if field == nil {
		return ""
	}
	if typ := strings.TrimSpace(field.TagSettings["TYPE"]); typ != "" {
		return typ
	}

	switch field.DataType {
	case gormschema.Bool:
		return "BOOLEAN"
	case gormschema.Int, gormschema.Uint:
		return integerType(field, d)
	case gormschema.Float:
		return floatType(field)
	case gormschema.String:
		if field.Size > 0 {
			return fmt.Sprintf("VARCHAR(%d)", field.Size)
		}
		return "TEXT"
	case gormschema.Time:
		if d.Name() == dialect.NamePostgres {
			return "TIMESTAMPTZ"
		}
		return "DATETIME"
	case gormschema.Bytes:
		if d.Name() == dialect.NamePostgres {
			return "BYTEA"
		}
		return "BLOB"
	default:
		if field.DataType != "" {
			return strings.ToUpper(string(field.DataType))
		}
		if field.GORMDataType != "" {
			return strings.ToUpper(string(field.GORMDataType))
		}
		return "TEXT"
	}
}

func gormDefaultValue(field *gormschema.Field) string {
	if field == nil {
		return ""
	}
	tagDefault := strings.TrimSpace(field.TagSettings["DEFAULT"])
	if field.DefaultValue == "" && tagDefault != "" {
		return tagDefault
	}
	value := strings.TrimSpace(field.DefaultValue)
	if value == "" || !isStringField(field) || isSQLLiteralDefault(value) {
		return value
	}
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

func isStringField(field *gormschema.Field) bool {
	return field.DataType == gormschema.String ||
		field.GORMDataType == gormschema.String ||
		field.IndirectFieldType.Kind() == reflect.String
}

func isSQLLiteralDefault(value string) bool {
	upper := strings.ToUpper(value)
	if strings.HasPrefix(value, "'") ||
		strings.HasPrefix(value, "\"") ||
		strings.Contains(value, "(") ||
		upper == "NULL" ||
		upper == "TRUE" ||
		upper == "FALSE" ||
		upper == "CURRENT_DATE" ||
		upper == "CURRENT_TIME" ||
		upper == "CURRENT_TIMESTAMP" {
		return true
	}
	return false
}

func integerType(field *gormschema.Field, d dialect.IDialect) string {
	if field.PrimaryKey && field.AutoIncrement && d.Name() == dialect.NameSQLite {
		return "INTEGER"
	}
	unsigned := ""
	if d.Name() == dialect.NameMySQL && isUnsignedIntegerKind(field.IndirectFieldType.Kind()) {
		unsigned = " UNSIGNED"
	}
	switch field.IndirectFieldType.Kind() {
	case reflect.Int64, reflect.Uint64:
		return "BIGINT" + unsigned
	case reflect.Int8, reflect.Uint8, reflect.Int16, reflect.Uint16, reflect.Int32, reflect.Uint32:
		return "INTEGER" + unsigned
	default:
		return "INTEGER" + unsigned
	}
}

func isUnsignedIntegerKind(kind reflect.Kind) bool {
	switch kind {
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return true
	default:
		return false
	}
}

func floatType(field *gormschema.Field) string {
	if field.IndirectFieldType.Kind() == reflect.Float32 {
		return "FLOAT"
	}
	return "DOUBLE"
}
