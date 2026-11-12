package gormorm

import (
	"context"
	"testing"

	gormdb "gochen-contrib/data/db/gorm"
	"gochen-runtime/db/orm/repo"
	"gochen/db/orm"
	"gochen/testkit"
	"gochen/testkit/require"
)

func TestConstructorsExposeDatabaseAndSupportAuditAssembly(t *testing.T) {
	gdb := setupDB(t)
	connection, err := New(gdb)
	require.NoError(t, err)
	database, err := gormdb.New(gdb)
	require.NoError(t, err)
	explicit, err := NewFromDatabase(database)
	require.NoError(t, err)
	ctx := context.Background()
	_, err = database.Exec(ctx, `CREATE TABLE audit_records (
		id INTEGER PRIMARY KEY, resource_kind TEXT, entity_id TEXT, action TEXT,
		operator TEXT, initiator TEXT, reason TEXT, correlation_id TEXT, operation_id TEXT,
		timestamp DATETIME, changes TEXT, metadata TEXT, dimensions TEXT)`)
	require.NoError(t, err)
	for _, engine := range []*Orm{connection, explicit} {
		require.NotNil(t, engine.Database())
		require.Equal(t, connection.DatasourceID(), engine.DatasourceID())
		require.Equal(t, engine.Database(), engine.WithContext(ctx).Database())
		repository, err := repo.NewRepo[*txUser, int64](engine, "tx_users", repo.WithResourceKind[*txUser, int64]("user"))
		require.NoError(t, err)
		_, err = repo.NewAuditStore(engine, "audit_records", testkit.NewInt64Sequence(1), repository.AuditResource(nil))
		require.NoError(t, err)
		session, err := engine.BeginTx(ctx, nil)
		require.NoError(t, err)
		sourceID, ok := orm.DatasourceIDOf(session)
		require.True(t, ok)
		require.Equal(t, engine.DatasourceID(), sourceID)
		require.NoError(t, session.Rollback())
	}
	require.Equal(t, database, explicit.Database())
}
