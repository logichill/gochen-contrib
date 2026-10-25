package gormorm

import (
	"context"

	"gochen/contextx"
	"gochen/db/orm"
	"gochen/errors"
)

type session struct {
	Orm
	afterCommit contextx.IAfterCommitDispatcher
}

// Commit 提交事务。
func (s *session) Commit() error {
	if err := s.db.Commit().Error; err != nil {
		return err
	}
	if s.afterCommit == nil {
		return nil
	}
	if err := s.afterCommit.RunAfterCommit(); err != nil {
		return contextx.WrapAfterCommitError(err)
	}
	return nil
}

// Rollback 回滚当前事务。
func (s *session) Rollback() error { return s.db.Rollback().Error }

// AfterCommitDispatcher 暴露绑定在 session 上的提交后回调分发器。
func (s *session) AfterCommitDispatcher() contextx.IAfterCommitDispatcher {
	if s == nil {
		return nil
	}
	return s.afterCommit
}

// validateSavepointName 只允许字母/数字/下划线，防止拼接到 RELEASE 语句。
func validateSavepointName(name string) error {
	if name == "" {
		return errors.NewCode(errors.InvalidInput, "savepoint name is empty")
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_':
		default:
			return errors.NewCode(errors.InvalidInput, "savepoint name contains invalid characters").
				WithContext("name", name)
		}
	}
	return nil
}

// CreateSavepoint 实现 orm.ISavepointSession。
func (s *session) CreateSavepoint(ctx context.Context, name string) error {
	if err := validateSavepointName(name); err != nil {
		return err
	}
	return s.db.SavePoint(name).Error
}

// RollbackToSavepoint 实现 orm.ISavepointSession。
func (s *session) RollbackToSavepoint(ctx context.Context, name string) error {
	if err := validateSavepointName(name); err != nil {
		return err
	}
	return s.db.RollbackTo(name).Error
}

// ReleaseSavepoint 实现 orm.ISavepointSession。
// gorm 未提供释放保存点的 API，按方言通用语法直接执行。
func (s *session) ReleaseSavepoint(ctx context.Context, name string) error {
	if err := validateSavepointName(name); err != nil {
		return err
	}
	return s.db.Exec("RELEASE SAVEPOINT " + name).Error
}

var _ orm.IOrmSession = (*session)(nil)
var _ orm.ISavepointSession = (*session)(nil)
