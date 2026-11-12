package gogin_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	gogin "gochen-contrib/http/gin"
	"gochen-runtime/api/rest"
	"gochen-runtime/http/nethttp"
	appaudit "gochen/app/audit"
	appcrud "gochen/app/crud"
	"gochen/app/deletion"
	"gochen/auth"
	"gochen/contextx"
	"gochen/domain"
	domaincrud "gochen/domain/crud"
	"gochen/httpx"
	"gochen/testkit"
)

type capabilityNote struct {
	domaincrud.Entity[int64]
	domain.DeletionState
	Title string `json:"title"`
}

func TestRESTExplicitCapabilitiesAcrossTransports(t *testing.T) {
	for _, transport := range []string{"nethttp", "gin"} {
		t.Run(transport, func(t *testing.T) {
			must := func(err error) {
				t.Helper()
				if err != nil {
					t.Fatal(err)
				}
			}
			repository, err := testkit.NewAuditedMemoryRepository[*capabilityNote, int64](testkit.NewInt64Sequence(1))
			must(err)
			application, err := appcrud.NewApplication[*capabilityNote, int64](repository, appcrud.Options[*capabilityNote, int64]{Resource: "note", Authorizer: auth.Allow(), Audit: repository})
			must(err)
			trash, err := deletion.NewApplication[*capabilityNote, int64](repository, deletion.Options[*capabilityNote, int64]{Options: appcrud.Options[*capabilityNote, int64]{Resource: "note", Authorizer: auth.Allow(), Audit: repository}})
			must(err)
			history, err := appaudit.NewReader[int64](repository, repository, appaudit.Options{Resource: "note", Authorizer: auth.Allow()})
			must(err)
			wrong, err := appaudit.NewReader[int64](repository, repository, appaudit.Options{Resource: "other", Authorizer: auth.Allow()})
			must(err)
			extract := rest.WithOperatorExtractor[*capabilityNote, int64](func(httpx.IContext) (string, bool) { return "alice", true })
			// 失败发生于构造阶段，不留下部分 HTTP 路由。
			for name, options := range map[string][]rest.Option[*capabilityNote, int64]{
				"wrong resource":  {extract, rest.WithAudit[*capabilityNote, int64](wrong)},
				"nil audit":       {extract, rest.WithAudit[*capabilityNote, int64](nil)},
				"duplicate audit": {extract, rest.WithAudit[*capabilityNote, int64](history), rest.WithAudit[*capabilityNote, int64](history)},
				"nil deletion":    {extract, rest.WithDeletion[*capabilityNote, int64](nil)},
			} {
				if _, err := rest.NewRouteBuilder(application, options...); err == nil {
					t.Errorf("%s accepted", name)
				}
			}
			var server moduleHTTPServer = nethttp.NewServer(nil)
			if transport == "gin" {
				server, err = gogin.New(nil)
				must(err)
			}
			must(rest.Mount(server.Group("/notes"), application, extract, rest.WithAudit[*capabilityNote, int64](history), rest.WithDeletion[*capabilityNote, int64](trash)))
			ctx, err := contextx.WithOperator(context.Background(), "alice")
			must(err)
			must(application.Create(ctx, &capabilityNote{Title: "note"}))
			for _, request := range []struct{ method, path string }{
				{http.MethodGet, "/notes/1/audit"},
				{http.MethodDelete, "/notes/1"},
				{http.MethodGet, "/notes/deleted"},
				{http.MethodPost, "/notes/1/restore"},
				{http.MethodDelete, "/notes/1"},
				{http.MethodDelete, "/notes/1/purge"},
				{http.MethodGet, "/notes/1/audit"},
			} {
				response := httptest.NewRecorder()
				server.Handler().ServeHTTP(response, httptest.NewRequest(request.method, request.path, strings.NewReader("")))
				if response.Code != http.StatusOK {
					t.Fatalf("%s %s: %d %s", request.method, request.path, response.Code, response.Body.String())
				}
			}
		})
	}
}
