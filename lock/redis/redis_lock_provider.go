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

	// MaxLeaseDuration 租约单次最大生命周期上限（防调用方泄漏）。
	//
	// 语义：
	//   - > 0：到达该上限后停止续期并主动释放锁（防泄漏）；
	//   - == 0：使用默认值 5*TTL；
	//   - < 0：显式禁用上限，长任务不会被看门狗主动释放。
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
	} else {
		cfgCopy := *cfg
		cfg = &cfgCopy
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
	if maxLeaseDuration == 0 {
		// 未配置：用 5*TTL 兜底防泄漏；显式负数表示禁用上限（长任务）。
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
	var timer *time.Timer
	defer func() {
		if timer != nil {
			timer.Stop()
		}
	}()
	for {
		ok, err := p.client.SetNX(ctx, redisKey, token, p.ttl).Result()
		if err != nil {
			if stdErrors.Is(err, context.DeadlineExceeded) {
				return nil, errors.NewCodeWithCause(errors.Timeout, "lock acquire timeout", err).
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
				return nil, errors.NewCodeWithCause(errors.Timeout, "lock acquire timeout", ctx.Err()).
					WithContext("key", key).
					WithContext("ms", time.Since(start).Milliseconds())
			}
			return nil, ctx.Err()
		default:
		}

		if timer == nil {
			timer = time.NewTimer(p.pollInterval)
		} else {
			timer.Reset(p.pollInterval)
		}

		select {
		case <-timer.C:
		case <-ctx.Done():
			if stdErrors.Is(ctx.Err(), context.DeadlineExceeded) {
				return nil, errors.NewCodeWithCause(errors.Timeout, "lock acquire timeout", ctx.Err()).
					WithContext("key", key).
					WithContext("ms", time.Since(start).Milliseconds())
			}
			return nil, ctx.Err()
		}
	}
}

func (p *Provider) newLease(redisKey string, token string) *lock.Lease {
	safeLost := lock.NewSafeLostChan()
	stopRenew := p.startRenewal(redisKey, token, safeLost)
	return lock.NewLease(p.releaseFunc(redisKey, token, stopRenew, safeLost), safeLost.Channel())
}

var redisRenewLua = `
if redis.call("GET", KEYS[1]) == ARGV[1] then
  return redis.call("PEXPIRE", KEYS[1], ARGV[2])
else
  return 0
end
`

func (p *Provider) startRenewal(redisKey string, token string, safeLost *lock.SafeLostChan) func() {
	renewCtx, cancel := context.WithCancel(contextx.Background())
	interval := p.ttl / 3
	if interval <= 0 {
		interval = 100 * time.Millisecond
	}
	ttlMs := p.ttl.Milliseconds()
	renewScript := redis.NewScript(redisRenewLua)
	startTime := time.Now()

	go func() {
		// 无论因何退出（主动停止、达到上限、丢失租约、续期失败），
		// 都释放 renewCtx 及其上下文树：否则若上层仅监听 lease.Lost()
		// 而未显式 Release，关联的 timer/超时资源只能等 GC 回收。
		defer cancel()
		lastRenewTime := startTime
		timer := time.NewTimer(interval)
		defer timer.Stop()

		for {
			select {
			case <-renewCtx.Done():
				return
			case <-timer.C:
				if p.maxLeaseDuration > 0 && time.Since(startTime) >= p.maxLeaseDuration {
					p.logger.Warn(contextx.Background(), "lock lease max duration reached, stopping renewal to prevent leak",
						logging.String("redis_key", redisKey))
					safeLost.Close(errors.NewCode(errors.Timeout, "lock lease max duration reached"))
					return
				}

				const maxRenewAttempts = 3
				var renewErr error
				var lostLease bool

				backoff := interval / 4
				if backoff > 50*time.Millisecond {
					backoff = 50 * time.Millisecond
				}
				if backoff <= 0 {
					backoff = 10 * time.Millisecond
				}

				for attempt := 1; attempt <= maxRenewAttempts; attempt++ {
					if stdErrors.Is(renewCtx.Err(), context.Canceled) {
						return
					}
					// 剩余租约时间裁剪：单次尝试（含重试退避）不得把总耗时推过
					// 租约到期点，否则会在锁已被 Redis 自动释放后才退出循环，
					// 期间业务误以为仍持锁。
					remainingLease := p.ttl - time.Since(lastRenewTime)
					if remainingLease <= 0 {
						// renewErr 可能为 nil（首次尝试前租约已耗尽），Wrap(nil) 返回 nil，
						// 会把 lost 通道以 nil 错误关闭；必须显式构造错误。
						if renewErr == nil {
							renewErr = errors.NewCode(errors.Timeout, "lock renewal window exhausted before lease expiry")
						}
						safeLost.Close(errors.Wrap(renewErr, errors.Cache, "lock renewal failed: lease expired"))
						return
					}
					attemptTimeout := interval / time.Duration(maxRenewAttempts)
					if attemptTimeout <= 0 || attemptTimeout > 5*time.Second {
						attemptTimeout = 2 * time.Second
					}
					if attemptTimeout > remainingLease {
						attemptTimeout = remainingLease
					}
					execCtx, execCancel := context.WithTimeout(renewCtx, attemptTimeout)
					res, err := renewScript.Run(execCtx, p.client, []string{redisKey}, token, ttlMs).Result()
					execCancel()

					if err != nil {
						if stdErrors.Is(renewCtx.Err(), context.Canceled) {
							return
						}
						renewErr = err
						p.logger.Warn(contextx.Background(), "lock renewal transient failure, retrying",
							logging.Error(err), logging.String("redis_key", redisKey), logging.Int("attempt", attempt))
						if attempt < maxRenewAttempts {
							// 退避同样受剩余租约裁剪，避免退避本身跨越到期点。
							if wait := backoff; wait <= remainingLease {
								select {
								case <-renewCtx.Done():
									return
								case <-time.After(wait):
									continue
								}
							}
						}
						break
					}

					if n, ok := res.(int64); !ok || n == 0 {
						lostLease = true
						break
					}
					renewErr = nil
					lastRenewTime = time.Now()
					break
				}

				if lostLease {
					p.logger.Warn(contextx.Background(), "lock renewal lost lease (token mismatch or expired)",
						logging.String("redis_key", redisKey))
					safeLost.Close(errors.NewCode(errors.Conflict, "lock lease lost"))
					return
				}

				if renewErr != nil {
					p.logger.Warn(contextx.Background(), "lock renewal failed after retries",
						logging.Error(renewErr), logging.String("redis_key", redisKey))
					elapsed := time.Since(lastRenewTime)
					if elapsed >= p.ttl {
						safeLost.Close(errors.Wrap(renewErr, errors.Cache, "lock renewal failed: lease expired"))
						return
					}
					// 距离上次成功续租未超过 TTL，锁在 Redis 端仍有效，缩短间隔后快速重试
					retryDelay := interval / 2
					remaining := p.ttl - elapsed
					if retryDelay > remaining/2 {
						retryDelay = remaining / 2
					}
					if retryDelay <= 0 {
						retryDelay = 10 * time.Millisecond
					}
					timer.Reset(retryDelay)
					continue
				}

				timer.Reset(interval)
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
func (p *Provider) releaseFunc(redisKey string, token string, stopRenew func(), safeLost *lock.SafeLostChan) func() {
	releaseScript := redis.NewScript(redisReleaseLua)

	var once sync.Once
	return func() {
		once.Do(func() {
			if stopRenew != nil {
				stopRenew()
			}
			// 先关闭 lost 标记正常释放，防止停止续期协程并发上报假的 lost error
			safeLost.Close(nil)

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
