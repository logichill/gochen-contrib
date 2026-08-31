package redislock

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	stdErrors "errors"
	"sync"
	"time"

	"gochen/contextx"
	"gochen/errors"
	"gochen/observe/logging"
	"gochen/process/lock"

	"github.com/redis/go-redis/v9"
)

// Config 定义相关配置。
type Config struct {
	// KeyPrefix Redis key 前缀（默认：distributed_locks:）。
	KeyPrefix string

	// Owner 当前实例标识（会写入 token，用于诊断；release 安全校验使用 token）。
	Owner string

	// TTL 锁过期时间（默认：30s）。
	TTL time.Duration

	// MaxLeaseDuration 租约单次最大生命周期上限（防调用方泄漏；默认：0 表示由 5*TTL 兜底自动放行）。
	MaxLeaseDuration time.Duration

	// PollInterval 未获取到锁时的轮询等待时间（默认：50ms）。
	PollInterval time.Duration

	Logger logging.ILogger
}

// Provider 定义相关提供者。
type Provider struct {
	client           redis.UniversalClient
	keyPrefix        string
	owner            string
	ttl              time.Duration
	maxLeaseDuration time.Duration
	pollInterval     time.Duration
	logger           logging.ILogger
}

// New 创建提供者。
func New(client redis.UniversalClient, cfg *Config) (*Provider, error) {
	if client == nil {
		return nil, errors.NewCode(errors.InvalidInput, "redis client cannot be nil")
	}
	if cfg == nil {
		cfg = &Config{}
	}
	if cfg.Owner == "" {
		return nil, errors.NewCode(errors.InvalidInput, "owner cannot be empty")
	}
	if cfg.KeyPrefix == "" {
		cfg.KeyPrefix = "distributed_locks:"
	}
	if cfg.TTL <= 0 {
		cfg.TTL = 30 * time.Second
	}
	maxLeaseDuration := cfg.MaxLeaseDuration
	if maxLeaseDuration <= 0 {
		maxLeaseDuration = 5 * cfg.TTL
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = 50 * time.Millisecond
	}
	if cfg.Logger == nil {
		cfg.Logger = logging.ComponentLogger("lock.redis").WithField("prefix", cfg.KeyPrefix)
	}
	return &Provider{
		client:           client,
		keyPrefix:        cfg.KeyPrefix,
		owner:            cfg.Owner,
		ttl:              cfg.TTL,
		maxLeaseDuration: maxLeaseDuration,
		pollInterval:     cfg.PollInterval,
		logger:           cfg.Logger,
	}, nil
}

// Acquire 处理基础锁获取。
func (p *Provider) Acquire(ctx context.Context, key string) (func(), error) {
	lease, err := p.AcquireLease(ctx, key)
	if err != nil {
		return nil, err
	}
	return lease.Release, nil
}

// AcquireLease 获取租约锁，具备自动续期与租约丢失信号。
func (p *Provider) AcquireLease(ctx context.Context, key string) (lock.ILockLease, error) {
	if key == "" {
		return nil, errors.NewCode(errors.InvalidInput, "lock key cannot be empty")
	}
	if ctx == nil {
		ctx = contextx.Background()
	}

	token, err := newToken(p.owner)
	if err != nil {
		return nil, errors.Wrap(err, errors.Internal, "generate redis lock token failed").WithContext("key", key)
	}
	redisKey := p.keyPrefix + key

	start := time.Now()
	for {
		ok, err := p.client.SetNX(ctx, redisKey, token, p.ttl).Result()
		if err != nil {
			if stdErrors.Is(err, context.DeadlineExceeded) {
				return nil, errors.NewCode(errors.Timeout, "lock acquire timeout").
					WithContext("key", key).
					WithContext("ms", time.Since(start).Milliseconds())
			}
			if stdErrors.Is(err, context.Canceled) {
				return nil, err
			}
			return nil, errors.Wrap(err, errors.Cache, "redis lock acquire failed").WithContext("key", key)
		}
		if ok {
			return p.newLease(redisKey, token), nil
		}

		select {
		case <-ctx.Done():
			if stdErrors.Is(ctx.Err(), context.DeadlineExceeded) {
				return nil, errors.NewCode(errors.Timeout, "lock acquire timeout").
					WithContext("key", key).
					WithContext("ms", time.Since(start).Milliseconds())
			}
			return nil, ctx.Err()
		default:
		}

		select {
		case <-time.After(p.pollInterval):
		case <-ctx.Done():
			if stdErrors.Is(ctx.Err(), context.DeadlineExceeded) {
				return nil, errors.NewCode(errors.Timeout, "lock acquire timeout").
					WithContext("key", key).
					WithContext("ms", time.Since(start).Milliseconds())
			}
			return nil, ctx.Err()
		}
	}
}

type redisLockLease struct {
	release func()
	lost    chan error
}

func (l *redisLockLease) Release() {
	if l == nil || l.release == nil {
		return
	}
	l.release()
}

func (l *redisLockLease) Lost() <-chan error {
	if l == nil {
		ch := make(chan error)
		close(ch)
		return ch
	}
	return l.lost
}

type safeLostChan struct {
	ch   chan<- error
	once sync.Once
}

func (l *safeLostChan) close(err error) {
	if l == nil || l.ch == nil {
		return
	}
	l.once.Do(func() {
		if err != nil {
			select {
			case l.ch <- err:
			default:
			}
		}
		close(l.ch)
	})
}

func (p *Provider) newLease(redisKey string, token string) *redisLockLease {
	lost := make(chan error, 1)
	safeLost := &safeLostChan{ch: lost}
	stopRenew := p.startRenewal(redisKey, token, safeLost)
	return &redisLockLease{
		lost:    lost,
		release: p.releaseFunc(redisKey, token, stopRenew, safeLost),
	}
}

var redisRenewLua = `
if redis.call("GET", KEYS[1]) == ARGV[1] then
  return redis.call("PEXPIRE", KEYS[1], ARGV[2])
else
  return 0
end
`

func (p *Provider) startRenewal(redisKey string, token string, safeLost *safeLostChan) func() {
	renewCtx, cancel := context.WithCancel(contextx.Background())
	interval := p.ttl / 3
	if interval <= 0 {
		interval = 100 * time.Millisecond
	}
	ttlMs := p.ttl.Milliseconds()
	renewScript := redis.NewScript(redisRenewLua)
	startTime := time.Now()

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-renewCtx.Done():
				return
			case <-ticker.C:
				if p.maxLeaseDuration > 0 && time.Since(startTime) >= p.maxLeaseDuration {
					p.logger.Warn(contextx.Background(), "lock lease max duration reached, stopping renewal to prevent leak",
						logging.String("redis_key", redisKey))
					safeLost.close(errors.NewCode(errors.Timeout, "lock lease max duration reached"))
					return
				}

				execCtx, execCancel := context.WithTimeout(renewCtx, interval)
				res, err := renewScript.Run(execCtx, p.client, []string{redisKey}, token, ttlMs).Result()
				execCancel()

				if err != nil {
					if stdErrors.Is(renewCtx.Err(), context.Canceled) {
						return
					}
					p.logger.Warn(contextx.Background(), "lock renewal failed",
						logging.Error(err), logging.String("redis_key", redisKey))
					safeLost.close(errors.Wrap(err, errors.Cache, "lock renewal failed"))
					return
				}

				if n, ok := res.(int64); !ok || n == 0 {
					p.logger.Warn(contextx.Background(), "lock renewal lost lease (token mismatch or expired)",
						logging.String("redis_key", redisKey))
					safeLost.close(errors.NewCode(errors.Conflict, "lock lease lost"))
					return
				}
			}
		}
	}()

	return cancel
}

var redisReleaseLua = `
if redis.call("GET", KEYS[1]) == ARGV[1] then
  return redis.call("DEL", KEYS[1])
else
  return 0
end
`

// releaseFunc 处理releaseFunc。
func (p *Provider) releaseFunc(redisKey string, token string, stopRenew func(), safeLost *safeLostChan) func() {
	releaseScript := redis.NewScript(redisReleaseLua)

	var once sync.Once
	return func() {
		once.Do(func() {
			if stopRenew != nil {
				stopRenew()
			}
			// 先关闭 lost 标记正常释放，防止停止续期协程并发上报假的 lost error
			safeLost.close(nil)

			ctx, cancel := context.WithTimeout(contextx.Background(), 5*time.Second)
			defer cancel()

			res, err := releaseScript.Run(ctx, p.client, []string{redisKey}, token).Result()
			if err != nil {
				p.logger.Warn(ctx, "lock release failed", logging.Error(err), logging.String("redis_key", redisKey))
				return
			}

			// 0 代表 owner/token 不匹配或 key 已不存在（都属于“可接受但可疑”的情况）。
			if n, ok := res.(int64); ok && n == 0 {
				p.logger.Warn(ctx, "lock release had no effect (token mismatch or already expired)", logging.String("redis_key", redisKey))
			}
		})
	}
}

// newToken 创建令牌。
func newToken(owner string) (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return owner + ":" + hex.EncodeToString(b[:]), nil
}

var _ lock.ILockProvider = (*Provider)(nil)
var _ lock.ILeaseLockProvider = (*Provider)(nil)
