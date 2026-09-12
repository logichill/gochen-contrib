package gogin

import (
	stdpath "path"
	"strings"

	"gochen/httpx"

	"github.com/gin-gonic/gin"
)

// routeGroup Gin 路由组适配器。
type routeGroup struct {
	group  *gin.RouterGroup
	server *Server
	prefix string
}

func calculateGinPath(basePath, relativePath string) string {
	if relativePath == "" {
		if basePath == "" {
			return "/"
		}
		return basePath
	}
	finalPath := stdpath.Clean(basePath + "/" + relativePath)
	if strings.HasSuffix(relativePath, "/") && !strings.HasSuffix(finalPath, "/") {
		finalPath += "/"
	}
	if !strings.HasPrefix(finalPath, "/") {
		finalPath = "/" + finalPath
	}
	return finalPath
}

func (g *routeGroup) registerRoute(method, path string, handler httpx.Handler) {
	fullPath := calculateGinPath(g.group.BasePath(), path)
	if g.server.reg.tryReserve(method, fullPath) {
		return
	}
	defer func() {
		r := recover()
		if r == nil {
			return
		}
		if conflictPath, ok := duplicateRoutePath(r); ok {
			if conflictPath != fullPath {
				g.server.reg.rollback(method, fullPath)
			}
			g.server.reg.markConflict(method, conflictPath)
			return
		}
		g.server.reg.rollback(method, fullPath)
		panic(r)
	}()
	g.group.Handle(method, path, g.server.wrapHandler(handler))
}

// GET 在当前组注册 GET 路由。
func (g *routeGroup) GET(path string, handler httpx.Handler) httpx.IRouteGroup {
	g.registerRoute("GET", path, handler)
	return g
}

// POST 在当前组注册 POST 路由。
func (g *routeGroup) POST(path string, handler httpx.Handler) httpx.IRouteGroup {
	g.registerRoute("POST", path, handler)
	return g
}

// PUT 在当前组注册 PUT 路由。
func (g *routeGroup) PUT(path string, handler httpx.Handler) httpx.IRouteGroup {
	g.registerRoute("PUT", path, handler)
	return g
}

// DELETE 在当前组注册 DELETE 路由。
func (g *routeGroup) DELETE(path string, handler httpx.Handler) httpx.IRouteGroup {
	g.registerRoute("DELETE", path, handler)
	return g
}

// PATCH 在当前组注册 PATCH 路由。
func (g *routeGroup) PATCH(path string, handler httpx.Handler) httpx.IRouteGroup {
	g.registerRoute("PATCH", path, handler)
	return g
}

// HEAD 在当前组注册 HEAD 路由。
func (g *routeGroup) HEAD(path string, handler httpx.Handler) httpx.IRouteGroup {
	g.registerRoute("HEAD", path, handler)
	return g
}

// OPTIONS 在当前组注册 OPTIONS 路由。
func (g *routeGroup) OPTIONS(path string, handler httpx.Handler) httpx.IRouteGroup {
	g.registerRoute("OPTIONS", path, handler)
	return g
}

// Group 创建继承当前前缀和中间件的子组。
func (g *routeGroup) Group(prefix string) httpx.IRouteGroup {
	subGroup := g.group.Group(prefix)
	return &routeGroup{group: subGroup, server: g.server, prefix: joinRoutePrefix(g.prefix, prefix)}
}

// Use 为本组后续注册的路由追加中间件。
func (g *routeGroup) Use(middleware ...httpx.Middleware) httpx.IRouteGroup {
	for _, mw := range middleware {
		if mw != nil {
			g.group.Use(g.server.wrapMiddleware(mw))
		}
	}
	return g
}
