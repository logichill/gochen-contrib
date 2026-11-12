package gogin

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	hostauthz "gochen-runtime/host/authz"
	"gochen/auth"
	"gochen/errors"
	"gochen/httpx"
)

func TestAuthorizationGateIncludesNativeRoutesAndSealsRegistrations(t *testing.T) {
	server, err := New(nil)
	if err != nil {
		t.Fatal(err)
	}
	server.GET("/health", func(httpx.IContext) error { return nil })
	server.Engine().GET("/native", func(*gin.Context) {})
	entries := hostauthz.NewEntryRegistry()
	if err := entries.BindHTTP("GET", "/health", auth.EntryBinding{ResourceAccess: auth.ResourceAccessNone, Source: "health handler", AllowReason: "liveness"}); err != nil {
		t.Fatal(err)
	}
	if err := entries.ValidateEntries(t.Context(), server); !errors.Is(err, errors.InvalidInput) {
		t.Fatalf("missing native entry=%v", err)
	}
	if report := entries.Report(); len(report) != 2 || report[1].Class != auth.EntryInvalid {
		t.Fatalf("native inventory=%+v", report)
	}
	if err := entries.BindHTTP("GET", "/native", auth.EntryBinding{ResourceAccess: auth.ResourceAccessNone, Source: "native diagnostic handler", AllowReason: "public diagnostics"}); err != nil {
		t.Fatal(err)
	}
	if err := entries.ValidateEntries(t.Context(), server); err != nil {
		t.Fatal(err)
	}
	server.POST("/late", func(httpx.IContext) error { return nil })
	if !errors.Is(server.RegistrationError(), errors.FailedPrecondition) {
		t.Fatalf("late registration=%v", server.RegistrationError())
	}
	for _, route := range server.Routes() {
		if route.Path == "/late" {
			t.Fatal("unvalidated route became active")
		}
	}
}

func TestNativeRegistrationAfterEntryGateCannotExecute(t *testing.T) {
	server, err := New(nil)
	if err != nil {
		t.Fatal(err)
	}
	native := server.Engine()
	native.GET("/health", func(c *gin.Context) { c.Status(http.StatusOK) })
	entries := hostauthz.NewEntryRegistry()
	if err := entries.BindHTTP("GET", "/health", auth.EntryBinding{ResourceAccess: auth.ResourceAccessNone, Source: "native health", AllowReason: "liveness"}); err != nil {
		t.Fatal(err)
	}
	if err := entries.ValidateEntries(t.Context(), server); err != nil {
		t.Fatal(err)
	}
	called := false
	native.GET("/late-native", func(c *gin.Context) { called = true; c.Status(http.StatusOK) })
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/late-native", nil))
	if called || response.Code == http.StatusOK {
		t.Fatalf("unvalidated native entry executed: called=%t status=%d", called, response.Code)
	}
	if !errors.Is(server.RegistrationError(), errors.FailedPrecondition) {
		t.Fatalf("native route drift=%v", server.RegistrationError())
	}
	if err := server.Start("127.0.0.1:0"); !errors.Is(err, errors.FailedPrecondition) {
		t.Fatalf("drifted server startup=%v", err)
	}
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/health", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("validated route failed: status=%d", response.Code)
	}
}
