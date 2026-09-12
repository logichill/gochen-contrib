package gogin_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	gogin "gochen-contrib/http/gin"
	gochenhttp "gochen-runtime/http"
	"gochen/httpx"
)

func TestListRoutes(t *testing.T) {
	// Test nil and typed nil handling
	if routes := gogin.ListRoutes(nil); routes != nil {
		t.Fatalf("expected nil routes for nil target, got %+v", routes)
	}
	var nilServer *gogin.Server
	if routes := gogin.ListRoutes(nilServer); routes != nil {
		t.Fatalf("expected nil routes for typed-nil server, got %+v", routes)
	}

	srv, err := gogin.New(&gochenhttp.WebConfig{Mode: "test"})
	if err != nil {
		t.Fatalf("new gin server: %v", err)
	}

	api := srv.Group("/api/v1")
	api.GET("/users", func(ctx httpx.IContext) error { return ctx.String(200, "users") })
	api.POST("/users", func(ctx httpx.IContext) error { return ctx.String(200, "create user") })
	srv.GET("/health", func(ctx httpx.IContext) error { return ctx.String(200, "ok") })
	srv.GET("/metrics", func(ctx httpx.IContext) error { return ctx.String(200, "metrics") })

	routes := gogin.ListRoutes(srv)
	if len(routes) != 4 {
		t.Fatalf("expected 4 routes, got %d", len(routes))
	}

	docRoutes := gogin.ListRoutes(srv, gogin.WithPathFilter(gogin.DefaultDocumentableFilter("/api/v1/")))
	pathSet := make(map[string]bool)
	for _, r := range docRoutes {
		pathSet[r.Path] = true
	}
	if !pathSet["/api/v1/users"] {
		t.Errorf("expected /api/v1/users in docRoutes")
	}
	if !pathSet["/health"] {
		t.Errorf("expected /health in docRoutes")
	}
	if pathSet["/metrics"] {
		t.Errorf("/metrics should have been filtered out by default documentable filter")
	}
}

func TestServerRouteConflictsAndHostInteroperability(t *testing.T) {
	srv, err := gogin.New(&gochenhttp.WebConfig{Mode: "test"})
	if err != nil {
		t.Fatalf("new gin server: %v", err)
	}

	// Register unique routes
	srv.GET("/ping", func(ctx httpx.IContext) error { return ctx.String(200, "pong") })
	srv.POST("/ping", func(ctx httpx.IContext) error { return ctx.String(200, "pong") })

	if conflicts := srv.RouteConflicts(); len(conflicts) != 0 {
		t.Fatalf("expected 0 conflicts, got %d: %+v", len(conflicts), conflicts)
	}

	// Register duplicate route (Server catches Gin panic and records conflict)
	srv.GET("/ping", func(ctx httpx.IContext) error { return ctx.String(200, "duplicate") })

	conflicts := srv.RouteConflicts()
	if len(conflicts) != 1 {
		t.Fatalf("expected 1 conflict, got %d: %+v", len(conflicts), conflicts)
	}
	if conflicts[0].Method != http.MethodGet || conflicts[0].Path != "/ping" || conflicts[0].Count != 2 {
		t.Fatalf("unexpected conflict detail: %+v", conflicts[0])
	}

	// Test host WithRouteRegistry interoperability
	wrapped := httpx.WithRouteRegistry(srv)
	if wrapped != srv {
		t.Fatalf("WithRouteRegistry should return srv unchanged since it implements IRouteRegistry")
	}

	engine, ok := gogin.EngineOf(wrapped)
	if !ok || engine == nil {
		t.Fatalf("EngineOf failed on WithRouteRegistry wrapped server")
	}

	handler, ok := gogin.HandlerOf(wrapped)
	if !ok || handler == nil {
		t.Fatalf("HandlerOf failed on WithRouteRegistry wrapped server")
	}
}

func TestServerWildcardConflictStillPanics(t *testing.T) {
	srv, err := gogin.New(&gochenhttp.WebConfig{Mode: "test"})
	if err != nil {
		t.Fatalf("new gin server: %v", err)
	}
	srv.GET("/users/:id", func(ctx httpx.IContext) error { return nil })

	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic on wildcard conflict, but none occurred")
		}
	}()
	srv.GET("/users/:name", func(ctx httpx.IContext) error { return nil })
}

func TestServerGroupSlashNormalizationConflict(t *testing.T) {
	srv, err := gogin.New(&gochenhttp.WebConfig{Mode: "test"})
	if err != nil {
		t.Fatalf("new gin server: %v", err)
	}
	g := srv.Group("/api")
	g.GET("/users", func(ctx httpx.IContext) error { return nil })
	g.GET("//users", func(ctx httpx.IContext) error { return nil })

	conflicts := srv.RouteConflicts()
	if len(conflicts) != 1 {
		t.Fatalf("expected 1 conflict for group //users, got %d: %+v", len(conflicts), conflicts)
	}
	if conflicts[0].Path != "/api/users" || conflicts[0].Count != 2 {
		t.Fatalf("unexpected conflict detail: %+v", conflicts[0])
	}
}

func TestServerGroupEmptyAndTrailingSlashDistinct(t *testing.T) {
	srv, err := gogin.New(&gochenhttp.WebConfig{Mode: "test"})
	if err != nil {
		t.Fatalf("new gin server: %v", err)
	}
	gSlash := srv.Group("/api/")
	gSlash.GET("", func(ctx httpx.IContext) error { return ctx.String(200, "slash") })

	gPlain := srv.Group("/api")
	gPlain.GET("", func(ctx httpx.IContext) error { return ctx.String(200, "plain") })

	conflicts := srv.RouteConflicts()
	if len(conflicts) != 0 {
		t.Fatalf("expected 0 conflicts for /api and /api/, got %+v", conflicts)
	}

	h := srv.Handler()
	recSlash := httptest.NewRecorder()
	h.ServeHTTP(recSlash, httptest.NewRequest("GET", "/api/", nil))
	if recSlash.Code != 200 || recSlash.Body.String() != "slash" {
		t.Fatalf("expected /api/ to return 200 slash, got status=%d body=%q", recSlash.Code, recSlash.Body.String())
	}

	recPlain := httptest.NewRecorder()
	h.ServeHTTP(recPlain, httptest.NewRequest("GET", "/api", nil))
	if recPlain.Code != 200 || recPlain.Body.String() != "plain" {
		t.Fatalf("expected /api to return 200 plain, got status=%d body=%q", recPlain.Code, recPlain.Body.String())
	}
}

func TestServerStaticDuplicateConflict(t *testing.T) {
	srv, err := gogin.New(&gochenhttp.WebConfig{Mode: "test"})
	if err != nil {
		t.Fatalf("new gin server: %v", err)
	}
	dir := t.TempDir()
	srv.Static("/pub", dir)
	srv.Static("/pub", dir)

	conflicts := srv.RouteConflicts()
	if len(conflicts) != 2 {
		t.Fatalf("expected 2 conflicts for static duplicate (GET+HEAD), got %d: %+v", len(conflicts), conflicts)
	}
	for _, c := range conflicts {
		if c.Path != "/pub/*filepath" || c.Count != 2 {
			t.Fatalf("unexpected static conflict detail: %+v", c)
		}
	}
}

func TestServerStaticThenGetWildcardConflict(t *testing.T) {
	srv, err := gogin.New(&gochenhttp.WebConfig{Mode: "test"})
	if err != nil {
		t.Fatalf("new gin server: %v", err)
	}
	dir := t.TempDir()
	srv.Static("/pub", dir)
	srv.GET("/pub/*filepath", func(ctx httpx.IContext) error { return nil })

	conflicts := srv.RouteConflicts()
	if len(conflicts) != 1 {
		t.Fatalf("expected 1 conflict (GET /pub/*filepath), got %d: %+v", len(conflicts), conflicts)
	}
	if conflicts[0].Method != http.MethodGet || conflicts[0].Path != "/pub/*filepath" || conflicts[0].Count != 2 {
		t.Fatalf("unexpected static then get conflict detail: %+v", conflicts[0])
	}
}

func TestServerGetWildcardThenStaticConflict(t *testing.T) {
	srv, err := gogin.New(&gochenhttp.WebConfig{Mode: "test"})
	if err != nil {
		t.Fatalf("new gin server: %v", err)
	}
	dir := t.TempDir()
	srv.GET("/pub/*filepath", func(ctx httpx.IContext) error { return nil })
	srv.Static("/pub", dir)

	conflicts := srv.RouteConflicts()
	if len(conflicts) != 1 {
		t.Fatalf("expected 1 conflict (GET /pub/*filepath), got %d: %+v", len(conflicts), conflicts)
	}
	if conflicts[0].Method != http.MethodGet || conflicts[0].Path != "/pub/*filepath" || conflicts[0].Count != 2 {
		t.Fatalf("unexpected get then static conflict detail: %+v", conflicts[0])
	}
}

func TestServerServeStaticDuplicateConflict(t *testing.T) {
	srv, err := gogin.New(&gochenhttp.WebConfig{Mode: "test"})
	if err != nil {
		t.Fatalf("new gin server: %v", err)
	}
	filePath := filepath.Join(t.TempDir(), "file.txt")
	_ = os.WriteFile(filePath, []byte("ok"), 0644)
	srv.ServeStatic("/file.txt", filePath)
	srv.ServeStatic("/file.txt", filePath)

	conflicts := srv.RouteConflicts()
	if len(conflicts) != 2 {
		t.Fatalf("expected 2 conflicts for serve static duplicate (GET+HEAD), got %d: %+v", len(conflicts), conflicts)
	}
	for _, c := range conflicts {
		if c.Path != "/file.txt" || c.Count != 2 {
			t.Fatalf("unexpected serve static conflict detail: %+v", c)
		}
	}
}

func TestAccessHelpersTypedNilSafety(t *testing.T) {
	var nilServer *gogin.Server
	if engine, ok := gogin.EngineOf(nilServer); ok || engine != nil {
		t.Fatalf("expected (nil, false) for typed-nil server EngineOf")
	}
	if handler, ok := gogin.HandlerOf(nilServer); ok || handler != nil {
		t.Fatalf("expected (nil, false) for typed-nil server HandlerOf")
	}

	var nilContext httpx.IContext
	if ginCtx, ok := gogin.ContextOf(nilContext); ok || ginCtx != nil {
		t.Fatalf("expected (nil, false) for nil context ContextOf")
	}
}
