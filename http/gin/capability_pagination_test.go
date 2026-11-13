package gogin_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	gogin "gochen-contrib/http/gin"
	"gochen-runtime/api/rest"
	"gochen-runtime/http/nethttp"
	"gochen/app"
	appcrud "gochen/app/crud"
	appaudit "gochen/app/crud/audit"
	"gochen/audit"
	"gochen/auth"
	"gochen/contextx"
	"gochen/testkit"
	"gochen/testkit/require"
)

func TestRESTCapabilityPaginationAcrossTransports(t *testing.T) {
	for _, transport := range []string{"nethttp", "gin"} {
		for _, tc := range []struct {
			name               string
			read, audit, route int
		}{
			{name: "smaller capabilities", read: 10, audit: 1, route: 10},
			{name: "smaller read application", read: 1, audit: 2, route: 10},
			{name: "smaller route limit", read: 10, audit: 3, route: 1},
		} {
			t.Run(transport+"/"+tc.name, func(t *testing.T) {
				ctx, err := contextx.WithOperator(t.Context(), "alice")
				require.NoError(t, err)
				repository, err := testkit.NewAuditedMemoryRepository[*capabilityNote, int64](testkit.NewInt64Sequence(1))
				require.NoError(t, err)
				options := appcrud.Options[*capabilityNote, int64]{Resource: "note", Authorizer: auth.Allow(), Audit: repository, DeleteMode: appcrud.DeleteSoft, EnableDeletedQuery: true, Limits: app.Limits{MaxPageSize: tc.read}}
				application, err := appcrud.NewApplication(repository, options)
				require.NoError(t, err)
				history, err := appaudit.NewReader[int64](repository, repository, appaudit.Options{Resource: "note", Authorizer: auth.Allow(), Limits: app.Limits{MaxPageSize: tc.audit}})
				require.NoError(t, err)
				for i := 1; i <= 5; i++ {
					entity := &capabilityNote{Title: fmt.Sprintf("note %d", i)}
					require.NoError(t, application.Create(ctx, entity))
					if i == 1 {
						for update := 0; update < 4; update++ {
							entity.Title = fmt.Sprintf("updated %d", update)
							require.NoError(t, application.Update(ctx, entity))
						}
					}
					require.NoError(t, application.Delete(ctx, entity.GetID()))
				}
				baselineHistory, err := appaudit.NewReader[int64](repository, repository, appaudit.Options{Resource: "note", Authorizer: auth.Allow(), Limits: app.Limits{MaxPageSize: 100}})
				require.NoError(t, err)
				allRecords, err := baselineHistory.AuditTrail(ctx, 1, 0, 100)
				require.NoError(t, err)
				require.Len(t, allRecords, 6)
				options.Limits.MaxPageSize = 100
				baselineTrash, err := appcrud.NewApplication(repository, options)
				require.NoError(t, err)
				allDeleted, err := baselineTrash.ListDeleted(ctx, 0, 100)
				require.NoError(t, err)
				require.Len(t, allDeleted, 5)

				var server moduleHTTPServer = nethttp.NewServer(nil)
				if transport == "gin" {
					server, err = gogin.New(nil)
					require.NoError(t, err)
				}
				require.NoError(t, rest.Mount(server.Group("/notes"), application,
					rest.WithAudit[*capabilityNote, int64](history), rest.WithRoutes[*capabilityNote, int64](rest.OpList, rest.OpAuditRead, rest.OpListDeleted),
					rest.WithPagination[*capabilityNote, int64](3, tc.route)))

				for _, size := range []string{"size=3", "page_size=3", ""} {
					var auditIDs, deletedIDs []int64
					auditSize := min(3, tc.audit, tc.route)
					deletedSize := min(3, tc.read, tc.route)
					for page := 1; page <= (len(allRecords)+auditSize-1)/auditSize+1; page++ {
						response := httptest.NewRecorder()
						path := fmt.Sprintf("/notes/1/audit?page=%d&%s", page, size)
						server.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil).WithContext(ctx))
						require.Equalf(t, http.StatusOK, response.Code, "%s: %s", path, response.Body.String())
						var body struct {
							Data []audit.AuditRecord `json:"data"`
						}
						require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
						start := min((page-1)*auditSize, len(allRecords))
						require.Len(t, body.Data, min(auditSize, len(allRecords)-start))
						for _, record := range body.Data {
							auditIDs = append(auditIDs, record.ID)
						}
					}
					for page := 1; page <= (len(allDeleted)+deletedSize-1)/deletedSize+1; page++ {
						response := httptest.NewRecorder()
						path := fmt.Sprintf("/notes/deleted?page=%d&%s", page, size)
						server.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil).WithContext(ctx))
						require.Equalf(t, http.StatusOK, response.Code, "%s: %s", path, response.Body.String())
						var body struct {
							Data []*capabilityNote `json:"data"`
						}
						require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
						start := min((page-1)*deletedSize, len(allDeleted))
						require.Len(t, body.Data, min(deletedSize, len(allDeleted)-start))
						for _, entity := range body.Data {
							deletedIDs = append(deletedIDs, entity.GetID())
						}
					}
					wantAuditIDs := make([]int64, len(allRecords))
					for i, record := range allRecords {
						wantAuditIDs[i] = record.ID
					}
					wantDeletedIDs := make([]int64, len(allDeleted))
					for i, entity := range allDeleted {
						wantDeletedIDs[i] = entity.GetID()
					}
					if !reflect.DeepEqual(wantAuditIDs, auditIDs) || !reflect.DeepEqual(wantDeletedIDs, deletedIDs) {
						t.Fatalf("%q skipped or repeated rows: audit=%v want=%v deleted=%v want=%v", size, auditIDs, wantAuditIDs, deletedIDs, wantDeletedIDs)
					}
				}
			})
		}
	}
}
