package migration

import (
	"context"
	"fmt"

	"gochen-runtime/db/sql/safeident"
	"gochen/db/dialect"
	"gochen/errors"
)

// Drop 删除当前数据库中的全部非系统表。
//
// 该操作不会提供交互确认：调用方必须自行确保目标数据库正确。SQLite/MySQL
// 在固定连接上执行逐表删除，过程不保证原子性；PostgreSQL 使用 CASCADE，
// 可能同时删除依赖对象。CLI 默认会在调用前要求精确输入 yes。
func Drop(ctx context.Context, cfg Config) error {
	runner, err := NewRunner(ctx, cfg)
	if err != nil {
		return err
	}
	defer func() { _ = runner.Close() }()
	return runner.Drop(ctx)
}

// Drop 删除当前数据库中的全部非系统表。该操作逐表执行且可能受 CASCADE
// 影响，失败时可能已删除部分表；调用方应在隔离环境中显式使用。
func (r *Runner) Drop(ctx context.Context) error {
	if r == nil || r.database == nil || r.runner == nil {
		return errors.NewCode(errors.InvalidInput, "migration database is nil")
	}
	return r.runner.WithLock(ctx, func() error {
		d := dialect.FromDatabase(r.database)
		return withDropConnection(ctx, r.database, d, func(database dropDatabase) error {
			return dropTables(ctx, database, d, r.runner.StateTableName())
		})
	})
}

func dropTables(ctx context.Context, database dropDatabase, d dialect.IDialect, stateTable string) (err error) {
	tables, err := listTables(ctx, database, d, nil)
	if err != nil {
		return err
	}
	tables = moveTableToEnd(tables, stateTable)
	if len(tables) == 0 {
		return nil
	}
	if err := disableForeignKeys(ctx, database, d); err != nil {
		return errors.Wrap(err, errors.Database, "disable foreign keys failed")
	}
	defer func() {
		restoreCtx, cancel := foreignKeyRestoreContext(ctx)
		defer cancel()
		if restoreErr := enableForeignKeys(restoreCtx, database, d); restoreErr != nil {
			restoreErr = errors.Wrap(restoreErr, errors.Database, "enable foreign keys failed")
			err = errors.Join(err, restoreErr)
		}
	}()
	for _, table := range tables {
		if !safeident.IsSafeIdentifier(table) {
			return errors.NewCode(errors.InvalidInput, "unsafe table name").WithContext("table", table)
		}
		statement := fmt.Sprintf("DROP TABLE IF EXISTS %s", d.QuoteIdentifier(table))
		if d.Name() == dialect.NamePostgres {
			statement += " CASCADE"
		}
		if _, err := database.Exec(ctx, statement); err != nil {
			return errors.Wrap(err, errors.Database, "drop table failed").WithContext("table", table)
		}
	}
	return nil
}

func moveTableToEnd(tables []string, table string) []string {
	if table == "" || len(tables) < 2 {
		return tables
	}
	out := tables[:0]
	found := false
	for _, current := range tables {
		if current == table {
			found = true
			continue
		}
		out = append(out, current)
	}
	if found {
		out = append(out, table)
	}
	return out
}
