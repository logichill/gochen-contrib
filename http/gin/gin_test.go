package gogin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	gochenhttp "gochen-runtime/http"
	"gochen/contextx"
	"gochen/errors"
	"gochen/httpx"

	"github.com/gin-gonic/gin"
)

func TestServer_MaxBodySizeEnforced(t *testing.T) {
	s, err := New(&gochenhttp.WebConfig{Mode: "test"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	s.Use(func(c httpx.IContext, next func() error) error {
		c.Set(httpx.MaxBodySizeKey, httpx.ValueOf(int64(8)))
		return next()
	})
	s.POST("/x", func(c httpx.IContext) error {
		_, err := c.Body()
		return err
	})

	engine, ok := EngineOf(s)
	if !ok {
		t.Fatalf("expected gin engine")
	}
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/x", strings.NewReader(`{"x":"yyyyyyyyyyyy"}`))
	engine.ServeHTTP(w, r)

	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected 413, got %d body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), string(errors.PayloadTooLarge)) {
		t.Fatalf("expected response contains %s, got %s", errors.PayloadTooLarge, w.Body.String())
	}
}

func TestServer_MaxBodySizeRouteOverride(t *testing.T) {
	s, err := New(&gochenhttp.WebConfig{Mode: "test"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	s.Use(func(c httpx.IContext, next func() error) error {
		c.Set(httpx.MaxBodySizeKey, httpx.ValueOf(int64(4)))
		return next()
	})
	s.POST("/x", func(c httpx.IContext) error {
		_, err := c.Body()
		return err
	})

	engine, ok := EngineOf(s)
	if !ok {
		t.Fatalf("expected gin engine")
	}
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/x", strings.NewReader(`1234567890`))
	engine.ServeHTTP(w, r)

	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected 413, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestServer_Static_HiddenFilesBlockedByDefault(t *testing.T) {
	s, err := New(&gochenhttp.WebConfig{Mode: "test"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("secret"), 0o600); err != nil {
		t.Fatalf("write hidden file: %v", err)
	}

	s.Static("/assets", dir)

	engine, ok := EngineOf(s)
	if !ok {
		t.Fatalf("expected gin engine")
	}
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/assets/.env", nil)
	engine.ServeHTTP(w, r)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for hidden file, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestServer_Static_WellKnownAllowed(t *testing.T) {
	s, err := New(&gochenhttp.WebConfig{Mode: "test"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	dir := t.TempDir()
	wellKnownDir := filepath.Join(dir, ".well-known")
	if err := os.MkdirAll(wellKnownDir, 0o755); err != nil {
		t.Fatalf("mkdir .well-known: %v", err)
	}
	if err := os.WriteFile(filepath.Join(wellKnownDir, "acme-challenge"), []byte("ok"), 0o600); err != nil {
		t.Fatalf("write .well-known file: %v", err)
	}

	s.Static("/assets", dir)

	engine, ok := EngineOf(s)
	if !ok {
		t.Fatalf("expected gin engine")
	}
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/assets/.well-known/acme-challenge", nil)
	engine.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 for .well-known path, got %d body=%s", w.Code, w.Body.String())
	}
	if strings.TrimSpace(w.Body.String()) != "ok" {
		t.Fatalf("unexpected body: %q", w.Body.String())
	}
}

func TestServer_Static_SymlinkEscapeBlocked(t *testing.T) {
	s, err := New(&gochenhttp.WebConfig{Mode: "test"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	root := t.TempDir()
	outsideDir := t.TempDir()
	outsideFile := filepath.Join(outsideDir, "secret.txt")
	if err := os.WriteFile(outsideFile, []byte("TOPSECRET"), 0o600); err != nil {
		t.Fatalf("write outside file: %v", err)
	}

	if err := os.Symlink(outsideFile, filepath.Join(root, "link.txt")); err != nil {
		t.Fatalf("create symlink: %v", err)
	}

	s.Static("/assets", root)

	engine, ok := EngineOf(s)
	if !ok {
		t.Fatalf("expected gin engine")
	}
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/assets/link.txt", nil)
	engine.ServeHTTP(w, r)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for symlink escape, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestWriteErrorResponse_UsesHttpxEncoderMetadataFields(t *testing.T) {
	gin.SetMode(gin.TestMode)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	ctx := newContext(&Server{}, c)

	derived, err := contextx.WithRequestID(ctx.RequestContext(), "req-1")
	if err != nil {
		t.Fatalf("WithRequestID returned error: %v", err)
	}
	reqCtx := ctx.RequestContext().WithContext(derived)
	derived, err = contextx.WithTraceID(reqCtx, "trc-1")
	if err != nil {
		t.Fatalf("WithTraceID returned error: %v", err)
	}
	ctx.SetContext(reqCtx.WithContext(derived))

	if err := writeErrorResponse(ctx, errors.NewCode(errors.NotFound, "not found")); err != nil {
		t.Fatalf("writeErrorResponse returned error: %v", err)
	}
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}

	var payload httpx.ResponseMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}
	if payload.TraceID != "trc-1" {
		t.Fatalf("expected trace_id %q, got %q", "trc-1", payload.TraceID)
	}
	if payload.RequestID != "req-1" {
		t.Fatalf("expected request_id %q, got %q", "req-1", payload.RequestID)
	}
}

func TestRequestContextBindsRequestIDFromHeader(t *testing.T) {
	gin.SetMode(gin.TestMode)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Request-ID", "req-stage4")
	c.Request = req
	ctx := newContext(&Server{}, c)

	reqCtx := ctx.RequestContext()
	if requestID := contextx.RequestID(reqCtx); requestID != "req-stage4" {
		t.Fatalf("expected request_id %q, got %q", "req-stage4", requestID)
	}
}
