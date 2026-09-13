package gormorm

import (
	"gochen/contextx"
	"gochen/db/orm"
)

type session struct {
	Orm
	afterCommit contextx.IAfterCommitDispatcher
}

// Commit 提交当前事务。
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

var _ orm.IOrmSession = (*session)(nil)
