package gogin

import (
	"fmt"
	"net/http"

	"gochen/httpx"

	"github.com/gin-gonic/gin"
)

const handlerErrorKey = "gochen.http.handler_error"

// finishRequest 在全部中间件返回后统一处理未被接管的错误。
func (s *Server) finishRequest(c *gin.Context) {
	c.Next()
	if err := handlerError(c); err != nil {
		_ = writeErrorResponse(newContext(s, c), err)
	}
}

func handlerError(c *gin.Context) error {
	value, _ := c.Get(handlerErrorKey)
	err, _ := value.(error)
	return err
}

// wrapHandler 将 handler 错误交还外层中间件，不提前写出错误响应。
func (s *Server) wrapHandler(handler httpx.Handler) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Set(handlerErrorKey, handler(newContext(s, c)))
	}
}

// wrapMiddleware 保留 Gin 全局/分组作用域，并在 next 返回时传递下游错误。
func (s *Server) wrapMiddleware(middleware httpx.Middleware) gin.HandlerFunc {
	return func(c *gin.Context) {
		continued := false
		err := middleware(newContext(s, c), func() error {
			continued = true
			c.Next()
			return handlerError(c)
		})
		c.Set(handlerErrorKey, err)
		// Gin 会自动推进 handler 索引；未调用 next 时必须显式终止。
		if !continued {
			c.Abort()
		}
	}
}

// writeErrorResponse 写入错误响应。
func writeErrorResponse(ctx *ginContext, err error) error {
	if err == nil || ctx.Writer.Written() {
		return nil
	}
	if v, ok := ctx.Get(string(responseWrittenKey)); ok {
		if written, ok := httpx.ValueAs[bool](v); ok && written {
			return nil
		}
	}

	status, payload := httpx.EncodeErrorResponse(ctx, err)
	if payload == nil {
		return nil
	}
	if jerr := ctx.JSON(status, httpx.JSONValue(payload)); jerr != nil {
		_ = ctx.String(http.StatusInternalServerError, fmt.Sprintf("%s: %s", payload.Code, payload.Message))
	}
	ctx.Set(string(responseWrittenKey), httpx.ValueOf(true))
	return nil
}
