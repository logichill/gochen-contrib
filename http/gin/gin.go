package gogin

import (
	"net/http"
	"net/netip"
	"strings"
	"sync"

	gochenhttp "gochen-runtime/http"
	"gochen-runtime/http/request"

	"github.com/gin-gonic/gin"
)

// Server 将 Gin 路由接入 Gochen HTTP 契约，并管理 HTTP 服务生命周期。
type Server struct {
	engine      *gin.Engine
	config      *gochenhttp.WebConfig
	server      *http.Server
	reg         *routeRegistry
	lifecycleMu sync.Mutex
	stopped     bool

	trustedProxyChecker func(netip.Addr) bool
}

// New 创建 Gin 适配器，并校验可信代理配置。
func New(config *gochenhttp.WebConfig) (*Server, error) {
	cfg := gochenhttp.DefaultWebConfig()
	if config != nil {
		copied := *config
		cfg = &copied
	}

	checker, err := request.NewTrustedProxyChecker(cfg.TrustedProxies)
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
	engine.Use(s.finishRequest)

	return s, nil
}
