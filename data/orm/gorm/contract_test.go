package gormorm

import (
	"context"
	"testing"
	"time"

	gormdb "gochen-contrib/data/db/gorm"
	"gochen-runtime/db/orm/contracttest"
	"gochen/db/orm"

	gormsqlite "github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestORMContract(t *testing.T) {
	contracttest.Run(t, func(t *testing.T) orm.IOrm {
		gdb, err := gorm.Open(gormsqlite.Open(":memory:"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
		if err != nil {
			t.Fatal(err)
		}
		sqlDB, err := gdb.DB()
		if err != nil {
			t.Fatal(err)
		}
		sqlDB.SetMaxOpenConns(1)
		t.Cleanup(func() {
			if err := sqlDB.Close(); err != nil {
				t.Error(err)
			}
		})
		database, err := gormdb.New(gdb)
		if err != nil {
			t.Fatal(err)
		}
		adapter, err := NewFromDatabase(database)
		if err != nil {
			t.Fatal(err)
		}
		return adapter
	})
}

type panicCreateRecord struct {
	ID   int64 `gorm:"primaryKey"`
	Name string
}

func (r *panicCreateRecord) BeforeCreate(*gorm.DB) error {
	if r.Name == "panic" {
		panic("create hook panic")
	}
	return nil
}

func TestModel_CreatePanicRollsBackBatch(t *testing.T) {
	for _, skipDefaultTx := range []bool{false, true} {
		t.Run(map[bool]string{false: "default", true: "skip_default_transaction"}[skipDefaultTx], func(t *testing.T) {
			gdb, err := gorm.Open(gormsqlite.Open(":memory:"), &gorm.Config{
				Logger: logger.Default.LogMode(logger.Silent), SkipDefaultTransaction: skipDefaultTx,
			})
			if err != nil {
				t.Fatal(err)
			}
			sqlDB, err := gdb.DB()
			if err != nil {
				t.Fatal(err)
			}
			sqlDB.SetMaxOpenConns(1)
			t.Cleanup(func() {
				if err := sqlDB.Close(); err != nil {
					t.Error(err)
				}
			})
			if err := gdb.AutoMigrate(&panicCreateRecord{}); err != nil {
				t.Fatal(err)
			}
			adapter, err := New(gdb)
			if err != nil {
				t.Fatal(err)
			}
			model, err := adapter.Model(&orm.ModelMeta{ModelFactory: orm.NewModelFactory[*panicCreateRecord]()})
			if err != nil {
				t.Fatal(err)
			}
			func() {
				defer func() {
					if got := recover(); got != "create hook panic" {
						t.Fatalf("panic = %v, want create hook panic", got)
					}
				}()
				_ = model.Create(context.Background(), &panicCreateRecord{ID: 1, Name: "first"}, &panicCreateRecord{ID: 2, Name: "panic"})
			}()
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			count, err := model.Count(ctx)
			if err != nil || count != 0 {
				t.Fatalf("partial create survived panic: count=%d err=%v", count, err)
			}
		})
	}
}
