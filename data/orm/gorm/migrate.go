package gormorm

import (
	"context"

	"gochen-contrib/data/db/gorm"
	"gochen/contextx"
	"gochen/db"
	"gochen/errors"
)

// AutoMigrate 处理AutoMigrate。
func AutoMigrate(ctx context.Context, db db.IDatabase, models ...any) error {
	if db == nil {
		return errors.NewCode(errors.InvalidInput, "database cannot be nil")
	}
	gdb, ok := gormdb.DBOf(db)
	if !ok || gdb == nil {
		return errors.NewCode(errors.InvalidInput, "database is not backed by *gorm.DB")
	}
	if ctx == nil {
		ctx = contextx.Background()
	}
	if err := gdb.WithContext(ctx).AutoMigrate(models...); err != nil {
		return errors.Wrap(err, errors.Database, "auto migrate failed")
	}
	return nil
}
