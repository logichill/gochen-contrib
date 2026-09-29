package gormorm

import (
	"sync"

	"gochen/db/orm"
	"gochen/errors"

	"gorm.io/gorm"
	gormschema "gorm.io/gorm/schema"
)

// resolveModelMeta uses GORM's own schema parser to expose the concrete model
// fields required by gochen's fail-closed dynamic query validation.
func resolveModelMeta(db *gorm.DB, source *orm.ModelMeta) (*orm.ModelMeta, error) {
	if source == nil {
		return nil, errors.NewCode(errors.InvalidInput, "orm model meta cannot be nil")
	}
	if len(source.Fields) > 0 || source.ModelFactory == nil {
		return source, nil
	}

	model := source.NewModel()
	if model == nil {
		return source, nil
	}
	var namer gormschema.Namer = gormschema.NamingStrategy{}
	if db != nil && db.NamingStrategy != nil {
		namer = db.NamingStrategy
	}
	parsed, err := gormschema.Parse(model, &sync.Map{}, namer)
	if err != nil {
		return nil, errors.Wrap(err, errors.InvalidInput, "parse gorm model metadata failed")
	}

	indexesByField := make(map[string][]string)
	for _, index := range parsed.ParseIndexes() {
		if index == nil {
			continue
		}
		for _, indexField := range index.Fields {
			if indexField.Field == nil || indexField.DBName == "" {
				continue
			}
			indexesByField[indexField.DBName] = append(indexesByField[indexField.DBName], index.Name)
		}
	}

	fields := make([]orm.FieldMeta, 0, len(parsed.DBNames))
	for _, column := range parsed.DBNames {
		field := parsed.FieldsByDBName[column]
		if field == nil || field.DBName == "" {
			continue
		}
		fields = append(fields, orm.FieldMeta{
			Name:          field.Name,
			Column:        field.DBName,
			PrimaryKey:    field.PrimaryKey,
			AutoIncrement: field.AutoIncrement,
			Nullable:      !field.NotNull && !field.PrimaryKey,
			Unique:        field.Unique,
			Indexes:       append([]string(nil), indexesByField[field.DBName]...),
			DefaultValue:  field.DefaultValue,
		})
	}

	resolved := *source
	resolved.Fields = fields
	if resolved.Table == "" {
		resolved.Table = parsed.Table
	}
	return &resolved, nil
}
