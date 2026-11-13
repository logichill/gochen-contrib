package gogin_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	gormdb "gochen-contrib/data/db/gorm"
	gormorm "gochen-contrib/data/orm/gorm"
	gogin "gochen-contrib/http/gin"
	"gochen-runtime/api/rest"
	"gochen-runtime/db/orm/repo"
	"gochen-runtime/host"
	"gochen-runtime/host/authz"
	"gochen-runtime/host/config"
	"gochen-runtime/host/module"
	hostruntime "gochen-runtime/host/module/runtime"
	"gochen-runtime/http/nethttp"
	"gochen-runtime/quick"
	"gochen/app"
	"gochen/app/crud"
	"gochen/auth"
	domaincrud "gochen/domain/crud"
	"gochen/errors"
	"gochen/httpx"

	gormsqlite "github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type moduleNote struct {
	domaincrud.Entity[string]
	Title string `json:"title"`
}

type renameNote struct {
	ID              string `json:"-"`
	Title           string `json:"title"`
	ExpectedVersion uint64 `json:"expected_version"`
}

// noteRenamer 将业务动作直接建立在既有 CRUD 执行协议上。
type noteRenamer struct {
	application *crud.Application[*moduleNote, string]
}

func (r *noteRenamer) Execute(ctx context.Context, input renameNote) (*moduleNote, error) {
	return r.application.Execute(ctx, "rename", input.ID, func(_ context.Context, note *moduleNote) error {
		if note.Version != input.ExpectedVersion {
			return errors.NewCode(errors.Concurrency, "note version changed")
		}
		note.Title = input.Title
		return nil
	})
}
func (r *noteRenamer) AuthorizationBinding() (auth.EntryBinding, error) {
	return r.application.AuthorizationBinding("rename")
}

type moduleHTTPServer interface {
	httpx.IServer
	Handler() http.Handler
}

// 外部消费方能够直接引用完整事务和审计端口，无需导入 Core internal。
var (
	_ app.ITransactionRunner = (*repo.Repo[*moduleNote, string])(nil)
	_ app.IAuditRepository   = (*repo.AuditStore)(nil)
)

func TestTypedModuleGORMHTTPContract(t *testing.T) {
	for _, transport := range []string{"nethttp", "gin"} {
		t.Run(transport, func(t *testing.T) {
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
			if err := gdb.Exec(`CREATE TABLE notes (id TEXT PRIMARY KEY, version INTEGER NOT NULL, title TEXT NOT NULL)`).Error; err != nil {
				t.Fatal(err)
			}
			database, err := gormdb.New(gdb)
			if err != nil {
				t.Fatal(err)
			}
			engine, err := gormorm.NewFromDatabase(database)
			if err != nil {
				t.Fatal(err)
			}
			var application *crud.Application[*moduleNote, string]
			ctor := func() (module.IModule, error) {
				repository, err := repo.NewRepo[*moduleNote, string](engine, "notes", repo.WithResourceKind[*moduleNote, string]("note"))
				if err != nil {
					return nil, err
				}
				application, err = quick.DeclaredApp(repository,
					crud.Options[*moduleNote, string]{DeleteMode: crud.DeletePurge, Resource: "note", Limits: app.Limits{MaxPageSize: 10}, Actions: []auth.Action{{Name: "rename", Kind: auth.KindUpdate}}}, nil,
					auth.CompileOptions{Checker: authz.NewActionChecker()})
				if err != nil {
					return nil, err
				}
				readOptions := crud.Options[*moduleNote, string]{Resource: "note"}
				readDescriptor, err := crud.Describe(readOptions, false)
				if err != nil {
					return nil, err
				}
				readOptions.Authorizer, err = auth.Compile(auth.Declaration{Descriptor: readDescriptor, ResourceAccess: auth.ResourceAccessData},
					auth.CompileOptions{Metadata: repository.ExecutionDescriptor().Metadata(), Checker: authz.NewActionChecker()})
				if err != nil {
					return nil, err
				}
				readOnly, err := crud.NewReadApplication(repository, readOptions)
				if err != nil {
					return nil, err
				}
				rename := &noteRenamer{application: application}
				return host.Module("notes").Binding(
					rest.Resource[*moduleNote, string](application),
					rest.Resource[*moduleNote, string](readOnly, rest.WithBasePath[*moduleNote, string]("/browse")),
					rest.Bind(http.MethodPost, "/commands/:id/rename", rename, func(c httpx.IContext) (renameNote, error) {
						var input renameNote
						if err := c.BindJSON(&input); err != nil {
							return input, err
						}
						input.ID = c.Param("id")
						return input, nil
					}),
				).Build()
			}
			var server moduleHTTPServer = nethttp.NewServer(nil)
			if transport == "gin" {
				server, err = gogin.New(nil)
				if err != nil {
					t.Fatal(err)
				}
			}
			entries := authz.NewEntryRegistry()
			h := hostruntime.NewHost([]module.ModuleCtor{ctor}, config.WithHTTPServer(server),
				config.WithDisableHealthRoute(true), config.WithEntryValidator(entries), config.WithRouteMiddlewares(moduleIdentity))
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if err := h.Prepare(ctx); err != nil {
				t.Fatal(err)
			}
			if err := h.StartBackground(ctx); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				cancel()
				if err := h.Shutdown(context.Background()); err != nil {
					t.Error(err)
				}
				if err := h.Shutdown(context.Background()); err != nil {
					t.Error(err)
				}
				if err := sqlDB.Ping(); err != nil {
					t.Errorf("Host closed injected database: %v", err)
				}
			})
			network := httptest.NewServer(server.Handler())
			defer network.Close()
			client := network.Client()
			request := func(method, path, role, body string, status int) json.RawMessage {
				t.Helper()
				req, err := http.NewRequestWithContext(t.Context(), method, network.URL+"/api/v1/notes"+path, strings.NewReader(body))
				if err != nil {
					t.Fatal(err)
				}
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("X-Test-Role", role)
				response, err := client.Do(req)
				if err != nil {
					t.Fatal(err)
				}
				defer response.Body.Close()
				payload, err := io.ReadAll(response.Body)
				if err != nil {
					t.Fatal(err)
				}
				if response.StatusCode != status {
					t.Fatalf("%s %s=%d want %d: %s", method, path, response.StatusCode, status, payload)
				}
				var envelope struct {
					Data json.RawMessage `json:"data"`
				}
				if err := json.Unmarshal(payload, &envelope); err != nil {
					t.Fatal(err)
				}
				return envelope.Data
			}
			request("GET", "", "", "", 401)
			request("POST", "", "reader", `{"id":"denied","title":"forbidden"}`, 403)
			request("GET", "/denied", "reader", "", 404)
			request("POST", "", "editor", `{"id":"n1","title":"first"}`, 200)
			var note moduleNote
			if err := json.Unmarshal(request("GET", "/n1", "reader", "", 200), &note); err != nil {
				t.Fatal(err)
			}
			if note.Title != "first" || note.Version != 0 {
				t.Fatalf("created=%+v", note)
			}
			request("PUT", "/n1", "editor", `{"title":"second","version":0}`, 200)
			request("PUT", "/n1", "editor", `{"title":"stale","version":0}`, 409)
			if err := json.Unmarshal(request("GET", "/n1", "reader", "", 200), &note); err != nil {
				t.Fatal(err)
			}
			if note.Title != "second" || note.Version != 1 {
				t.Fatalf("updated=%+v", note)
			}
			request("POST", "/commands/n1/rename", "reader", `{"title":"denied","expected_version":1}`, 403)
			request("POST", "/commands/n1/rename", "editor", `{"title":"renamed","expected_version":1}`, 200)
			request("POST", "/commands/n1/rename", "editor", `{"title":"stale","expected_version":1}`, 409)
			if err := json.Unmarshal(request("GET", "/n1", "reader", "", 200), &note); err != nil {
				t.Fatal(err)
			}
			if note.Title != "renamed" || note.Version != 2 {
				t.Fatalf("command update=%+v", note)
			}
			request("GET", "", "reader", "", 200)
			request("GET", "/browse", "", "", 401)
			request("GET", "/browse/n1", "reader", "", 200)
			request("GET", "/browse", "reader", "", 200)
			for _, route := range server.(httpx.IRouteSnapshot).Routes() {
				if strings.Contains(route.Path, "/browse") && route.Method != http.MethodGet && route.Method != http.MethodHead && route.Method != http.MethodOptions {
					t.Fatalf("read-only binding exposed a write route: %+v", route)
				}
			}
			request("DELETE", "/n1", "editor", "", 200)
			request("GET", "/n1", "reader", "", 404)
			if len(entries.Report()) == 0 {
				t.Fatal("actual entries not checked")
			}
			for _, entry := range entries.Report() {
				if entry.Class == auth.EntryInvalid {
					t.Fatalf("invalid entry=%+v", entry)
				}
			}
			// 普通 Go 调用不能绕过应用授权。
			if _, err := application.List(context.Background(), 0, 10); err == nil {
				t.Fatal("direct call bypassed authorization")
			}
		})
	}
}

func moduleIdentity(c httpx.IContext, next func() error) error {
	role := c.Header("X-Test-Role")
	var permissions []string
	switch role {
	case "reader":
		permissions = []string{"note:api:read", "note:api:list"}
	case "editor":
		permissions = []string{"note:api:*"}
	default:
		return next()
	}
	ctx, err := authz.WithPrincipal(c.RequestContext(), authz.Principal{SubjectID: role, Permissions: permissions})
	if err != nil {
		return err
	}
	c.SetContext(c.RequestContext().WithContext(ctx))
	return next()
}
