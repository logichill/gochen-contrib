package gormorm

import (
	"context"
	"fmt"
	"testing"

	repopkg "gochen-runtime/db/orm/repo"
	"gochen/app/query"
	"gochen/contextx"
	"gochen/db"
	"gochen/db/orm"
	"gochen/errors"

	gormsqlite "github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

type user struct {
	ID   int64  `gorm:"primaryKey"`
	Name string `gorm:"column:name"`
}

type userWithProfiles struct {
	ID       int64         `gorm:"primaryKey"`
	Name     string        `gorm:"column:name"`
	Profiles []userProfile `gorm:"foreignKey:UserID"`
}

type userProfile struct {
	ID     int64  `gorm:"primaryKey"`
	UserID int64  `gorm:"column:user_id"`
	Label  string `gorm:"column:label"`
}

type txUser struct {
	ID      int64  `gorm:"primaryKey"`
	Version uint64 `gorm:"column:version"`
}

func (u *txUser) GetID() int64       { return u.ID }
func (u *txUser) GetVersion() uint64 { return u.Version }

type reservedColumnEntity struct {
	ID      int64  `gorm:"primaryKey"`
	Order   string `gorm:"column:order"`
	Version uint64 `gorm:"column:version"`
}

func (e *reservedColumnEntity) GetID() int64       { return e.ID }
func (e *reservedColumnEntity) GetVersion() uint64 { return e.Version }

func setupDB(t *testing.T) *gorm.DB {
	t.Helper()

	db, err := gorm.Open(
		gormsqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())),
		&gorm.Config{},
	)
	if err != nil {
		t.Fatalf("gorm.Open: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("gorm.DB: %v", err)
	}
	t.Cleanup(func() {
		if err := sqlDB.Close(); err != nil {
			t.Error(err)
		}
	})
	if err := db.AutoMigrate(&user{}); err != nil {
		t.Fatalf("AutoMigrate: %v", err)
	}
	return db
}

func TestOrm_BasicCRUD(t *testing.T) {
	db := setupDB(t)
	o, err := New(db)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	m, err := o.Model(&orm.ModelMeta{ModelFactory: orm.NewModelFactory[*user](), Table: "users"})
	if err != nil {
		t.Fatalf("Model: %v", err)
	}
	ctx := context.Background()

	u := &user{Name: "alice"}
	if err := m.Create(ctx, u); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if u.ID == 0 {
		t.Fatalf("expected auto ID assigned")
	}

	var got user
	if err := m.First(ctx, &got, orm.WithWhere("id = ?", u.ID)); err != nil {
		t.Fatalf("First: %v", err)
	}
	if got.Name != "alice" {
		t.Fatalf("expected alice, got %q", got.Name)
	}

	if err := m.UpdateValues(ctx, map[string]any{"name": "bob"}, orm.WithWhere("id = ?", u.ID)); err != nil {
		t.Fatalf("UpdateValues: %v", err)
	}
	if err := m.First(ctx, &got, orm.WithWhere("id = ?", u.ID)); err != nil {
		t.Fatalf("First after update: %v", err)
	}
	if got.Name != "bob" {
		t.Fatalf("expected bob, got %q", got.Name)
	}

	if err := m.Delete(ctx, orm.WithWhere("id = ?", u.ID)); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if err := m.First(ctx, &got, orm.WithWhere("id = ?", u.ID)); err == nil {
		t.Fatalf("expected not found")
	} else if errors.Code(err) != errors.NotFound {
		t.Fatalf("expected NOT_FOUND, got %v", err)
	}
}

func TestOrm_UpdateValuesHonorsSelect(t *testing.T) {
	for _, method := range []string{"UpdateValues", "UpdateValuesWithResult"} {
		t.Run(method, func(t *testing.T) {
			for _, tc := range []struct {
				name        string
				columns     []string
				wantVersion uint64
			}{
				{"single", []string{"order"}, 7},
				{"multiple", []string{"order", "version"}, 0},
				{"unrestricted", nil, 0},
			} {
				t.Run(tc.name, func(t *testing.T) {
					db := setupDB(t)
					if err := db.AutoMigrate(&reservedColumnEntity{}); err != nil {
						t.Fatalf("AutoMigrate: %v", err)
					}
					rows := []reservedColumnEntity{
						{ID: 1, Order: "before", Version: 7},
						{ID: 2, Order: "untouched", Version: 9},
					}
					if err := db.Create(&rows).Error; err != nil {
						t.Fatalf("Create: %v", err)
					}
					o, err := New(db)
					if err != nil {
						t.Fatalf("New: %v", err)
					}
					m, err := o.Model(&orm.ModelMeta{
						Table:        "reserved_column_entities",
						ModelFactory: orm.NewModelFactory[*reservedColumnEntity](),
					})
					if err != nil {
						t.Fatalf("Model: %v", err)
					}
					opts := []orm.QueryOption{orm.WithWhere("id = ?", 1), orm.WithSelect(tc.columns...)}
					values := map[string]any{"order": "after", "version": uint64(0)}
					if method == "UpdateValuesWithResult" {
						resultModel, ok := m.(orm.IModelWithResult)
						if !ok {
							t.Fatal("model does not expose result-aware writes")
						}
						result, err := resultModel.UpdateValuesWithResult(context.Background(), values, opts...)
						if err != nil {
							t.Fatalf("UpdateValuesWithResult: %v", err)
						}
						if affected, err := result.RowsAffected(); err != nil || affected != 1 {
							t.Fatalf("RowsAffected = %d, %v; want 1", affected, err)
						}
					} else if err := m.UpdateValues(context.Background(), values, opts...); err != nil {
						t.Fatalf("UpdateValues: %v", err)
					}
					var got []reservedColumnEntity
					if err := db.Order("id").Find(&got).Error; err != nil {
						t.Fatalf("Find updated rows: %v", err)
					}
					want := reservedColumnEntity{ID: 1, Order: "after", Version: tc.wantVersion}
					if len(got) != 2 || got[0] != want || got[1] != rows[1] {
						t.Fatalf("updated rows = %+v; want [%+v %+v]", got, want, rows[1])
					}
				})
			}
		})
	}
}

func TestOrm_SavePersistsZeroValues(t *testing.T) {
	db := setupDB(t)
	o, err := New(db)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	m, err := o.Model(&orm.ModelMeta{ModelFactory: orm.NewModelFactory[*user](), Table: "users"})
	if err != nil {
		t.Fatalf("Model: %v", err)
	}
	ctx := context.Background()
	u := &user{Name: "alice"}
	if err := m.Create(ctx, u); err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Cleanup(func() {
		_ = m.Delete(context.Background(), orm.WithWhere("id = ?", u.ID))
	})

	u.Name = ""
	if err := m.Save(ctx, u, orm.WithWhere("id = ?", u.ID)); err != nil {
		t.Fatalf("Save zero value: %v", err)
	}
	var got user
	if err := m.First(ctx, &got, orm.WithWhere("id = ?", u.ID)); err != nil {
		t.Fatalf("First after Save: %v", err)
	}
	if got.Name != "" {
		t.Fatalf("Save skipped zero value, got name %q", got.Name)
	}

	u.Name = "restored"
	if err := m.Save(ctx, u, orm.WithWhere("id = ?", u.ID)); err != nil {
		t.Fatalf("restore non-zero value: %v", err)
	}
	u.Name = ""
	resultModel, ok := m.(orm.IModelWithResult)
	if !ok {
		t.Fatal("model does not expose result-aware writes")
	}
	result, err := resultModel.SaveWithResult(ctx, u, orm.WithWhere("id = ?", u.ID))
	if err != nil {
		t.Fatalf("SaveWithResult zero value: %v", err)
	}
	if affected, err := result.RowsAffected(); err != nil || affected != 1 {
		t.Fatalf("SaveWithResult rows = %d, %v", affected, err)
	}
	if err := m.First(ctx, &got, orm.WithWhere("id = ?", u.ID)); err != nil {
		t.Fatalf("First after SaveWithResult: %v", err)
	}
	if got.Name != "" {
		t.Fatalf("SaveWithResult skipped zero value, got name %q", got.Name)
	}
}

func TestOrm_SaveDoesNotPersistLoadedAssociations(t *testing.T) {
	db := setupDB(t)
	if err := db.AutoMigrate(&userWithProfiles{}, &userProfile{}); err != nil {
		t.Fatalf("AutoMigrate associations: %v", err)
	}
	account := &userWithProfiles{Name: "alice", Profiles: []userProfile{{Label: "original"}}}
	if err := db.Create(account).Error; err != nil {
		t.Fatalf("create associated user: %v", err)
	}
	var loaded userWithProfiles
	if err := db.Preload("Profiles").First(&loaded, account.ID).Error; err != nil {
		t.Fatalf("load associated user: %v", err)
	}
	loaded.Name = ""
	loaded.Profiles[0].Label = "must-not-be-saved"

	o, err := New(db)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	m, err := o.Model(&orm.ModelMeta{ModelFactory: orm.NewModelFactory[*userWithProfiles](), Table: "user_with_profiles"})
	if err != nil {
		t.Fatalf("Model: %v", err)
	}
	if err := m.Save(context.Background(), &loaded, orm.WithWhere("id = ?", loaded.ID)); err != nil {
		t.Fatalf("Save: %v", err)
	}

	var profile userProfile
	if err := db.First(&profile, loaded.Profiles[0].ID).Error; err != nil {
		t.Fatalf("reload profile: %v", err)
	}
	if profile.Label != "original" {
		t.Fatalf("Save persisted association label %q", profile.Label)
	}
	var saved userWithProfiles
	if err := db.First(&saved, loaded.ID).Error; err != nil {
		t.Fatalf("reload user: %v", err)
	}
	if saved.Name != "" {
		t.Fatalf("Save skipped scalar zero value, got %q", saved.Name)
	}
}

func TestOrm_SaveWithResultDoesNotPersistLoadedAssociations(t *testing.T) {
	db := setupDB(t)
	if err := db.AutoMigrate(&userWithProfiles{}, &userProfile{}); err != nil {
		t.Fatalf("AutoMigrate associations: %v", err)
	}
	account := &userWithProfiles{Name: "alice", Profiles: []userProfile{{Label: "original"}}}
	if err := db.Create(account).Error; err != nil {
		t.Fatalf("create associated user: %v", err)
	}
	var loaded userWithProfiles
	if err := db.Preload("Profiles").First(&loaded, account.ID).Error; err != nil {
		t.Fatalf("load associated user: %v", err)
	}
	loaded.Name = ""
	loaded.Profiles[0].Label = "must-not-be-saved"

	o, err := New(db)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	m, err := o.Model(&orm.ModelMeta{ModelFactory: orm.NewModelFactory[*userWithProfiles](), Table: "user_with_profiles"})
	if err != nil {
		t.Fatalf("Model: %v", err)
	}
	resultModel, ok := m.(orm.IModelWithResult)
	if !ok {
		t.Fatal("model does not expose result-aware writes")
	}
	result, err := resultModel.SaveWithResult(context.Background(), &loaded, orm.WithWhere("id = ?", loaded.ID))
	if err != nil {
		t.Fatalf("SaveWithResult: %v", err)
	}
	if affected, err := result.RowsAffected(); err != nil || affected != 1 {
		t.Fatalf("SaveWithResult rows = %d, %v", affected, err)
	}

	var profile userProfile
	if err := db.First(&profile, loaded.Profiles[0].ID).Error; err != nil {
		t.Fatalf("reload profile: %v", err)
	}
	if profile.Label != "original" {
		t.Fatalf("SaveWithResult persisted association label %q", profile.Label)
	}
	var saved userWithProfiles
	if err := db.First(&saved, loaded.ID).Error; err != nil {
		t.Fatalf("reload user: %v", err)
	}
	if saved.Name != "" {
		t.Fatalf("SaveWithResult skipped scalar zero value, got %q", saved.Name)
	}
}

func TestOrm_OrderByAndGroupByMustBeSafeIdentifiers(t *testing.T) {
	db := setupDB(t)
	o, err := New(db)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	m, err := o.Model(&orm.ModelMeta{ModelFactory: orm.NewModelFactory[*user](), Table: "users"})
	if err != nil {
		t.Fatalf("Model: %v", err)
	}

	var got []user
	if err := m.Find(context.Background(), &got, orm.WithOrderBy("name desc; drop table users", false)); err == nil {
		t.Fatalf("expected error")
	} else if errors.Code(err) != errors.InvalidInput {
		t.Fatalf("expected INVALID_INPUT, got %v", err)
	}

	if err := m.Find(context.Background(), &got, orm.WithGroupBy("name, id")); err == nil {
		t.Fatalf("expected error")
	} else if errors.Code(err) != errors.InvalidInput {
		t.Fatalf("expected INVALID_INPUT, got %v", err)
	}
}

func TestOrmRepoQueryQuotesReservedFilterColumn(t *testing.T) {
	db := setupDB(t)
	if err := db.AutoMigrate(&reservedColumnEntity{}); err != nil {
		t.Fatalf("AutoMigrate reserved column entity: %v", err)
	}
	if err := db.Create(&reservedColumnEntity{Order: "first"}).Error; err != nil {
		t.Fatalf("create reserved column entity: %v", err)
	}

	o, err := New(db)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	repository, err := repopkg.NewRepo[*reservedColumnEntity, int64](o, "reserved_column_entities")
	if err != nil {
		t.Fatalf("NewRepo: %v", err)
	}
	var filters query.QueryFilters
	filters = filters.Append("Order", query.QueryExpr{
		Op:    query.FilterOpEq,
		Value: query.StringValue("first"),
	})

	got, err := repository.Query(context.Background(), query.QueryOptions{Filters: filters})
	if err != nil {
		t.Fatalf("Query reserved column: %v", err)
	}
	if len(got) != 1 || got[0].Order != "first" {
		t.Fatalf("Query reserved column result = %#v, want one matching row", got)
	}
}

func TestOrm_SelectRawExpressionsAreApplied(t *testing.T) {
	db := setupDB(t)
	o, err := New(db)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	m, err := o.Model(&orm.ModelMeta{ModelFactory: orm.NewModelFactory[*user](), Table: "users"})
	if err != nil {
		t.Fatalf("Model: %v", err)
	}
	ctx := context.Background()

	for _, name := range []string{"alice", "alice", "bob"} {
		if err := m.Create(ctx, &user{Name: name}); err != nil {
			t.Fatalf("Create %q: %v", name, err)
		}
	}

	var rows []struct {
		Name  string `gorm:"column:name"`
		Count int64  `gorm:"column:count"`
	}
	if err := m.Find(ctx, &rows,
		orm.WithSelect("name"),
		orm.WithSelectExprUnsafe("COUNT(*) AS count"),
		orm.WithGroupBy("name"),
		orm.WithOrderBy("name", false),
	); err != nil {
		t.Fatalf("Find: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 grouped rows, got %d: %+v", len(rows), rows)
	}
	if rows[0].Name != "alice" || rows[0].Count != 2 {
		t.Fatalf("unexpected first row: %+v", rows[0])
	}
	if rows[1].Name != "bob" || rows[1].Count != 1 {
		t.Fatalf("unexpected second row: %+v", rows[1])
	}
}

func TestSessionBoundRepo_ManualCommitRunsAfterCommitCallbacks(t *testing.T) {
	db := setupDB(t)
	o, err := New(db)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	session, err := o.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("BeginTx: %v", err)
	}

	repository, err := repopkg.NewRepo[*txUser, int64](session, "users")
	if err != nil {
		t.Fatalf("NewRepo: %v", err)
	}
	if repository == nil {
		t.Fatal("expected repository")
	}

	txCtx, err := orm.WithTxSession(context.Background(), session, false)
	if err != nil {
		t.Fatalf("WithTxSession: %v", err)
	}
	called := 0
	if err := contextx.AppendAfterCommit(txCtx, func(context.Context) error {
		called++
		return nil
	}); err != nil {
		t.Fatalf("AppendAfterCommit: %v", err)
	}
	if err := session.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if called != 1 {
		t.Fatalf("expected after-commit callback once, got %d", called)
	}
}

func TestBeginTxSession_ExposesTransactionDatabase(t *testing.T) {
	db := setupDB(t)
	o, err := New(db)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	session, err := o.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("BeginTx: %v", err)
	}

	txDB := session.Database()
	if txDB == nil {
		t.Fatal("expected transaction session to expose database")
	}
	if _, err := txDB.Exec(context.Background(), "INSERT INTO users (name) VALUES (?)", "tx-visible"); err != nil {
		t.Fatalf("tx exec: %v", err)
	}
	if err := session.Rollback(); err != nil {
		t.Fatalf("Rollback: %v", err)
	}

	var count int64
	if err := db.Model(&user{}).Where("name = ?", "tx-visible").Count(&count).Error; err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected transaction database writes to roll back, got %d rows", count)
	}
}

func TestBeginTxSession_DatabaseCloseDoesNotCloseRootDB(t *testing.T) {
	db := setupDB(t)
	o, err := New(db)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	session, err := o.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("BeginTx: %v", err)
	}
	txDB := session.Database()
	if txDB == nil {
		t.Fatal("expected transaction session to expose database")
	}
	if err := txDB.Close(); err != nil {
		t.Fatalf("transaction database Close: %v", err)
	}
	if err := session.Rollback(); err != nil {
		t.Fatalf("Rollback: %v", err)
	}
	if err := db.Exec("INSERT INTO users (name) VALUES (?)", "root-still-open").Error; err != nil {
		t.Fatalf("expected root db to remain open after transaction database Close, got %v", err)
	}
}

func TestOrm_NamingConvention(t *testing.T) {
	gdb := setupDB(t)
	o, err := New(gdb)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ncProvider, ok := any(o).(db.INamingConventionProvider)
	if !ok {
		t.Fatal("expected Orm to implement db.INamingConventionProvider")
	}

	nc := ncProvider.NamingConvention()
	if nc.TenantColumn != "tenant_id" || nc.VersionColumn != "version" {
		t.Fatalf("unexpected default naming convention: %+v", nc)
	}
}

func TestOrm_JoinWithReservedTableAndMultipleOn(t *testing.T) {
	db := setupDB(t)
	// 创建保留字表 "group"
	if err := db.Exec(`CREATE TABLE "group" (id INTEGER PRIMARY KEY, tenant_id INTEGER, name TEXT)`).Error; err != nil {
		t.Fatalf("create group table: %v", err)
	}
	if err := db.Exec(`ALTER TABLE users ADD COLUMN tenant_id INTEGER`).Error; err != nil {
		t.Fatalf("alter users table: %v", err)
	}
	if err := db.Exec(`INSERT INTO "group" (id, tenant_id, name) VALUES (1, 100, 'dev-group')`).Error; err != nil {
		t.Fatalf("insert group: %v", err)
	}
	if err := db.Exec(`INSERT INTO users (id, name, tenant_id) VALUES (1, 'alice', 100)`).Error; err != nil {
		t.Fatalf("insert user: %v", err)
	}

	o, err := New(db)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	m, err := o.Model(&orm.ModelMeta{Table: "users"})
	if err != nil {
		t.Fatalf("Model: %v", err)
	}

	var results []struct {
		UserID    int64  `gorm:"column:id"`
		UserName  string `gorm:"column:name"`
		GroupName string `gorm:"column:group_name"`
	}

	// 测试：JOIN 保留字表 "group"，且包含多个 ON 条件（引用首个与后续条件），同时混合 SelectRaw
	err = m.Find(context.Background(), &results,
		orm.WithJoin(orm.Join{
			Type:  orm.JoinLeft,
			Table: "group",
			On: []orm.JoinOn{
				{Left: "users.id", Right: "group.id"},
				{Left: "users.tenant_id", Right: "group.tenant_id"},
			},
		}),
		orm.WithSelect("users.id", "users.name"),
		orm.WithSelectExprUnsafe(`"group"."name" AS group_name`),
	)
	if err != nil {
		t.Fatalf("Find with join on reserved table and multi-on: %v", err)
	}
	if len(results) != 1 || results[0].GroupName != "dev-group" {
		t.Fatalf("unexpected join results: %+v", results)
	}

	// 测试：以保留字 "group" 作为主表 Model
	gm, err := o.Model(&orm.ModelMeta{Table: "group"})
	if err != nil {
		t.Fatalf("Model with reserved table: %v", err)
	}
	var groupList []struct {
		ID   int64  `gorm:"column:id"`
		Name string `gorm:"column:name"`
	}
	if err := gm.Find(context.Background(), &groupList); err != nil {
		t.Fatalf("Find on reserved primary table: %v", err)
	}
	if len(groupList) != 1 || groupList[0].Name != "dev-group" {
		t.Fatalf("unexpected group list: %+v", groupList)
	}
}

func TestOrm_CountContractBehavior(t *testing.T) {
	db := setupDB(t)
	if err := db.Exec(`ALTER TABLE users ADD COLUMN category TEXT`).Error; err != nil {
		t.Fatalf("alter table: %v", err)
	}
	if err := db.Exec(`ALTER TABLE users ADD COLUMN nullable_note TEXT`).Error; err != nil {
		t.Fatalf("alter table: %v", err)
	}
	// 插入 3 条数据，分属 2 个 category；其中部分列为 NULL
	if err := db.Exec(`INSERT INTO users (id, name, category, nullable_note) VALUES (1, 'u1', 'cat-A', NULL)`).Error; err != nil {
		t.Fatalf("insert: %v", err)
	}
	if err := db.Exec(`INSERT INTO users (id, name, category, nullable_note) VALUES (2, 'u2', 'cat-A', 'note-2')`).Error; err != nil {
		t.Fatalf("insert: %v", err)
	}
	if err := db.Exec(`INSERT INTO users (id, name, category, nullable_note) VALUES (3, 'u3', 'cat-B', NULL)`).Error; err != nil {
		t.Fatalf("insert: %v", err)
	}

	o, err := New(db)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	m, err := o.Model(&orm.ModelMeta{Table: "users"})
	if err != nil {
		t.Fatalf("Model: %v", err)
	}
	ctx := context.Background()

	// 1. 3 行数据加 Offset(1)，Count 仍必须返回 3（不受分页影响）
	c1, err := m.Count(ctx, orm.WithOffset(1))
	if err != nil {
		t.Fatalf("Count with offset: %v", err)
	}
	if c1 != 3 {
		t.Fatalf("Count with offset: got %d, want 3", c1)
	}

	// 2. 选择含 NULL 值的列，Count 仍返回全部 3 行
	c2, err := m.Count(ctx, orm.WithSelect("nullable_note"))
	if err != nil {
		t.Fatalf("Count with nullable select: %v", err)
	}
	if c2 != 3 {
		t.Fatalf("Count with nullable select: got %d, want 3", c2)
	}

	// 3. 2 个分组加 Limit(1)，Count 必须返回组数 2（不受 Limit(1) 影响）
	c3, err := m.Count(ctx, orm.WithGroupBy("category"), orm.WithLimit(1))
	if err != nil {
		t.Fatalf("Count with groupby & limit: %v", err)
	}
	if c3 != 2 {
		t.Fatalf("Count with groupby & limit: got %d, want 2", c3)
	}
}
