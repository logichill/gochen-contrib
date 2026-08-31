package gogin

import (
	"net/http"
	"reflect"

	"gochen/httpx"

	"github.com/gin-gonic/gin"
)

// EngineProvider 暴露底层 *gin.Engine，仅供 gin 适配层使用。
type EngineProvider interface {
	Engine() *gin.Engine
}

// HandlerProvider 暴露底层 http.Handler，仅供 gin 适配层使用。
type HandlerProvider interface {
	Handler() http.Handler
}

// ContextProvider 暴露底层 *gin.Context，仅供 gin 适配层使用。
type ContextProvider interface {
	GinContext() *gin.Context
}

// EngineOf 从抽象 server 中提取底层 *gin.Engine。
func EngineOf(server httpx.IServer) (*gin.Engine, bool) {
	if isNil(server) {
		return nil, false
	}
	if provider, ok := server.(EngineProvider); ok {
		engine := provider.Engine()
		return engine, engine != nil
	}
	return nil, false
}

// HandlerOf 从抽象 server 中提取底层 http.Handler。
func HandlerOf(server httpx.IServer) (http.Handler, bool) {
	if isNil(server) {
		return nil, false
	}
	if provider, ok := server.(HandlerProvider); ok {
		handler := provider.Handler()
		return handler, handler != nil
	}
	return nil, false
}

// ContextOf 从抽象上下文中提取底层 *gin.Context。
func ContextOf(ctx httpx.IContext) (*gin.Context, bool) {
	if isNil(ctx) {
		return nil, false
	}
	if provider, ok := ctx.(ContextProvider); ok {
		ginCtx := provider.GinContext()
		return ginCtx, ginCtx != nil
	}
	return nil, false
}

func isNil(v any) bool {
	if v == nil {
		return true
	}
	val := reflect.ValueOf(v)
	switch val.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return val.IsNil()
	default:
		return false
	}
}
