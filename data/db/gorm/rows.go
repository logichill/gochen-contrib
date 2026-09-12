package gormdb

import (
	"database/sql"

	"gochen/errors"
)

type dbRows struct{ rows *sql.Rows }

// Next 推进到下一项并返回是否成功。
func (r *dbRows) Next() bool { return r.rows.Next() }

// Scan 把当前结果写入目标对象。
func (r *dbRows) Scan(dest ...any) error { return r.rows.Scan(dest...) }

// Close 关闭当前资源。
func (r *dbRows) Close() error { return r.rows.Close() }

// Err 处理Err。
func (r *dbRows) Err() error { return r.rows.Err() }

// Columns 处理Columns。
func (r *dbRows) Columns() ([]string, error) { return r.rows.Columns() }

// ColumnTypes 处理Column类型列表。
func (r *dbRows) ColumnTypes() ([]*sql.ColumnType, error) { return r.rows.ColumnTypes() }

type dbRow struct{ row *sql.Row }

// Scan 把当前结果写入目标对象。
func (r *dbRow) Scan(dest ...any) error { return r.row.Scan(dest...) }

// Err 处理Err。
func (r *dbRow) Err() error { return r.row.Err() }

type dbResult struct {
	rowsAffected int64
}

// LastInsertId 返回最后插入的主键。
func (r *dbResult) LastInsertId() (int64, error) {
	return 0, errors.NewCode(errors.Unsupported, "LastInsertId not supported")
}

// RowsAffected 返回受影响的行数。
func (r *dbResult) RowsAffected() (int64, error) { return r.rowsAffected, nil }
