package gogin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"

	"gochen-runtime/http/request"
	"gochen/contextx"
	"gochen/errors"
	"gochen/httpx"

	"github.com/gin-gonic/gin"
)

// ctxKey 避免与 gin 内部 key 冲突。
type ctxKey string

const (
	requestContextKey  ctxKey = "gochen.http.request_context"
	responseWrittenKey ctxKey = "gochen.http.response_written"
)

type ginContext struct {
	*gin.Context
	server *Server
}

// newContext 创建上下文。
func newContext(s *Server, c *gin.Context) *ginContext {
	return &ginContext{Context: c, server: s}
}

// Method 返回 HTTP 请求方法。
func (c *ginContext) Method() string { return c.Context.Request.Method }

// Path 返回请求 URL 路径。
func (c *ginContext) Path() string { return c.Context.Request.URL.Path }

// Query 返回指定查询参数的首个值。
func (c *ginContext) Query(key string) string { return c.Context.Query(key) }

// Param 返回匹配路由中的路径参数。
func (c *ginContext) Param(key string) string { return c.Context.Param(key) }

// Header 返回请求头。
func (c *ginContext) Header(key string) string { return c.GetHeader(key) }

// QueryParams 返回全部查询参数。
func (c *ginContext) QueryParams() url.Values { return c.Context.Request.URL.Query() }

// Request 返回底层 HTTP 请求。
func (c *ginContext) Request() *http.Request { return c.Context.Request }

// ResponseWriter 返回底层 http.ResponseWriter，供 Cookie、Hijack 等适配能力使用。
func (c *ginContext) ResponseWriter() http.ResponseWriter { return c.Writer }

// UserAgent 返回请求的 User-Agent。
func (c *ginContext) UserAgent() string { return c.Header("User-Agent") }

// Body 读取并缓存请求体。
func (c *ginContext) Body() ([]byte, error) {
	return request.ReadBody(c.Writer, c.Context.Request, c.Keys, "request_body_cache")
}

// BindJSON 将请求 JSON 解码到目标对象，并应用共享的请求体限制。
func (c *ginContext) BindJSON(obj any) error {
	return request.BindJSON(c.Writer, c.Context.Request, c.Keys, "request_body_cache", obj)
}

// ShouldBindJSON 与 BindJSON 使用相同的解码规则。
func (c *ginContext) ShouldBindJSON(obj any) error { return c.BindJSON(obj) }

// BindQuery 将查询参数绑定到目标对象。
func (c *ginContext) BindQuery(obj any) error {
	return request.BindQuery(obj, c.Context.Request.URL.Query())
}

// SetStatus 设置 HTTP 响应状态码。
func (c *ginContext) SetStatus(code int) { c.Status(code) }

// SetHeader 设置响应头。
func (c *ginContext) SetHeader(key, value string) { c.Context.Header(key, value) }

// JSON 写入指定状态码的 JSON 响应。
func (c *ginContext) JSON(code int, obj httpx.JSONBody) error {
	if !httpx.StatusAllowsBody(code) && !obj.IsNil() {
		return errors.NewCode(errors.InvalidInput, "response status does not allow body")
	}
	var data []byte
	if httpx.StatusAllowsBody(code) {
		var err error
		data, err = json.Marshal(obj)
		if err != nil {
			return errors.Wrap(err, errors.Internal, "failed to serialize JSON")
		}
	}
	return c.writeResponse(code, "application/json; charset=utf-8", data)
}

// String 写入指定状态码的文本响应。
func (c *ginContext) String(code int, text string) error {
	return c.writeResponse(code, "text/plain; charset=utf-8", []byte(text))
}

// Data 写入指定内容类型的二进制响应。
func (c *ginContext) Data(code int, contentType string, data []byte) error {
	return c.writeResponse(code, contentType, data)
}

func (c *ginContext) writeResponse(code int, contentType string, data []byte) error {
	if !httpx.StatusAllowsBody(code) && len(data) != 0 {
		return errors.NewCode(errors.InvalidInput, "response status does not allow body")
	}
	if c.Writer.Written() {
		return errors.NewCode(errors.Conflict, "response already written")
	}
	c.Writer.Header().Set("Content-Type", contentType)
	c.Writer.WriteHeader(code)
	c.Writer.WriteHeaderNow()
	if !httpx.StatusAllowsBody(code) || len(data) == 0 {
		return nil
	}
	_, err := c.Writer.Write(data)
	return err
}

// StatusCode 返回当前响应状态码（best-effort）。
func (c *ginContext) StatusCode() int {
	if c.Writer == nil {
		return 0
	}
	return c.Writer.Status()
}

// BytesWritten 返回响应体字节数（best-effort）。
func (c *ginContext) BytesWritten() int64 {
	if c.Writer == nil {
		return 0
	}
	n := c.Writer.Size()
	if n <= 0 {
		return 0
	}
	return int64(n)
}

// Abort 阻止后续中间件和处理器执行。
func (c *ginContext) Abort() { c.Context.Abort() }

// AbortWithStatus 中止处理并写入响应状态码。
func (c *ginContext) AbortWithStatus(code int) { c.Context.AbortWithStatus(code) }

// AbortWithStatusJSON 中止处理并写入 JSON 响应。
func (c *ginContext) AbortWithStatusJSON(code int, obj httpx.JSONBody) {
	_ = c.JSON(code, obj)
	c.Abort()
}

// IsAborted 返回请求是否已中止。
func (c *ginContext) IsAborted() bool { return c.Context.IsAborted() }

// Set 存储请求级上下文值。
func (c *ginContext) Set(key string, value httpx.ContextValue) {
	c.Context.Set(key, value)
}

// Get 获取请求级上下文值。
func (c *ginContext) Get(key string) (httpx.ContextValue, bool) {
	v, ok := c.Context.Get(key)
	if !ok {
		return httpx.ContextValue{}, false
	}
	if typed, ok := v.(httpx.ContextValue); ok {
		return typed, true
	}
	return httpx.ValueOf(v), true
}

// RequestContext 返回当前请求的 Gochen 上下文。
func (c *ginContext) RequestContext() httpx.IRequestContext {
	if v, ok := c.Get(string(requestContextKey)); ok {
		if rc, ok := httpx.ValueAs[httpx.IRequestContext](v); ok && rc != nil {
			return rc
		}
	}
	rc := newRequestContext(c.Context.Request)
	c.Set(string(requestContextKey), httpx.ValueOf(rc))
	return rc
}

// SetContext 替换当前请求的 Gochen 上下文。
func (c *ginContext) SetContext(ctx httpx.IRequestContext) {
	if ctx == nil {
		ctx = newRequestContext(c.Context.Request)
	}
	c.Set(string(requestContextKey), httpx.ValueOf(ctx))
	if c.Context.Request != nil {
		c.Context.Request = c.Context.Request.WithContext(ctx)
	}
}

// Required 获取指定 key 的值；当 key 不存在时返回 error（不再 panic）。
func (c *ginContext) Required(key string) (httpx.ContextValue, error) {
	if key == "" {
		return httpx.ContextValue{}, errors.NewCode(errors.InvalidInput, "key cannot be empty")
	}
	if v, ok := c.Get(key); ok {
		return v, nil
	}
	return httpx.ContextValue{}, errors.NewCode(errors.Internal, "missing required context value").
		WithContext("key", key)
}

// GinContext 返回底层 *gin.Context，仅供 gin 适配层使用。
func (c *ginContext) GinContext() *gin.Context { return c.Context }

// ClientIP 根据可信代理规则解析客户端地址。
func (c *ginContext) ClientIP() string {
	return request.ResolveClientIP(c.Context.Request, c.server.trustedProxyChecker)
}

// newRequestContext 创建请求上下文。
func newRequestContext(req *http.Request) httpx.IRequestContext {
	base := context.Context(nil)
	if req != nil {
		base = req.Context()
	}
	if base == nil {
		base = contextx.Background()
	}
	if strings.TrimSpace(contextx.RequestID(base)) == "" && req != nil {
		if requestID := strings.TrimSpace(req.Header.Get("X-Request-ID")); requestID != "" {
			if bound, err := contextx.WithRequestID(base, requestID); err == nil {
				base = bound
			}
		}
	}
	ctx, err := httpx.NewRequestContext(base)
	if err == nil && ctx != nil {
		return ctx
	}
	fallback, _ := httpx.NewRequestContext(contextx.Background())
	return fallback
}
