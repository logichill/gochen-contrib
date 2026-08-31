package gogin

import (
	"context"
	"fmt"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	gochenhttp "gochen-runtime/http"
	"gochen-runtime/http/request"
	"gochen/contextx"
	"gochen/errors"
	"gochen/httpx"

	"github.com/gin-gonic/gin"
)

// Server 承载一组模块的轻量服务实现。
type Server struct {
	engine *gin.Engine
	config *gochenhttp.WebConfig
	server *http.Server
	reg    *routeRegistry

	trustedProxyChecker func(netip.Addr) bool
}

// New 创建服务。
func New(config *gochenhttp.WebConfig) (*Server, error) {
	cfg := config
	if cfg == nil {
		cfg = &gochenhttp.WebConfig{}
	}
	applyWebConfigDefaults(cfg)

	checker, err := buildTrustedProxyChecker(cfg.TrustedProxies)
	if err != nil {
		return nil, err
	}

	switch strings.ToLower(strings.TrimSpace(cfg.Mode)) {
	case "release":
		gin.SetMode(gin.ReleaseMode)
	case "test":
		gin.SetMode(gin.TestMode)
	default:
		gin.SetMode(gin.DebugMode)
	}

	engine := gin.New()
	engine.Use(gin.Recovery())
	enableRequestLog := strings.ToLower(strings.TrimSpace(cfg.Mode)) != "release"
	if cfg.EnableRequestLog != nil {
		enableRequestLog = *cfg.EnableRequestLog
	}
	if enableRequestLog {
		engine.Use(gin.Logger())
	}

	s := &Server{
		engine:              engine,
		config:              cfg,
		reg:                 newRouteRegistry(),
		trustedProxyChecker: checker,
	}

	return s, nil
}

func (s *Server) registerRoute(method, path string, handler httpx.Handler) {
	normPath := normalizeRoutePath(path)
	if s.reg.tryReserve(method, normPath) {
		return
	}
	defer func() {
		r := recover()
		if r == nil {
			return
		}
		if conflictPath, ok := duplicateRoutePath(r); ok {
			if conflictPath != normPath {
				s.reg.rollback(method, normPath)
			}
			s.reg.markConflict(method, conflictPath)
			return
		}
		s.reg.rollback(method, normPath)
		panic(r)
	}()
	s.engine.Handle(method, path, s.wrapHandler(handler))
}

// GET 返回当前值。
func (s *Server) GET(path string, handler httpx.Handler) httpx.IServer {
	s.registerRoute("GET", path, handler)
	return s
}

// POST 处理POST。
func (s *Server) POST(path string, handler httpx.Handler) httpx.IServer {
	s.registerRoute("POST", path, handler)
	return s
}

// PUT 处理PUT。
func (s *Server) PUT(path string, handler httpx.Handler) httpx.IServer {
	s.registerRoute("PUT", path, handler)
	return s
}

// DELETE 删除记录。
func (s *Server) DELETE(path string, handler httpx.Handler) httpx.IServer {
	s.registerRoute("DELETE", path, handler)
	return s
}

// PATCH 处理PATCH。
func (s *Server) PATCH(path string, handler httpx.Handler) httpx.IServer {
	s.registerRoute("PATCH", path, handler)
	return s
}

// HEAD 处理HEAD。
func (s *Server) HEAD(path string, handler httpx.Handler) httpx.IServer {
	s.registerRoute("HEAD", path, handler)
	return s
}

// OPTIONS 处理选项。
func (s *Server) OPTIONS(path string, handler httpx.Handler) httpx.IServer {
	s.registerRoute("OPTIONS", path, handler)
	return s
}

// Group 处理分组。
func (s *Server) Group(prefix string) httpx.IRouteGroup {
	group := s.engine.Group(prefix)
	return &routeGroup{group: group, server: s, prefix: normalizeRoutePrefix(prefix)}
}

// Use 处理Use。
func (s *Server) Use(middleware ...httpx.Middleware) httpx.IServer {
	for _, mw := range middleware {
		if mw != nil {
			s.engine.Use(s.wrapMiddleware(mw))
		}
	}
	return s
}

// Static 处理Static。
func (s *Server) Static(prefix, root string) httpx.IServer {
	p := normalizeRoutePath(prefix)
	if !strings.HasSuffix(p, "/") {
		p += "/"
	}
	fullPattern := p + "*filepath"

	dupGET := s.reg.tryReserve("GET", fullPattern)
	dupHEAD := s.reg.tryReserve("HEAD", fullPattern)
	if dupGET || dupHEAD {
		if !dupGET {
			s.reg.rollback("GET", fullPattern)
		}
		if !dupHEAD {
			s.reg.rollback("HEAD", fullPattern)
		}
		return s
	}

	defer func() {
		r := recover()
		if r == nil {
			return
		}
		if conflictPath, ok := duplicateRoutePath(r); ok {
			s.reg.rollback("GET", fullPattern)
			s.reg.rollback("HEAD", fullPattern)
			s.reg.markConflict("GET", conflictPath)
			s.reg.markConflict("HEAD", conflictPath)
			return
		}
		s.reg.rollback("GET", fullPattern)
		s.reg.rollback("HEAD", fullPattern)
		panic(r)
	}()
	s.engine.StaticFS(prefix, newSafeStaticFS(root))
	return s
}

// ServeStatic 处理ServeStatic。
func (s *Server) ServeStatic(path, root string) {
	p := normalizeRoutePath(path)
	dupGET := s.reg.tryReserve("GET", p)
	dupHEAD := s.reg.tryReserve("HEAD", p)
	if dupGET || dupHEAD {
		if !dupGET {
			s.reg.rollback("GET", p)
		}
		if !dupHEAD {
			s.reg.rollback("HEAD", p)
		}
		return
	}

	defer func() {
		r := recover()
		if r == nil {
			return
		}
		if conflictPath, ok := duplicateRoutePath(r); ok {
			s.reg.rollback("GET", p)
			s.reg.rollback("HEAD", p)
			s.reg.markConflict("GET", conflictPath)
			s.reg.markConflict("HEAD", conflictPath)
			return
		}
		s.reg.rollback("GET", p)
		s.reg.rollback("HEAD", p)
		panic(r)
	}()
	s.engine.StaticFile(path, root)
}

// Start 启动服务。
func (s *Server) Start(addr string) error {
	if addr == "" {
		addr = fmt.Sprintf("%s:%d", s.config.Host, s.config.Port)
	}
	s.server = &http.Server{
		Addr:         addr,
		Handler:      s.engine,
		ReadTimeout:  s.config.ReadTimeout,
		WriteTimeout: s.config.WriteTimeout,
		IdleTimeout:  s.config.IdleTimeout,
	}

	if s.config.TLSEnabled {
		return s.server.ListenAndServeTLS(s.config.CertFile, s.config.KeyFile)
	}
	return s.server.ListenAndServe()
}

// Stop 停止服务。
func (s *Server) Stop(ctx context.Context) error {
	if s.server == nil {
		return nil
	}
	if ctx == nil {
		ctx = contextx.Background()
	}
	return s.server.Shutdown(ctx)
}

// Engine 返回底层 *gin.Engine，仅供 gin 适配层使用。
func (s *Server) Engine() *gin.Engine { return s.engine }

// Handler 返回底层 http.Handler，仅供桥接层使用。
func (s *Server) Handler() http.Handler { return s.engine }

// wrapHandler 包装处理器。
func (s *Server) wrapHandler(handler httpx.Handler) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := newContext(s, c)
		if err := handler(ctx); err != nil {
			_ = writeErrorResponse(ctx, err)
		}
	}
}

// wrapMiddleware 包装中间件。
func (s *Server) wrapMiddleware(middleware httpx.Middleware) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := newContext(s, c)
		err := middleware(ctx, func() error {
			c.Next()
			return nil
		})
		if err != nil {
			_ = writeErrorResponse(ctx, err)
			c.Abort()
		}
	}
}

// routeGroup Gin 路由组适配器。
type routeGroup struct {
	group  *gin.RouterGroup
	server *Server
	prefix string
}

// safeStaticFS 为 Gin 适配器提供“安全默认”静态文件语义：
// - 禁止访问隐藏文件/目录（如 .env/.git）；
// - 禁止目录列表（目录需存在 index.html 才可访问）。
type safeStaticFS struct {
	fs           http.FileSystem
	rootAbs      string
	rootResolved string
	validRoot    bool
}

// newSafeStaticFS 创建SafeStaticFS。
func newSafeStaticFS(root string) safeStaticFS {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return safeStaticFS{fs: gin.Dir(root, false)}
	}

	rootResolved := rootAbs
	if resolved, err := filepath.EvalSymlinks(rootAbs); err == nil {
		rootResolved = resolved
	}

	return safeStaticFS{
		fs:           gin.Dir(root, false),
		rootAbs:      rootAbs,
		rootResolved: rootResolved,
		validRoot:    true,
	}
}

// Open 打开目标资源。
func (s safeStaticFS) Open(name string) (http.File, error) {
	if !s.validRoot {
		return nil, os.ErrNotExist
	}

	clean := path.Clean("/" + name)
	clean = strings.TrimPrefix(clean, "/")
	if clean == "" {
		clean = "."
	}
	if hasHiddenSegment(clean) {
		return nil, os.ErrNotExist
	}

	fullPath := s.rootAbs
	if clean != "." {
		fullPath = filepath.Join(s.rootAbs, filepath.FromSlash(clean))
	}
	fullAbs, err := filepath.Abs(fullPath)
	if err != nil || !isWithinRoot(s.rootAbs, fullAbs) {
		return nil, os.ErrNotExist
	}

	resolvedPath, err := filepath.EvalSymlinks(fullAbs)
	if err != nil || !isWithinRoot(s.rootResolved, resolvedPath) {
		return nil, os.ErrNotExist
	}

	f, err := s.fs.Open(clean)
	if err != nil {
		return nil, err
	}

	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	if info.IsDir() {
		indexPath := path.Join(clean, "index.html")
		if hasHiddenSegment(indexPath) {
			_ = f.Close()
			return nil, os.ErrNotExist
		}

		indexFullPath := filepath.Join(s.rootAbs, filepath.FromSlash(indexPath))
		indexAbs, err := filepath.Abs(indexFullPath)
		if err != nil || !isWithinRoot(s.rootAbs, indexAbs) {
			_ = f.Close()
			return nil, os.ErrNotExist
		}
		resolvedIndexPath, err := filepath.EvalSymlinks(indexAbs)
		if err != nil || !isWithinRoot(s.rootResolved, resolvedIndexPath) {
			_ = f.Close()
			return nil, os.ErrNotExist
		}

		idx, err := s.fs.Open(indexPath)
		if err != nil {
			_ = f.Close()
			return nil, os.ErrNotExist
		}
		_ = idx.Close()
	}

	return f, nil
}

// hasHiddenSegment 判断HiddenSegment。
func hasHiddenSegment(p string) bool {
	for _, seg := range strings.Split(p, "/") {
		if seg == "" || seg == "." {
			continue
		}
		if strings.HasPrefix(seg, ".") {
			if seg == ".well-known" {
				continue
			}
			return true
		}
	}
	return false
}

// isWithinRoot 判断WithinRoot。
func isWithinRoot(rootAbs, fullAbs string) bool {
	if rootAbs == fullAbs {
		return true
	}
	sep := string(os.PathSeparator)
	root := rootAbs
	if !strings.HasSuffix(root, sep) {
		root += sep
	}
	return strings.HasPrefix(fullAbs, root)
}

func (g *routeGroup) registerRoute(method, path string, handler httpx.Handler) {
	fullPath := joinRoutePath(g.prefix, path)
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

// GET 返回当前值。
func (g *routeGroup) GET(path string, handler httpx.Handler) httpx.IRouteGroup {
	g.registerRoute("GET", path, handler)
	return g
}

// POST 处理POST。
func (g *routeGroup) POST(path string, handler httpx.Handler) httpx.IRouteGroup {
	g.registerRoute("POST", path, handler)
	return g
}

// PUT 处理PUT。
func (g *routeGroup) PUT(path string, handler httpx.Handler) httpx.IRouteGroup {
	g.registerRoute("PUT", path, handler)
	return g
}

// DELETE 删除记录。
func (g *routeGroup) DELETE(path string, handler httpx.Handler) httpx.IRouteGroup {
	g.registerRoute("DELETE", path, handler)
	return g
}

// PATCH 处理PATCH。
func (g *routeGroup) PATCH(path string, handler httpx.Handler) httpx.IRouteGroup {
	g.registerRoute("PATCH", path, handler)
	return g
}

// HEAD 处理HEAD。
func (g *routeGroup) HEAD(path string, handler httpx.Handler) httpx.IRouteGroup {
	g.registerRoute("HEAD", path, handler)
	return g
}

// OPTIONS 处理选项。
func (g *routeGroup) OPTIONS(path string, handler httpx.Handler) httpx.IRouteGroup {
	g.registerRoute("OPTIONS", path, handler)
	return g
}

// Group 处理分组。
func (g *routeGroup) Group(prefix string) httpx.IRouteGroup {
	subGroup := g.group.Group(prefix)
	return &routeGroup{group: subGroup, server: g.server, prefix: joinRoutePrefix(g.prefix, prefix)}
}

// Use 处理Use。
func (g *routeGroup) Use(middleware ...httpx.Middleware) httpx.IRouteGroup {
	for _, mw := range middleware {
		if mw != nil {
			g.group.Use(g.server.wrapMiddleware(mw))
		}
	}
	return g
}

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

// Method 返回Method。
func (c *ginContext) Method() string { return c.Context.Request.Method }

// Path 获取路径。
func (c *ginContext) Path() string { return c.Context.Request.URL.Path }

// Query 返回查询。
func (c *ginContext) Query(key string) string { return c.Context.Query(key) }

// Param 返回Param。
func (c *ginContext) Param(key string) string { return c.Context.Param(key) }

// Header 返回请求头。
func (c *ginContext) Header(key string) string { return c.Context.GetHeader(key) }

// QueryParams 返回查询Params。
func (c *ginContext) QueryParams() url.Values { return c.Context.Request.URL.Query() }

// Request 返回请求。
func (c *ginContext) Request() *http.Request { return c.Context.Request }

// ResponseWriter 返回底层 http.ResponseWriter，供 Cookie、Hijack 等适配能力使用。
func (c *ginContext) ResponseWriter() http.ResponseWriter { return c.Writer }

// UserAgent 处理用户Agent。
func (c *ginContext) UserAgent() string { return c.Header("User-Agent") }

// Body 返回响应体。
func (c *ginContext) Body() ([]byte, error) {
	return request.ReadBody(c.Writer, c.Context.Request, c.Keys, "request_body_cache")
}

// BindJSON 处理BindJSON。
func (c *ginContext) BindJSON(obj any) error {
	return request.BindJSON(c.Writer, c.Context.Request, c.Keys, "request_body_cache", obj)
}

// ShouldBindJSON 判断BindJSON。
func (c *ginContext) ShouldBindJSON(obj any) error { return c.BindJSON(obj) }

// BindQuery 处理Bind查询。
func (c *ginContext) BindQuery(obj any) error {
	return request.BindQuery(obj, c.Context.Request.URL.Query())
}

// SetStatus 设置当前 Span 的状态。
func (c *ginContext) SetStatus(code int) { c.Status(code) }

// SetHeader 设置响应头。
func (c *ginContext) SetHeader(key, value string) { c.Context.Header(key, value) }

// JSON 处理JSON。
func (c *ginContext) JSON(code int, obj httpx.JSONBody) error {
	c.Context.JSON(code, obj)
	return nil
}

// String 返回字符串表示。
func (c *ginContext) String(code int, text string) error {
	c.Context.String(code, text)
	return nil
}

// Data 处理数据。
func (c *ginContext) Data(code int, contentType string, data []byte) error {
	c.Context.Data(code, contentType, data)
	return nil
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

// Abort 处理Abort。
func (c *ginContext) Abort() { c.Context.Abort() }

// AbortWithStatus 处理Abort并带状态。
func (c *ginContext) AbortWithStatus(code int) { c.Context.AbortWithStatus(code) }

// AbortWithStatusJSON 处理Abort并带状态JSON。
func (c *ginContext) AbortWithStatusJSON(code int, obj httpx.JSONBody) {
	c.Context.AbortWithStatusJSON(code, obj)
}

// IsAborted 判断Aborted。
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

// RequestContext 返回上下文。
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

// SetContext 设置上下文。
func (c *ginContext) SetContext(ctx httpx.IRequestContext) {
	if ctx == nil {
		ctx = newRequestContext(c.Context.Request)
	}
	c.Set(string(requestContextKey), httpx.ValueOf(ctx))
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

// ClientIP 处理客户端IP。
func (c *ginContext) ClientIP() string {
	return request.ResolveClientIP(c.Context.Request, c.server.trustedProxyChecker)
}

// applyWebConfigDefaults 应用Web配置Defaults。
func applyWebConfigDefaults(cfg *gochenhttp.WebConfig) {
	if cfg.ReadTimeout <= 0 {
		cfg.ReadTimeout = gochenhttp.DefaultReadTimeout
	}
	if cfg.WriteTimeout <= 0 {
		cfg.WriteTimeout = gochenhttp.DefaultWriteTimeout
	}
	if cfg.IdleTimeout <= 0 {
		cfg.IdleTimeout = gochenhttp.DefaultIdleTimeout
	}
	if cfg.Host == "" {
		cfg.Host = gochenhttp.DefaultWebHost
	}
	if cfg.Port == 0 {
		cfg.Port = gochenhttp.DefaultWebPort
	}
}

// buildTrustedProxyChecker 构造TrustedProxyChecker。
func buildTrustedProxyChecker(trusted []string) (func(netip.Addr) bool, error) {
	if len(trusted) == 0 {
		return nil, nil
	}
	addrs := make([]netip.Addr, 0, len(trusted))
	prefixes := make([]netip.Prefix, 0, len(trusted))
	for _, raw := range trusted {
		s := strings.TrimSpace(raw)
		if s == "" {
			continue
		}
		if strings.Contains(s, "/") {
			p, err := netip.ParsePrefix(s)
			if err != nil {
				return nil, errors.NewCode(errors.InvalidInput, "invalid trusted proxy CIDR").WithContext("cidr", s)
			}
			prefixes = append(prefixes, p)
			continue
		}
		ip, err := netip.ParseAddr(s)
		if err != nil {
			return nil, errors.NewCode(errors.InvalidInput, "invalid trusted proxy IP").WithContext("ip", s)
		}
		addrs = append(addrs, ip)
	}

	return func(ip netip.Addr) bool {
		for _, a := range addrs {
			if a == ip {
				return true
			}
		}
		for _, p := range prefixes {
			if p.Contains(ip) {
				return true
			}
		}
		return false
	}, nil
}

// writeErrorResponse 写入错误响应。
func writeErrorResponse(ctx httpx.IContext, err error) error {
	if err == nil {
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

type routeKey struct {
	method string
	path   string
}

type routeRegistry struct {
	mu     sync.Mutex
	counts map[routeKey]int
}

func newRouteRegistry() *routeRegistry {
	return &routeRegistry{counts: make(map[routeKey]int, 64)}
}

func (r *routeRegistry) tryReserve(method, path string) bool {
	method = strings.TrimSpace(strings.ToUpper(method))
	path = normalizeRoutePath(path)
	if method == "" || path == "" {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	k := routeKey{method: method, path: path}
	if r.counts[k] > 0 {
		r.counts[k]++
		return true
	}
	r.counts[k] = 1
	return false
}

func (r *routeRegistry) rollback(method, path string) {
	method = strings.TrimSpace(strings.ToUpper(method))
	path = normalizeRoutePath(path)
	if method == "" || path == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	k := routeKey{method: method, path: path}
	if r.counts[k] <= 1 {
		delete(r.counts, k)
	} else {
		r.counts[k]--
	}
}

func (r *routeRegistry) markConflict(method, path string) {
	method = strings.TrimSpace(strings.ToUpper(method))
	path = normalizeRoutePath(path)
	if method == "" || path == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	k := routeKey{method: method, path: path}
	if r.counts[k] < 1 {
		r.counts[k] = 2
	} else {
		r.counts[k]++
	}
}

func (r *routeRegistry) conflicts() []httpx.RouteConflict {
	r.mu.Lock()
	defer r.mu.Unlock()

	out := make([]httpx.RouteConflict, 0)
	for k, c := range r.counts {
		if c > 1 {
			out = append(out, httpx.RouteConflict{Method: k.method, Path: k.path, Count: c})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Path == out[j].Path {
			return out[i].Method < out[j].Method
		}
		return out[i].Path < out[j].Path
	})
	return out
}

// duplicateRoutePath 从 Gin 内部 panic 消息中提取冲突路径。
//
// 注意：该文案契约来自 gin/tree.go（"handlers are already registered for path '...'"）。
// 升级 Gin 版本时需复核此匹配逻辑；若 Gin 调整报错文案，行为将安全退化为启动期直接 panic，
// 且 routes_test.go 中的单元测试会立即捕获此变化。
func duplicateRoutePath(r any) (string, bool) {
	if r == nil {
		return "", false
	}
	s, ok := r.(string)
	if !ok {
		s = fmt.Sprint(r)
	}
	const prefix = "handlers are already registered for path '"
	const suffix = "'"
	if strings.HasPrefix(s, prefix) && strings.HasSuffix(s, suffix) {
		path := strings.TrimSuffix(strings.TrimPrefix(s, prefix), suffix)
		return path, true
	}
	return "", false
}

// RouteConflicts 返回当前注册路由中存在冲突（method+path 重复）的列表。
//
// 语义说明：
// - 为支持 host 的 FailFastOnRouteConflicts 结构化治理，Server 在发生重复注册时会捕获 Gin 的重复路由 panic 并计入冲突表（首个 handler 生效）；
// - 非 host 托管的单机使用场景下，重复注册不再直接中断崩溃，调用方如需校验冲突应显式检查 RouteConflicts()；
// - 通配符冲突（如 /users/:id 与 /users/:name）及非法 HTTP 方法等其他严重错误仍会保持原生 panic fail-fast。
func (s *Server) RouteConflicts() []httpx.RouteConflict {
	if s == nil || s.reg == nil {
		return nil
	}
	return s.reg.conflicts()
}

func normalizeRoutePrefix(prefix string) string {
	prefix = strings.TrimSpace(prefix)
	if prefix == "" || prefix == "/" {
		return ""
	}
	if !strings.HasPrefix(prefix, "/") {
		prefix = "/" + prefix
	}
	return strings.TrimSuffix(prefix, "/")
}

func normalizeRoutePath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return path
}

func joinRoutePrefix(parentPrefix string, childPrefix string) string {
	parentPrefix = normalizeRoutePrefix(parentPrefix)
	childPrefix = normalizeRoutePrefix(childPrefix)
	if parentPrefix == "" {
		return childPrefix
	}
	if childPrefix == "" {
		return parentPrefix
	}
	return parentPrefix + childPrefix
}

func joinRoutePath(prefix string, path string) string {
	prefix = normalizeRoutePrefix(prefix)
	path = normalizeRoutePath(path)
	if prefix == "" {
		return path
	}
	if path == "" || path == "/" {
		return prefix
	}
	return prefix + path
}

var _ httpx.IServer = (*Server)(nil)
var _ httpx.IRouteRegistry = (*Server)(nil)
var _ httpx.IContext = (*ginContext)(nil)
