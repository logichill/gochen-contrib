package gormdb

import (
	"reflect"

	"gochen/db"

	"gorm.io/gorm"
)

// DBProvider 暴露底层 *gorm.DB，仅供 gorm 适配层使用。
type DBProvider interface {
	GormDB() *gorm.DB
}

// DBOf 从 gochen 抽象数据库中提取底层 *gorm.DB。
func DBOf(db db.IDatabase) (*gorm.DB, bool) {
	if isNil(db) {
		return nil, false
	}
	provider, ok := db.(DBProvider)
	if !ok {
		return nil, false
	}
	raw := provider.GormDB()
	return raw, raw != nil
}

func isNil(v any) bool {
	if v == nil {
		return true
	}
	val := reflect.ValueOf(v)
	switch val.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return val.IsNil()
	default:
		return false
	}
}
