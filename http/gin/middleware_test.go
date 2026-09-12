package gogin

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	runtimehttp "gochen-runtime/http"
	"gochen-runtime/http/middleware"
	"gochen/errors"
	"gochen/httpx"

	"github.com/gin-gonic/gin"
)

func TestServer_TimeoutRejectsUnsafeAsyncExecution(t *testing.T) {
	logging := false
	srv, err := New(&runtimehttp.WebConfig{Mode: "test", EnableRequestLog: &logging})
	if err != nil {
		t.Fatal(err)
	}
	var middlewareErr error
	srv.Use(func(ctx httpx.IContext, next func() error) error {
		middlewareErr = next()
		return middlewareErr
	}, middleware.Timeout(time.Second))
	called := false
	srv.GET("/timeout", func(ctx httpx.IContext) error {
		called = true
		return ctx.String(http.StatusOK, "unexpected")
	})
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/timeout", nil))
	if !errors.Is(middlewareErr, errors.Unsupported) || called {
		t.Fatalf("unsafe asynchronous chain ran: err=%v called=%v", middlewareErr, called)
	}
	if rec.Code != errors.ToHTTPStatus(middlewareErr) {
		t.Fatalf("status=%d, want %d", rec.Code, errors.ToHTTPStatus(middlewareErr))
	}
}

func TestServer_GlobalMiddlewareScope(t *testing.T) {
	logging := false
	srv, err := New(&runtimehttp.WebConfig{Mode: "test", EnableRequestLog: &logging})
	if err != nil {
		t.Fatal(err)
	}
	srv.Use(func(ctx httpx.IContext, next func() error) error {
		return ctx.String(http.StatusUnauthorized, "denied")
	})
	called := false
	native := func(ctx *gin.Context) { called = true; ctx.String(http.StatusOK, "unexpected") }
	srv.Engine().GET("/native", native)
	srv.Engine().NoRoute(native)
	srv.Engine().HandleMethodNotAllowed = true
	srv.Engine().NoMethod(native)
	srv.Static("/files", t.TempDir())
	for _, req := range []struct{ method, path string }{
		{http.MethodGet, "/native"},
		{http.MethodGet, "/missing"},
		{http.MethodPost, "/native"},
		{http.MethodGet, "/files/missing.txt"},
	} {
		called = false
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, httptest.NewRequest(req.method, req.path, nil))
		if called || rec.Code != http.StatusUnauthorized || rec.Body.String() != "denied" {
			t.Errorf("%s %s bypassed global middleware: called=%v status=%d body=%q", req.method, req.path, called, rec.Code, rec.Body.String())
		}
	}
}
